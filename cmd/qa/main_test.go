package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestMutants: the changes mutate makes to a file, and none to its tests,
// its imports, its constants or a string's +.
func TestMutants(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package a

import "strings"

const limit = 1 + 2

func f(a, b int, c bool) string {
	if a == b && !c {
		a++
	}
	return strings.Repeat("x", a) + "y"
}

var on = 1 < 2
`), 0o644)
	os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package a\n\nvar _ = 1 == 2\n"), 0o644)
	ms, err := mutants(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.from+">"+m.to)
	}
	if want := []string{"&&>||", "==>!=", "!>", "++>--", "<>>="}; !slices.Equal(got, want) { // the outer && first; vars are mutated
		t.Fatalf("mutants %q, want %q", got, want)
	}
	if m := ms[1]; m.line != 8 || m.n != 2 {
		t.Errorf("== at line %d, %d bytes", m.line, m.n)
	}
	if m := coverLine.FindStringSubmatch("ok  \tdesk/internal/fzf\t0.007s\tcoverage: 74.6% of statements"); m == nil || m[1] != "desk/internal/fzf" || m[2] != "74.6" {
		t.Errorf("cover line: %q", m)
	}
}
