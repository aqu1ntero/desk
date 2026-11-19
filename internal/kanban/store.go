package kanban

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Path is where the board lives: $XDG_DATA_HOME/desk/board.json.
func Path() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "desk", "board.json")
}

// Load reads the board at path; a missing file is a fresh default board. A
// board that doesn't read (cut short by a power loss) gives way to the one
// Save kept before it (BackupPath).
func Load(path string) (*Board, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	b := &Board{}
	if err := json.Unmarshal(data, b); err != nil {
		bak, berr := os.ReadFile(BackupPath(path))
		if b = (&Board{}); berr != nil || json.Unmarshal(bak, b) != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	b.migrate()
	return b, nil
}

// BackupPath is an earlier board, kept by Save.
func BackupPath(path string) string { return path + ".bak" }

// backupEvery is how old the backup gets before Save keeps a newer one: a
// bug or a slip that spoils the board leaves the one from before it, not
// the one saved half a second earlier.
const backupEvery = time.Hour

// backupDue says whether Save keeps the board it replaces as backup bak.
func backupDue(bak string, now time.Time) bool {
	fi, err := os.Stat(bak)
	return err != nil || now.Sub(fi.ModTime()) >= backupEvery
}

// Private makes desk's data folder dir (the board's) and everything in it
// readable by the user alone: chats hold commands, their output and servers'
// details. Boards from before it were made world-readable.
func Private(dir string) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
		case d.IsDir():
			os.Chmod(p, 0o700)
		case d.Type().IsRegular():
			os.Chmod(p, 0o600)
		}
		return nil
	})
}

// ChatMsg is one entry of a card's conversation.
type ChatMsg struct {
	Who    string    `json:"who"`            // you, claude, tool, error
	Name   string    `json:"name,omitempty"` // lead: the department lead's name
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Cmd    string    `json:"cmd,omitempty"`    // tool: a shell command, whole
	Detail string    `json:"detail,omitempty"` // tool: Claude's description of it
	Path   string    `json:"path,omitempty"`   // tool: the file an Edit or Write changed
	Edits  []Edit    `json:"edits,omitempty"`  // tool: an Edit's changes
	Lines  int       `json:"lines,omitempty"`  // tool: a Write's lines and bytes
	Bytes  int       `json:"bytes,omitempty"`
}

// Edit is one change of an Edit tool use: Old replaced by New.
type Edit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ChatPath is where card id's conversation lives, beside the board.
func ChatPath(boardPath, id string) string {
	return filepath.Join(filepath.Dir(boardPath), "chats", id+".jsonl")
}

// AppendChat adds m to the conversation file at path.
func AppendChat(path string, m ChatMsg) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(m)
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadChat reads a conversation; a missing file is an empty one, and a torn
// last line (a crash mid-append) is skipped.
func LoadChat(path string) []ChatMsg {
	data, _ := os.ReadFile(path)
	var out []ChatMsg
	for _, l := range strings.Split(string(data), "\n") {
		var m ChatMsg
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// Save writes the board through a temp file synced to disk and a rename, so
// neither a crash nor a power loss mid-write leaves half a board; the board
// it replaces stays as BackupPath, a link to the same file, for Load.
func (b *Board) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if bak := BackupPath(path); backupDue(bak, time.Now()) {
		os.Remove(bak)
		os.Link(path, bak) // none yet, or a filesystem without links: no backup this time
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // the rename itself to disk; Windows can't sync a folder
		d.Sync()
		d.Close()
	}
	return nil
}
