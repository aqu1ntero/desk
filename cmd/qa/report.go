package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// covMarker opens every report, so CI finds its own PR comment to update.
const covMarker = "<!-- qa-cover -->"

// covReport is what cover found, for -report: a pull request's comment.
type covReport struct {
	mod     string    // the module path, cut from package names
	link    string    // a blob URL at the checked commit (…/blob/<sha>), "" for plain paths
	failed  []string  // failing tests, then failing packages
	pkgs    []pkgCov  // in go test's order
	base    string    // -base, "" when changed lines weren't checked
	min     float64   // -min
	files   []fileCov // changed files with statements, by path
	checked bool      // the changed lines were counted
}

type pkgCov struct {
	name     string
	pct      float64
	floor    float64
	hasFloor bool
}

type fileCov struct {
	path       string
	hit, total int
	missed     []int // changed lines with statements that didn't run
}

var (
	failTest = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	failPkg  = regexp.MustCompile(`^FAIL\s+(\S+)\s`)
)

// failures is go test output's failing tests and packages.
func failures(out string) []string {
	var tests, pkgs []string
	for _, l := range strings.Split(out, "\n") {
		if m := failTest.FindStringSubmatch(l); m != nil {
			tests = append(tests, m[1])
		} else if m := failPkg.FindStringSubmatch(l); m != nil {
			pkgs = append(pkgs, m[1])
		}
	}
	return append(tests, pkgs...)
}

// changed is the changed lines' totals over every file.
func (r *covReport) changed() (hit, total int) {
	for _, f := range r.files {
		hit += f.hit
		total += f.total
	}
	return hit, total
}

// pctOf is hit of total in percent; nothing to cover is all covered.
func pctOf(hit, total int) float64 {
	if total == 0 {
		return 100
	}
	return 100 * float64(hit) / float64(total)
}

// bar is pct as ten squares, a square per full 10% (so only 100% fills
// it): green at or over good, else yellow, red under half.
func bar(pct, good float64) string {
	full := int(math.Floor(pct / 10))
	c := "🟩"
	switch {
	case pct < 50:
		c = "🟥"
	case pct < good:
		c = "🟨"
	}
	return strings.Repeat(c, full) + strings.Repeat("⬜", 10-full)
}

// spans is sorted lines as ranges: 3, 4, 5, 9 → [3 5] [9 9].
func spans(lines []int) [][2]int {
	var s [][2]int
	for _, l := range lines {
		if n := len(s); n > 0 && s[n-1][1] == l-1 {
			s[n-1][1] = l
		} else {
			s = append(s, [2]int{l, l})
		}
	}
	return s
}

// at is a span of path as text, a link when r has one.
func (r *covReport) at(path string, s [2]int) string {
	t, frag := fmt.Sprintf("L%d", s[0]), fmt.Sprintf("#L%d", s[0])
	if s[1] != s[0] {
		t, frag = fmt.Sprintf("L%d–%d", s[0], s[1]), fmt.Sprintf("#L%d-L%d", s[0], s[1])
	}
	if r.link == "" {
		return t
	}
	return fmt.Sprintf("[%s](%s/%s%s)", t, r.link, path, frag)
}

// markdown is the report as GitHub Markdown: a verdict, the changed lines,
// what didn't run, and every package against its floor.
func (r *covReport) markdown() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	hit, total := r.changed()
	pct := pctOf(hit, total)
	var under []pkgCov
	for _, p := range r.pkgs {
		if !p.hasFloor || p.pct < p.floor {
			under = append(under, p)
		}
	}
	short := func(p string) string { return strings.TrimPrefix(p, r.mod+"/") }

	var why []string
	if len(r.failed) > 0 {
		why = append(why, "tests failed")
	}
	if len(under) > 0 {
		why = append(why, fmt.Sprintf("%d package(s) under their floor", len(under)))
	}
	if r.checked && pct < r.min {
		why = append(why, fmt.Sprintf("changed lines under %.0f%%", r.min))
	}
	w("%s\n", covMarker)
	if len(why) == 0 {
		w("## ✅ Coverage passes\n\n")
	} else {
		w("## ❌ Coverage fails: %s\n\n", strings.Join(why, ", "))
	}

	w("| Changed lines | Packages | Tests |\n|:-:|:-:|:-:|\n")
	switch {
	case !r.checked && len(r.failed) > 0:
		w("| not counted |")
	case !r.checked:
		w("| — |")
	case total == 0:
		w("| no code lines changed |")
	default:
		w("| **%.1f%%** · %d of %d (min %.0f%%) |", pct, hit, total, r.min)
	}
	w(" **%d of %d** at their floor |", len(r.pkgs)-len(under), len(r.pkgs))
	if len(r.failed) > 0 {
		w(" ❌ %d failing |\n\n", len(r.failed))
	} else {
		w(" ✅ pass |\n\n")
	}

	if len(r.failed) > 0 {
		w("### Failing\n\n")
		for _, f := range r.failed {
			w("- `%s`\n", short(f))
		}
		w("\n")
	}

	if r.checked && total > 0 {
		w("### Changed files\n\n| File | Covered | |\n|---|--:|---|\n")
		for _, f := range r.files {
			p := pctOf(f.hit, f.total)
			w("| `%s` | %d / %d · %.1f%% | %s |\n", f.path, f.hit, f.total, p, bar(p, r.min))
		}
		if hit < total {
			w("\n<details><summary><b>%d changed line(s) not run by a test</b></summary>\n\n", total-hit)
			for _, f := range r.files {
				if len(f.missed) == 0 {
					continue
				}
				var at []string
				for _, s := range spans(f.missed) {
					at = append(at, r.at(f.path, s))
				}
				w("- `%s`: %s\n", f.path, strings.Join(at, ", "))
			}
			w("\n</details>\n")
		}
		w("\n")
	}

	open := ""
	if len(under) > 0 {
		open = " open"
	}
	w("<details%s><summary><b>Packages</b> (coverage against its floor)</summary>\n\n| Package | Coverage | Floor | |\n|---|--:|--:|---|\n", open)
	for _, p := range r.pkgs {
		floor, mark := fmt.Sprintf("%.0f%%", p.floor), "✅"
		switch {
		case !p.hasFloor:
			floor, mark = "none", "⚠️ add one to `floors` in cmd/qa"
		case p.pct < p.floor:
			mark = "❌"
		}
		w("| `%s` | %.1f%% %s | %s | %s |\n", short(p.name), p.pct, bar(p.pct, p.floor), floor, mark)
	}
	w("\n</details>\n\n<sub>`go run ./cmd/qa cover`")
	if r.base != "" {
		w(" since `%s`", r.base)
	}
	w(" · updated on every push</sub>\n")
	return b.String()
}

// write saves the report to path ("" none).
func (r *covReport) write(path string) {
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(r.markdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "qa: report:", err)
	}
}
