package company

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

// Entry is one line of an organization's company log: work handed from
// one of its Claudes to another and what came back.
type Entry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // "brief" (work handed over), "result" (its answer), "message"
	From    string    `json:"from"` // a card's key, a role or department ("desk-qa", "security lead", "infra")
	To      string    `json:"to"`
	Card    string    `json:"card,omitempty"` // the card the work is for
	Text    string    `json:"text"`
	Verdict string    `json:"verdict,omitempty"` // a result's VERDICT: READY, BLOCKED, PARTIAL
}

// Dir is an organization's company folder, <Org>/.desk/company.
func Dir(orgDir string) string { return filepath.Join(orgDir, orgs.DeskDir, "company") }

// logMu keeps one line whole when several goroutines log at once.
var logMu sync.Mutex

// Log appends e to the organization's log.jsonl (in Dir), timed now when
// it isn't, its verdict read from the text when it has none.
func Log(orgDir string, e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if e.Verdict == "" {
		e.Verdict = Verdict(e.Text)
	}
	b, _ := json.Marshal(e) // strings and a time: it can't fail
	logMu.Lock()
	defer logMu.Unlock()
	if err := os.MkdirAll(Dir(orgDir), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Dir(orgDir), "log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Read is the organization's log, oldest first; lines it can't read are
// skipped.
func Read(orgDir string) []Entry {
	f, err := os.Open(filepath.Join(Dir(orgDir), "log.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Verdict is the word of text's last "VERDICT: …" line (READY, BLOCKED,
// PARTIAL); "" without one.
func Verdict(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	v, ok := strings.CutPrefix(strings.TrimSpace(lines[len(lines)-1]), "VERDICT:")
	if !ok {
		return ""
	}
	w, _, _ := strings.Cut(strings.TrimSpace(v), " ")
	return w
}
