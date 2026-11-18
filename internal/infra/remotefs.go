package infra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/diff"
)

// The infra Claude's file tools (file_read, file_write, file_edit) work on a
// server's files out of band, over the pane's own connection (runSh, so
// through a route's hops and as root with sudo), never typed into the shell:
// no quoting, no printf | cat, and the user approves every write as a diff.
// As Claude Code's own Edit, a file is written only after Claude read it
// and while it still is what it read; desk keeps the old content as a backup.

// fileMax is the largest file the tools read or write: it's held whole in
// memory (runSh buffers), but only a window of it reaches Claude. A
// variable so tests can make it small.
var fileMax = 64 << 20

// readMax is how much text one file_read gives Claude, and b64Max the
// largest file it gives whole as base64.
const (
	readMax = 256 << 10
	b64Max  = 1 << 20
)

// fileTarget is where a file tool works: a server alias, its infra dir and
// the name the user knows it by.
type fileTarget struct{ host, dir, label string }

// fileTarget is pane's server, or host's when one is named.
func (t *Tab) fileTarget(pane int, host string) (fileTarget, error) {
	if host != "" {
		n, err := t.findHost(host)
		if err != nil {
			return fileTarget{}, err
		}
		return fileTarget{n.host, n.dir, n.label}, nil
	}
	p, err := t.pane(pane)
	if err != nil {
		return fileTarget{}, err
	}
	return fileTarget{p.term.Host, p.dir, p.name}, nil
}

// readRemote is path on the target and whether it exists (no error then).
func readRemote(ctx context.Context, f fileTarget, path string, sudo bool) ([]byte, bool, error) {
	script := fmt.Sprintf(`f=%s; [ -e "$f" ] || exit 3; [ -f "$f" ] || { echo "not a regular file" >&2; exit 2; }
s=$(wc -c <"$f") || exit 1; [ "$s" -le %d ] || { echo "too big for the file tools: $s bytes" >&2; exit 2; }; cat -- "$f"`, shQuote(path), fileMax)
	var out bytes.Buffer
	err := runSh(ctx, f.host, f.dir, script, sudo, nil, &out)
	if ee, ok := err.(*exitError); ok && ee.code == 3 {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fileErr(err, sudo)
	}
	return out.Bytes(), true, nil
}

// writeRemote writes data to path atomically (a temp file beside it, then
// a rename): an existing file keeps its owner and mode (through a symlink,
// its target is written), a new one gets mode and its folders.
func writeRemote(ctx context.Context, f fileTarget, path string, data []byte, mode string, sudo bool) error {
	script := fmt.Sprintf(`f=%s; [ -L "$f" ] && f=$(readlink -f -- "$f"); d=$(dirname -- "$f")
mkdir -p -- "$d" || exit 1; t=$(mktemp "$d/.desk.XXXXXX") || exit 1
cat >"$t" || { rm -f -- "$t"; exit 1; }
if [ -e "$f" ]; then o=$(stat -c %%u:%%g -- "$f" 2>/dev/null || stat -f %%u:%%g -- "$f"); m=$(stat -c %%a -- "$f" 2>/dev/null || stat -f %%Lp -- "$f")
  chown "$o" "$t" 2>/dev/null; chmod "$m" "$t"; else chmod %s "$t"; fi
mv -f -- "$t" "$f" || { rm -f -- "$t"; exit 1; }`, shQuote(path), mode)
	return fileErr(runSh(ctx, f.host, f.dir, script, sudo, bytes.NewReader(data), nil), sudo)
}

// fileErr says what to do about a refusal: sudo for a permission, the
// user for a sudo that wants a password.
func fileErr(err error, sudo bool) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case sudo && strings.Contains(msg, "password is required"):
		return errors.New(msg + " (sudo here asks for a password, which the file tools can't give: ask the user)")
	case !sudo && strings.Contains(msg, "ermission denied"):
		return errors.New(msg + " (call it again with sudo: true to do it as root)")
	}
	return err
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func isBinary(b []byte) bool { return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 }

// seenKey is a file's key in Tab.fileSeen.
func seenKey(f fileTarget, path string) string { return f.host + ":" + path }

func (t *Tab) markSeen(f fileTarget, path string, b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fileSeen == nil {
		t.fileSeen = map[string]string{}
	}
	t.fileSeen[seenKey(f, path)] = digest(b)
}

// stagedFile is a file a card's Claude wrote for a server (desk peer
// file): it has the card's context and specialists, the infra Claude the
// server; file_write staged=N places it.
type stagedFile struct {
	from, host, path, mode, why string
	data                        []byte
}

