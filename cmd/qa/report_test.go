package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stderr returns what f printed to standard error.
func stderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	defer func() { os.Stderr = old }()
	return stdout(t, func() { os.Stderr = os.Stdout; f() }) // stdout's pipe takes both
}

// TestFailures: failing tests first, then failing packages; passing ones
// and other lines are not failures.
func TestFailures(t *testing.T) {
	out := "=== RUN   TestA\n--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n--- PASS: TestB\nFAIL\nFAIL\tdesk/internal/a\t0.01s\nok  \tdesk/internal/b\t0.01s\n"
	if got := failures(out); !slices.Equal(got, []string{"TestA", "TestA/sub", "desk/internal/a"}) {
		t.Errorf("%q", got)
	}
}

// TestSpans: runs of lines become ranges.
func TestSpans(t *testing.T) {
	got := spans([]int{3, 4, 5, 9, 11, 12})
	if !slices.Equal(got, [][2]int{{3, 5}, {9, 9}, {11, 12}}) {
		t.Errorf("%v", got)
	}
	if spans(nil) != nil {
		t.Error("none: not nil")
	}
}

// TestBar: ten squares, green at or over the bar, yellow under, red under half.
func TestBar(t *testing.T) {
	for _, c := range []struct {
		pct, good float64
		want      string
	}{
		{100, 98, strings.Repeat("🟩", 10)},
		{98, 98, strings.Repeat("🟩", 9) + "⬜"},
		{97.9, 98, strings.Repeat("🟨", 9) + "⬜"},
		{69.9, 98, strings.Repeat("🟨", 6) + strings.Repeat("⬜", 4)},
		{50, 98, strings.Repeat("🟨", 5) + strings.Repeat("⬜", 5)},
		{49.9, 98, strings.Repeat("🟥", 4) + strings.Repeat("⬜", 6)},
		{0, 98, strings.Repeat("⬜", 10)},
	} {
		if got := bar(c.pct, c.good); got != c.want {
			t.Errorf("bar(%v, %v) = %s", c.pct, c.good, got)
		}
	}
}

// TestPctOf: nothing to cover is all covered.
func TestPctOf(t *testing.T) {
	if pctOf(0, 0) != 100 || pctOf(1, 4) != 25 {
		t.Error(pctOf(0, 0), pctOf(1, 4))
	}
}

// TestReportPass: a passing report: its verdict, the changed lines with the
// ones not run linked by range, and the packages folded away.
func TestReportPass(t *testing.T) {
	r := &covReport{mod: "desk", link: "https://example.com/o/r/blob/abc", base: "origin/main", min: 98, checked: true,
		pkgs: []pkgCov{{name: "desk/internal/a", pct: 99.5, floor: 99, hasFloor: true}},
		files: []fileCov{
			{path: "internal/a/a.go", hit: 197, total: 200, missed: []int{10, 11, 40}},
			{path: "internal/a/b.go", hit: 3, total: 3},
		}}
	md := r.markdown()
	for _, want := range []string{
		covMarker + "\n## ✅ Coverage passes\n",
		"| **98.5%** · 200 of 203 (min 98%) | **1 of 1** at their floor | ✅ pass |",
		"| `internal/a/a.go` | 197 / 200 · 98.5% | " + strings.Repeat("🟩", 9) + "⬜ |",
		"| `internal/a/b.go` | 3 / 3 · 100.0% | " + strings.Repeat("🟩", 10) + " |",
		"<b>3 changed line(s) not run by a test</b>",
		"- `internal/a/a.go`: [L10–11](https://example.com/o/r/blob/abc/internal/a/a.go#L10-L11), [L40](https://example.com/o/r/blob/abc/internal/a/a.go#L40)\n",
		"<details><summary><b>Packages</b>",
		"| `internal/a` | 99.5% " + strings.Repeat("🟩", 9) + "⬜ | 99% | ✅ |",
		"since `origin/main`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "internal/a/b.go`:") || strings.Contains(md, "Failing") {
		t.Errorf("a fully run file listed, or failures:\n%s", md)
	}

	r.link, r.files = "", r.files[:1]
	if md := r.markdown(); !strings.Contains(md, "- `internal/a/a.go`: L10–11, L40\n") {
		t.Errorf("no link: plain ranges:\n%s", md)
	}
	r.files = []fileCov{{path: "internal/a/a.go", hit: 2, total: 2}}
	if md := r.markdown(); strings.Contains(md, "not run by a test") {
		t.Errorf("all run: no list:\n%s", md)
	}
}

// TestReportFail: each reason shows in the verdict; failing tests are
// listed; packages under their floor, or with none, open the packages.
func TestReportFail(t *testing.T) {
	r := &covReport{mod: "desk", min: 98, checked: true,
		failed: []string{"TestA", "desk/internal/a"},
		pkgs: []pkgCov{
			{name: "desk/internal/b", pct: 90, floor: 95, hasFloor: true},
			{name: "desk/internal/c", pct: 40},
		},
		files: []fileCov{{path: "internal/b/b.go", hit: 1, total: 2, missed: []int{7}}}}
	md := r.markdown()
	for _, want := range []string{
		"## ❌ Coverage fails: tests failed, 2 package(s) under their floor, changed lines under 98%\n",
		"| **50.0%** · 1 of 2 (min 98%) | **0 of 2** at their floor | ❌ 2 failing |",
		"### Failing\n\n- `TestA`\n- `internal/a`\n",
		"| `internal/b/b.go` | 1 / 2 · 50.0% | " + strings.Repeat("🟨", 5) + strings.Repeat("⬜", 5) + " |",
		"<details open><summary><b>Packages</b>",
		"| `internal/b` | 90.0% " + strings.Repeat("🟨", 9) + "⬜ | 95% | ❌ |",
		"| `internal/c` | 40.0% " + strings.Repeat("🟥", 4) + strings.Repeat("⬜", 6) + " | none | ⚠️ add one to `floors` in cmd/qa |",
		"<sub>`go run ./cmd/qa cover` · updated",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in:\n%s", want, md)
		}
	}
}

// TestReportUnchecked: changed lines not counted (no -base, tests failed
// first) or none changed say so, with no files part.
func TestReportUnchecked(t *testing.T) {
	r := &covReport{min: 98}
	if md := r.markdown(); !strings.Contains(md, "| — | **0 of 0** at their floor | ✅ pass |") || strings.Contains(md, "Changed files") {
		t.Errorf("no -base:\n%s", md)
	}
	r.failed = []string{"TestA"}
	if md := r.markdown(); !strings.Contains(md, "| not counted |") || strings.Contains(md, "under 98%") {
		t.Errorf("tests failed:\n%s", md)
	}
	r = &covReport{min: 98, checked: true}
	if md := r.markdown(); !strings.Contains(md, "| no code lines changed |") || !strings.Contains(md, "✅ Coverage passes") || strings.Contains(md, "Changed files") {
		t.Errorf("nothing changed:\n%s", md)
	}
}

// TestReportWrite: written where asked, nowhere without a path, and a
// folder that isn't there is said, not fatal.
func TestReportWrite(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	r := &covReport{min: 98}
	r.write("")
	if ds, _ := os.ReadDir(dir); len(ds) != 0 {
		t.Errorf("no path wrote %v", ds)
	}
	p := filepath.Join(dir, "r.md")
	r.write(p)
	if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), covMarker) {
		t.Errorf("%q", b)
	}
	out := stderr(t, func() { r.write(filepath.Join(dir, "no", "r.md")) })
	if !strings.Contains(out, "qa: report:") {
		t.Errorf("stderr %q", out)
	}
}
