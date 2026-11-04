package infra

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The audit log: one JSON line per command finished in any pane (yours or
// Claude's) and per approval answer, in $XDG_STATE_HOME/desk/infra/audit/<org>/<day>.jsonl.
// Outputs are cut to their tail and masked like everything sent to Claude.

type auditEntry struct {
	Time    time.Time `json:"time"`
	Org     string    `json:"org"`
	Group   string    `json:"group,omitempty"`
	Host    string    `json:"host,omitempty"`
	Event   string    `json:"event"`        // command | approval
	By      string    `json:"by,omitempty"` // you | claude
	Cmd     string    `json:"cmd"`
	Exit    *int      `json:"exit,omitempty"`
	DurMS   int64     `json:"dur_ms,omitempty"`
	Output  string    `json:"output,omitempty"`
	Verdict string    `json:"verdict,omitempty"` // approved | always | denied | stopped
}

var auditMu sync.Mutex

// stateDir is $XDG_STATE_HOME/desk/infra: the audit log, "always" answers.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "desk", "infra")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "desk", "infra")
}

func auditDir() string { return filepath.Join(stateDir(), "audit") }

// orgDirName is org as a file name ("local" for none).
func orgDirName(org string) string {
	if b := filepath.Base(org); b != "." && b != "/" && org != "" {
		return b
	}
	return "local"
}

// audit appends e to its org's file for the day. Errors are dropped: the log
// must never get in the way of the terminal.
func audit(e auditEntry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Cmd, e.Output = redact(e.Cmd), redact(e.Output)
	b, _ := json.Marshal(e)
	dir := filepath.Join(auditDir(), orgDirName(e.Org))
	auditMu.Lock()
	defer auditMu.Unlock()
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, e.Time.Format("2006-01-02")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
	f.Close()
}

// auditBlock logs a finished command of a pane of t.
func auditBlock(t *Tab, host string, b Block) {
	by, exit := "you", b.Exit
	if b.ByClaude {
		by = "claude"
	}
	audit(auditEntry{Org: t.org, Group: t.group, Host: host, Event: "command", By: by, Cmd: b.Cmd,
		Exit: &exit, DurMS: b.Dur.Milliseconds(), Output: tailLines(b.Output, 20, 4000)})
}

// blockDone runs on the terminal's goroutine as a command of a pane of t
// finishes: it's logged, and a long one may be notified.
func blockDone(t *Tab, host string, b Block) {
	auditBlock(t, host, b)
	if b.Dur >= longCommand {
		go dispatch(blockDoneMsg{t, host, b}) // not under the terminal's lock
	}
}

// tailLines keeps the last n lines of s, at most max bytes.
func tailLines(s string, n, max int) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		s = strings.Join(lines[len(lines)-n:], "\n")
	}
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

// Secret masking: what reaches Claude (prompts, tool results) and the audit
// log goes through redact. ponytail: patterns, not a parser; a secret in an
// unusual shape still gets through, add patterns as they show up.

// secretKey is a key that names a secret and its separator: "DB_PASSWORD=", "api_key": .
const secretKey = `(?:[a-z0-9]+[_.-])*(?:pass(?:word|wd|phrase)?|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credentials?)["']?[ \t]*[:=][ \t]*`

var secretRes = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?s)-----BEGIN ([A-Z ]*)PRIVATE KEY-----.*?(-----END ([A-Z ]*)PRIVATE KEY-----|\z)`), "-----BEGIN ${1}PRIVATE KEY----- [redacted]"},
	{regexp.MustCompile(`(?i)\b(authorization:\s*(?:bearer|basic|token)\s+)\S+`), "${1}[redacted]"},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+@`), "${1}[redacted]@"},
	{regexp.MustCompile(`(?i)(` + secretKey + `)(["'])[^"'\n]*["']`), "${1}${2}[redacted]${2}"},
	{regexp.MustCompile(`(?i)(` + secretKey + `)[^\s,;"'}]+`), "${1}[redacted]"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "[redacted]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), "[redacted]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), "[redacted]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), "[redacted]"},
}

