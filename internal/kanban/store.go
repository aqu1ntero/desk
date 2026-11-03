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

// Load reads the board at path; a missing file is a fresh default board.
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
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b.migrate()
	return b, nil
}

// ChatMsg is one entry of a card's conversation.
type ChatMsg struct {
	Who    string    `json:"who"` // you, claude, tool, error
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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

// Save writes the board through a temp file and a rename, so a crash
// mid-write never leaves half a board.
func (b *Board) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
