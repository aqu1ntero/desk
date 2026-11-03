package kanban

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMoveBeforeIgnored: an unknown card, a column out of range, or a card
// placed before itself leaves the board as it was.
func TestMoveBeforeIgnored(t *testing.T) {
	b := Default()
	a := b.Add(0, Card{Title: "a"})
	b.Add(0, Card{Title: "c"})
	for _, tc := range []struct {
		id     string
		col    int
		before string
	}{{"nope", 1, ""}, {a.ID, -1, ""}, {a.ID, len(b.Columns), ""}, {a.ID, 1, a.ID}} {
		b.MoveBefore(tc.id, tc.col, tc.before)
		if got := ids(b.Columns[0].Cards); !slices.Equal(got, []string{"a", "c"}) {
			t.Fatalf("MoveBefore(%q, %d, %q) moved: %v", tc.id, tc.col, tc.before, got)
		}
	}
}

// TestDeleteArchived: deleting an archived card drops it from the archive,
// keeping the others.
func TestDeleteArchived(t *testing.T) {
	b := Default()
	x := b.Add(0, Card{Title: "x"})
	y := b.Add(0, Card{Title: "y"})
	b.ArchiveCard(x.ID)
	b.ArchiveCard(y.ID)
	b.Delete(x.ID)
	if len(b.Archive) != 1 || b.Archive[0].ID != y.ID {
		t.Fatalf("archive %+v", b.Archive)
	}
}

// TestArchiveUnknown: archiving or restoring an unknown card changes
// nothing, and a board without Done archives none.
func TestArchiveUnknown(t *testing.T) {
	b := Default()
	b.Add(0, Card{Title: "a"})
	b.ArchiveCard("nope")
	b.Restore("nope")
	if len(b.Archive) != 0 || len(b.Columns[0].Cards) != 1 {
		t.Fatalf("board changed: %+v %+v", b.Archive, b.Columns[0].Cards)
	}
	b.Columns = []Column{{Title: "To do", Cards: []Card{{ID: "a"}}}}
	if n := b.ArchiveDone(); n != 0 || len(b.Columns[0].Cards) != 1 {
		t.Fatalf("ArchiveDone without Done = %d", n)
	}
}

// TestMatchesLinks: a card matches its links' refs and URLs.
func TestMatchesLinks(t *testing.T) {
	c := Card{Title: "x", Links: []Link{{Ref: "ABC-123", URL: "https://globex.example.com/browse/ABC-123"}}}
	if !c.Matches("ABC-123") || !c.Matches("globex.example") || c.Matches("initech") {
		t.Fatal("links not searched")
	}
}

// TestPath: XDG_DATA_HOME when set, else ~/.local/share.
func TestPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got := Path(); got != "/data/desk/board.json" {
		t.Fatalf("Path = %q", got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/acme")
	if got := Path(); got != "/home/acme/.local/share/desk/board.json" {
		t.Fatalf("Path = %q", got)
	}
}

// TestLoadErrors: an unreadable board or bad JSON is an error, not a fresh
// board (which would overwrite it on the next save).
func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if b, err := Load(dir); err == nil || b != nil {
		t.Fatalf("directory: %v %v", b, err)
	}
	bad := filepath.Join(dir, "board.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if b, err := Load(bad); err == nil || b != nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("bad json: %v %v", b, err)
	}
}

// TestWriteErrors: AppendChat and Save report a path they can't write.
func TestWriteErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if err := AppendChat(filepath.Join(file, "chats", "a.jsonl"), ChatMsg{}); err == nil {
		t.Error("AppendChat under a file")
	}
	if err := AppendChat(dir, ChatMsg{}); err == nil {
		t.Error("AppendChat onto a directory")
	}
	b := Default()
	if err := b.Save(filepath.Join(file, "board.json")); err == nil {
		t.Error("Save under a file")
	}
	os.Mkdir(filepath.Join(dir, "board.json.tmp"), 0o755)
	if err := b.Save(filepath.Join(dir, "board.json")); err == nil {
		t.Error("Save with its temp path taken by a directory")
	}
	b.Add(0, Card{Title: "far"}) // a time JSON can't hold
	b.Columns[0].Cards[0].Created = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "far.json")
	if err := b.Save(path); err == nil {
		t.Error("Save of an unmarshalable board")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("failed Save wrote a file")
	}
}
