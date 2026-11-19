package kanban

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// titleOf is the first card's title of the board saved at path.
func titleOf(t *testing.T, path string) string {
	t.Helper()
	b, err := Load(path)
	if err != nil || len(b.Columns[0].Cards) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	return b.Columns[0].Cards[0].Title
}

// saveTitled saves a board whose one card is titled title.
func saveTitled(t *testing.T, path, title string) {
	t.Helper()
	b := Default()
	b.Add(0, Card{Title: title})
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
}

// TestSaveBackup: Save keeps the board it replaces as the backup, and keeps
// that one until it is an hour old, so a spoiled board doesn't spoil it too.
func TestSaveBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	saveTitled(t, path, "one")
	if _, err := os.Stat(BackupPath(path)); err == nil {
		t.Error("a backup of no board")
	}
	saveTitled(t, path, "two")
	saveTitled(t, path, "three")
	if got := titleOf(t, BackupPath(path)); got != "one" {
		t.Errorf("backup %q, want the first board's", got)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(BackupPath(path), old, old)
	saveTitled(t, path, "four")
	if got := titleOf(t, BackupPath(path)); got != "three" {
		t.Errorf("an hour on, backup %q, want the board just replaced", got)
	}
	if got := titleOf(t, path); got != "four" {
		t.Errorf("board %q", got)
	}
}

// TestLoadBackup: a board that doesn't read gives way to its backup; with
// no good backup either, it is still an error.
func TestLoadBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	saveTitled(t, path, "one")
	saveTitled(t, path, "two")
	os.WriteFile(path, nil, 0o600) // a power loss's empty file
	if got := titleOf(t, path); got != "one" {
		t.Errorf("loaded %q, want the backup's", got)
	}
	os.WriteFile(BackupPath(path), []byte("{"), 0o600)
	if b, err := Load(path); err == nil || b != nil {
		t.Error("a bad board and a bad backup loaded")
	}
}

// TestSaveFull: a disk that fills mid-write fails the save and leaves the
// board as it was.
func TestSaveFull(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	path := filepath.Join(t.TempDir(), "board.json")
	saveTitled(t, path, "kept")
	os.Symlink("/dev/full", path+".tmp")
	if err := Default().Save(path); err == nil {
		t.Error("a save onto a full disk")
	}
	if got := titleOf(t, path); got != "kept" {
		t.Errorf("board %q", got)
	}
}

// TestPrivate: the board, the chats and their folders are the user's alone,
// new ones and those from before.
func TestPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	dir := filepath.Join(t.TempDir(), "desk")
	path := filepath.Join(dir, "board.json")
	saveTitled(t, path, "one")
	chat := ChatPath(path, "a")
	AppendChat(chat, ChatMsg{Text: "hi"})
	mode := func(p string) os.FileMode {
		fi, _ := os.Stat(p)
		return fi.Mode().Perm()
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600, filepath.Dir(chat): 0o700, chat: 0o600} {
		if m := mode(p); m != want {
			t.Errorf("new %s: %o, want %o", p, m, want)
		}
	}
	old := filepath.Join(dir, "images")
	os.Mkdir(old, 0o755)
	os.WriteFile(filepath.Join(old, "a.png"), nil, 0o644)
	os.Chmod(path, 0o644)
	os.Symlink(path, filepath.Join(dir, "link")) // left alone: Chmod on a link changes its target
	Private(dir)
	for p, want := range map[string]os.FileMode{old: 0o700, filepath.Join(old, "a.png"): 0o600, path: 0o600} {
		if m := mode(p); m != want {
			t.Errorf("old %s: %o, want %o", p, m, want)
		}
	}
	Private(filepath.Join(dir, "none")) // no folder yet: nothing to do
}

// TestLock: one desk at a time has the board; the next is told, and takes
// it once the first lets go.
func TestLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desk", "board.json")
	f, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if g, err := Lock(path); !errors.Is(err, ErrLocked) || g != nil {
		t.Errorf("a second lock: %v", err)
	}
	f.Close()
	g, err := Lock(path)
	if err != nil {
		t.Fatalf("after the first let go: %v", err)
	}
	g.Close()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	if _, err := Lock(filepath.Join(file, "board.json")); err == nil {
		t.Error("a lock under a file")
	}
	os.Mkdir(filepath.Join(dir, "taken.json.lock"), 0o700)
	if _, err := Lock(filepath.Join(dir, "taken.json")); err == nil {
		t.Error("a lock onto a directory")
	}
}