func redact(s string) string {
	for _, r := range secretRes {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// recorder writes a terminal's output as an asciicast v2 file (asciinema
// play replays it), unless the org's desk.toml says record = false.
// The terminal hands each chunk over (event, under its lock) and a goroutine
// of the recorder's own encodes and writes it, buffered, flushed every second
// and on close.
type recorder struct {
	f      *os.File
	start  time.Time
	carry  []byte // an incomplete UTF-8 sequence at the end of the last chunk (the writer's)
	ch     chan recEvent
	done   chan struct{} // closed once the writer flushed and closed the file
	closed bool          // close ran: later events (a resize) are dropped
}

type recEvent struct {
	at   time.Duration // since start, taken as it happened
	kind string
	b    []byte
}

// recFlush is how often a recording reaches the disk while output flows.
const recFlush = time.Second

func recordingPath(org, server string, at time.Time) string {
	return filepath.Join(stateDir(), "recordings", orgDirName(org), at.Format("2006-01-02-150405")+"-"+strings.ReplaceAll(server, "/", "_")+".cast")
}

func newRecorder(p string, w, h int) (*recorder, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	for n := 2; errors.Is(err, fs.ErrExist) && n < 100; n++ { // a second pane on that server within the same second
		f, err = os.OpenFile(fmt.Sprintf("%s-%d.cast", strings.TrimSuffix(p, ".cast"), n), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return nil, err
	}
	p = f.Name()
	now := time.Now()
	hdr, _ := json.Marshal(map[string]any{"version": 2, "width": w, "height": h, "timestamp": now.Unix(), "env": map[string]string{"TERM": "xterm-256color"}})
	f.Write(append(hdr, '\n'))
	recordingsOpen.Store(p, true)
	r := &recorder{f: f, start: now, ch: make(chan recEvent, 256), done: make(chan struct{})}
	go r.write()
	return r, nil
}

// close flushes what is queued, closes the file and waits for both.
func (r *recorder) close() {
	if r.closed {
		return
	}
	r.closed = true
	close(r.ch)
	<-r.done
	recordingsOpen.Delete(r.f.Name())
}

// write encodes the events as they come, buffered.
func (r *recorder) write() {
	w := bufio.NewWriter(r.f)
	tick := time.NewTicker(recFlush)
	defer tick.Stop()
	for {
		select {
		case e, ok := <-r.ch:
			if !ok {
				w.Flush()
				r.f.Close()
				close(r.done)
				return
			}
			r.encode(w, e)
		case <-tick.C:
			w.Flush()
		}
	}
}

// recordingsOpen holds the paths panes are still writing: the cleaner keeps them.
var recordingsOpen sync.Map

// Recordings are kept a week and to 1 GiB per org.
const recordingsMaxAge, recordingsMaxBytes = 7 * 24 * time.Hour, 1 << 30

// pruneRecordings deletes, per org, recordings older than a week, then the
// oldest ones until the org's total fits in 1 GiB. Recordings still being
// written are never deleted (they count toward the total).
func pruneRecordings(now time.Time) {
	orgs, _ := os.ReadDir(filepath.Join(stateDir(), "recordings"))
	for _, o := range orgs {
		dir := filepath.Join(stateDir(), "recordings", o.Name())
		ents, _ := os.ReadDir(dir)
		type cast struct {
			path string
			size int64
			mod  time.Time
		}
		var casts []cast
		var total int64
		for _, e := range ents {
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			c := cast{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()}
			if _, open := recordingsOpen.Load(c.path); !open && now.Sub(c.mod) > recordingsMaxAge {
				os.Remove(c.path)
				continue
			}
			casts, total = append(casts, c), total+c.size
		}
		slices.SortFunc(casts, func(a, b cast) int { return a.mod.Compare(b.mod) })
		for _, c := range casts {
			if total <= recordingsMaxBytes {
				break
			}
			if _, open := recordingsOpen.Load(c.path); !open && os.Remove(c.path) == nil {
				total -= c.size
			}
		}
	}
}

// cleanRecordings prunes now and then every hour.
func cleanRecordings() {
	for {
		pruneRecordings(time.Now())
		time.Sleep(time.Hour)
	}
}

// event queues one event: "o" output, "r" a resize ("WxH"). b is copied (the
// reader reuses its buffer).
func (r *recorder) event(kind string, b []byte) {
	if !r.closed {
		r.ch <- recEvent{time.Since(r.start), kind, bytes.Clone(b)}
	}
}

// encode writes one event as an asciicast line.
func (r *recorder) encode(w io.Writer, e recEvent) {
	b := e.b
	if e.kind == "o" {
		b = append(r.carry, b...)
		cut := len(b)
		for i := len(b) - 1; i >= 0 && i >= len(b)-3; i-- { // hold back a split rune
			if !utf8.RuneStart(b[i]) {
				continue
			}
			if !utf8.FullRune(b[i:]) {
				cut = i
			}
			break
		}
		b, r.carry = b[:cut], append([]byte(nil), b[cut:]...)
	}
	line, _ := json.Marshal([]any{float64(e.at.Microseconds()) / 1e6, e.kind, string(b)})
	w.Write(append(line, '\n'))
}