// stage keeps r's file for t's Claude and says its number.
func (t *Tab) stage(r peerReq) (int, error) {
	switch {
	case r.Host == "" || !strings.HasPrefix(r.Path, "/"):
		return 0, errors.New("desk peer file needs --host (a server of the organization) and --path (absolute, on it)")
	case r.Mode != "" && !fileMode.MatchString(r.Mode):
		return 0, fmt.Errorf("--mode %q: octal digits, like 644", r.Mode)
	case len(r.Data) > fileMax:
		return 0, fmt.Errorf("too big for the file tools: %d bytes", len(r.Data))
	}
	if _, err := t.findHost(r.Host); err != nil {
		return 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.staged = append(t.staged, stagedFile{r.From, r.Host, r.Path, r.Mode, strings.TrimSpace(r.Why), r.Data})
	return len(t.staged), nil
}

// fromStaged fills a file_write's arguments from staged file n: its
// content, and its server, path and mode unless given.
func (t *Tab) fromStaged(a *fileArgs) (from string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a.Staged > len(t.staged) {
		return "", fmt.Errorf("no staged file #%d", a.Staged)
	}
	s := t.staged[a.Staged-1]
	a.Content, a.Encoding = base64.StdEncoding.EncodeToString(s.data), "base64"
	if a.Host == "" && a.Pane == 0 {
		a.Host = s.host
	}
	if a.Path == "" {
		a.Path = s.path
	}
	if a.Mode == "" {
		a.Mode = s.mode
	}
	if a.Reason == "" {
		a.Reason = s.why
	}
	return s.from, nil
}

// fileArgs are the file tools' arguments.
type fileArgs struct {
	Path       string `json:"path"`
	Host       string `json:"host"`
	Pane       int    `json:"pane"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Sudo       bool   `json:"sudo"`
	Content    string `json:"content"`
	Mode       string `json:"mode"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Reason     string `json:"reason"`
	Encoding   string `json:"encoding"` // "base64": a binary file whole
	Staged     int    `json:"staged"`   // file_write: a card's file (desk peer file), by number
	from       string // the card whose Claude wrote it
}

var fileMode = regexp.MustCompile(`^[0-7]{3,4}$`)

// fileTool runs one of the file tools.
func (t *Tab) fileTool(name string, a fileArgs) toolResult {
	fail := func(err error) toolResult { return toolResult{Text: err.Error(), IsError: true} }
	if name == "file_write" && a.Staged > 0 {
		from, err := t.fromStaged(&a)
		if err != nil {
			return fail(err)
		}
		a.from = from
	}
	if !strings.HasPrefix(a.Path, "/") {
		return fail(errors.New("path must be absolute"))
	}
	f, err := t.fileTarget(a.Pane, a.Host)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if name == "file_read" {
		return t.fileRead(ctx, f, a)
	}
	var change func(old string, exists bool) (string, error)
	op := "write"
	switch name {
	case "file_write":
		if a.Mode == "" {
			a.Mode = "644"
		}
		if !fileMode.MatchString(a.Mode) {
			return fail(fmt.Errorf("mode %q: give octal digits, like 644", a.Mode))
		}
		content := a.Content
		switch a.Encoding {
		case "":
		case "base64":
			b, err := base64.StdEncoding.DecodeString(a.Content)
			if err != nil {
				return fail(fmt.Errorf("content isn't base64: %w", err))
			}
			content = string(b)
		default:
			return fail(fmt.Errorf("encoding %q: base64 or none", a.Encoding))
		}
		change = func(string, bool) (string, error) { return content, nil }
	case "file_edit":
		op = "edit"
		change = func(old string, exists bool) (string, error) {
			n := strings.Count(old, a.OldString)
			switch {
			case !exists:
				return "", fmt.Errorf("%s doesn't exist: create it with file_write", a.Path)
			case a.OldString == "":
				return "", errors.New("old_string is empty")
			case n == 0:
				return "", errors.New("old_string isn't in the file: file_read it and copy the text exactly")
			case n > 1 && !a.ReplaceAll:
				return "", fmt.Errorf("old_string is in the file %d times: give more of its context, or replace_all", n)
			}
			return strings.ReplaceAll(old, a.OldString, a.NewString), nil
		}
	default:
		return fail(fmt.Errorf("unknown tool %s", name))
	}
	return t.filePut(ctx, f, a, op, change)
}

// fileRead is file_read: numbered lines, as Claude Code's Read gives them.
func (t *Tab) fileRead(ctx context.Context, f fileTarget, a fileArgs) toolResult {
	b, ok, err := readRemote(ctx, f, a.Path, a.Sudo)
	switch {
	case err != nil:
		return toolResult{Text: err.Error(), IsError: true}
	case !ok:
		return toolResult{Text: a.Path + " doesn't exist on " + f.label, IsError: true}
	case a.Encoding == "base64" && len(b) > b64Max:
		return toolResult{Text: fmt.Sprintf("%s has %d bytes, more than file_read gives whole (%d): look at it with run", a.Path, len(b), b64Max), IsError: true}
	case a.Encoding == "base64":
		t.markSeen(f, a.Path, b)
		return toolResult{Text: fmt.Sprintf("%s:%s · %d bytes, base64\n%s\n", f.label, a.Path, len(b), base64.StdEncoding.EncodeToString(b))}
	case isBinary(b):
		return toolResult{Text: fmt.Sprintf("%s is a binary file (%d bytes): read it with encoding base64", a.Path, len(b)), IsError: true}
	}
	t.markSeen(f, a.Path, b)
	lines := diff.Split(string(b))
	from := max(a.Offset, 1)
	limit := a.Limit
	if limit <= 0 {
		limit = 2000
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s:%s · %d lines\n", f.label, a.Path, len(lines))
	end := min(len(lines), from-1+limit)
	for i := from - 1; i < end; i++ {
		if out.Len() > readMax { // a window Claude's context takes
			end = i
			break
		}
		l := lines[i]
		if len(l) > 2000 {
			l = l[:2000] + "…"
		}
		fmt.Fprintf(&out, "%6d\t%s\n", i+1, l)
	}
	if end < len(lines) {
		fmt.Fprintf(&out, "… %d more lines: read on with offset %d\n", len(lines)-end, end+1)
	}
	return toolResult{Text: out.String()}
}

// filePut changes a file with change (given its content and whether it
// exists), once the user approves the diff: file_write and file_edit.
func (t *Tab) filePut(ctx context.Context, f fileTarget, a fileArgs, op string, change func(string, bool) (string, error)) toolResult {
	fail := func(err error) toolResult { return toolResult{Text: err.Error(), IsError: true} }
	old, exists, err := readRemote(ctx, f, a.Path, a.Sudo)
	if err != nil {
		return fail(err)
	}
	if exists {
		t.mu.Lock()
		seen := t.fileSeen[seenKey(f, a.Path)]
		t.mu.Unlock()
		switch {
		case isBinary(old) && a.Encoding != "base64":
			return fail(fmt.Errorf("%s is a binary file: replace it whole with file_write and encoding base64", a.Path))
		case seen == "":
			return fail(fmt.Errorf("file_read %s first: it exists, and you write only what you've read", a.Path))
		case seen != digest(old):
			return fail(fmt.Errorf("%s changed since you read it: file_read it again", a.Path))
		}
	}
	next, err := change(string(old), exists)
	if err != nil {
		return fail(err)
	}
	if exists && next == string(old) {
		return fail(errors.New("that changes nothing"))
	}
	if len(next) > fileMax {
		return fail(fmt.Errorf("too big for the file tools: %d bytes", len(next)))
	}
	text, add, del := diff.Unified(string(old), next, 3)
	if isBinary(old) || isBinary([]byte(next)) { // no lines to show: what it is before and after
		text, add, del = fmt.Sprintf("binary: %d bytes → %d bytes\n-sha256 %s\n+sha256 %s\n", len(old), len(next), digest(old), digest([]byte(next))), 0, 0
	}
	why := ""
	if !exists {
		op, why = "create", "a new file, mode "+a.Mode
	}
	if a.Sudo {
		why = strings.TrimPrefix(why+" · as root (sudo)", " · ")
	}
	if a.from != "" {
		why = strings.TrimPrefix(why+" · written by "+a.from+"'s Claude", " · ")
	}
	what := fmt.Sprintf("%s %s:%s", op, f.label, a.Path)
	r := t.approve(&approval{cmd: text, reason: a.Reason, why: why, pane: a.Pane, file: f.label + ":" + a.Path, title: "Claude wants to " + op + " a file", task: taskFrom(t)})
	verdict := "approved"
	if !r.ok {
		verdict = "denied"
	}
	audit(auditEntry{Org: t.org, Group: t.group, Host: f.host, Event: "file", By: "claude", Cmd: fmt.Sprintf("%s (+%d −%d)", what, add, del), Verdict: verdict})
	if !r.ok {
		return fail(errors.New("the user denied this" + r.note + ". Do not retry it; ask them what they want instead."))
	}
	// the user may have taken a while: write only over what they saw
	if now, ok, err := readRemote(ctx, f, a.Path, a.Sudo); err != nil || ok != exists || !bytes.Equal(now, old) {
		return fail(fmt.Errorf("%s changed while the user looked at the change: file_read it again", a.Path))
	}
	backup := ""
	if exists {
		if backup, err = keepBackup(t.org, f, a.Path, old); err != nil {
			return fail(fmt.Errorf("not written: keeping the old content failed: %w", err))
		}
	}
	if err := writeRemote(ctx, f, a.Path, []byte(next), a.Mode, a.Sudo); err != nil {
		return fail(err)
	}
	t.markSeen(f, a.Path, []byte(next))
	res := fmt.Sprintf("%s (+%d −%d)", what, add, del)
	if backup != "" {
		res += "; the old content is kept at " + backup
	}
	return toolResult{Text: res}
}

// keepBackup saves a file's content before a write, under desk's state
// (user only), and says where. ponytail: kept until the user clears them;
// prune by age if they pile up.
func keepBackup(org string, f fileTarget, path string, b []byte) (string, error) {
	dir := filepath.Join(stateDir(), "backups", orgDirName(org), filepath.Base(f.host))
	name := time.Now().Format("2006-01-02T15-04-05.000") + strings.ReplaceAll(path, "/", "_")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, b, 0o600)
}
