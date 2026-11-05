package orgs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

// TestDeskTomlMoves: desk.toml files still at an org's folder are found
// there (the search folder's own, a subfolder's under only_toml, a deeper
// one) and move to the org's .desk, once.
func TestDeskTomlMoves(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "desk.toml"), []byte(`org = "Top"`), 0o600)
	os.MkdirAll(filepath.Join(work, "Acme", "x"), 0o755)
	os.MkdirAll(filepath.Join(work, "Globex"), 0o755)
	os.WriteFile(filepath.Join(work, "Acme", "desk.toml"), []byte("agent = \"a\"\n"), 0o600)
	os.WriteFile(filepath.Join(work, "Acme", "x", "desk.toml"), []byte(`org = "Deep"`), 0o600)
	got, skipped := Scan([]Folder{{Path: work, Root: true, Depth: 2, OnlyToml: true}})
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"Top", "Acme", "Deep"}) || !slices.Equal(skipped, []string{"Globex"}) {
		t.Fatalf("found %v, skipped %v", names, skipped)
	}
	for _, d := range []string{work, filepath.Join(work, "Acme"), filepath.Join(work, "Acme", "x")} {
		if _, err := os.Stat(filepath.Join(d, "desk.toml")); err == nil || read(filepath.Join(d, DeskDir, "desk.toml")) == "" {
			t.Errorf("%s: desk.toml not moved", d)
		}
	}
	if c, err := Load(work, "Acme"); err != nil || c.Agent != "a" {
		t.Errorf("moved file reads %+v %v", c, err)
	}
}

// TestDeskTomlNotRoot: a search folder that isn't an org keeps its desk.toml.
func TestDeskTomlNotRoot(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "desk.toml"), []byte(`org = "Top"`), 0o600)
	Scan([]Folder{{Path: work, Depth: 1}})
	AccountAt(work, work)
	if read(filepath.Join(work, "desk.toml")) == "" {
		t.Error("moved the desk.toml of a folder that is no org")
	}
}

// TestDeskTomlBoth: with both files the new one is read and the old one left.
func TestDeskTomlBoth(t *testing.T) {
	root := t.TempDir()
	writeAt(filepath.Join(root, "Acme", DeskDir, "desk.toml"), []byte("agent = \"new\"\n"), 0o600)
	os.WriteFile(filepath.Join(root, "Acme", "desk.toml"), []byte("agent = \"old\"\n"), 0o600)
	if c, _ := Load(root, "Acme"); c.Agent != "new" {
		t.Errorf("read %q, want the new one", c.Agent)
	}
	if read(filepath.Join(root, "Acme", "desk.toml")) != "agent = \"old\"\n" {
		t.Error("the old one changed")
	}
}

// TestMovedLinks: a relative link moves as one that still points at its
// file; an absolute one moves as it is.
func TestMovedLinks(t *testing.T) {
	org := t.TempDir()
	os.WriteFile(filepath.Join(org, "shared.toml"), []byte("rel"), 0o600)
	os.Symlink("shared.toml", filepath.Join(org, "desk.toml"))
	abs := filepath.Join(t.TempDir(), "servers")
	os.WriteFile(abs, []byte("abs"), 0o600)
	os.Symlink(abs, filepath.Join(org, "ssh_config"))
	for name, want := range map[string]string{"desk.toml": "rel", "ssh_config": "abs"} {
		p := Moved(org, name)
		if p != filepath.Join(org, DeskDir, name) || read(p) != want {
			t.Errorf("%s: %s reads %q", name, p, read(p))
		}
		if _, err := os.Lstat(filepath.Join(org, name)); err == nil {
			t.Errorf("%s: the old link stayed", name)
		}
	}
}

// TestMovedFails: when the file can't move, the old one is still the one read.
func TestMovedFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only folders")
	}
	org := t.TempDir()
	os.WriteFile(filepath.Join(org, "desk.toml"), []byte("x"), 0o600)
	os.Symlink("desk.toml", filepath.Join(org, "link"))
	os.Chmod(org, 0o555) // no .desk can be made
	if p := Moved(org, "desk.toml"); p != filepath.Join(org, "desk.toml") {
		t.Errorf("no .desk: %s", p)
	}
	os.Chmod(org, 0o755)
	os.Mkdir(filepath.Join(org, DeskDir), 0o555) // .desk takes nothing
	if p := Moved(org, "link"); p != filepath.Join(org, "link") {
		t.Errorf("link into a read-only .desk: %s", p)
	}
	os.Chmod(filepath.Join(org, DeskDir), 0o755)
	os.Chmod(org, 0o555) // the old one can't leave
	if p := Moved(org, "desk.toml"); p != filepath.Join(org, "desk.toml") {
		t.Errorf("read-only org: %s", p)
	}
	os.Chmod(org, 0o755)
	if p := Moved(org, "none"); p != filepath.Join(org, DeskDir, "none") {
		t.Errorf("nothing to move: %s", p)
	}
}
