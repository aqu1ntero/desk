package infra

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestLocalGo: under localGo (Windows: no sh) the local host's files are
// listed, copied both ways (a folder with a file and a link, modes kept),
// renamed, made, chmodded and removed in Go, with this OS's paths.
func TestLocalGo(t *testing.T) {
	old := localGo
	localGo = true
	defer func() { localGo = old }()
	ctx := context.Background()
	src, dst := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(src, "d"), 0o750)
	os.WriteFile(filepath.Join(src, "d", "f.txt"), []byte("hello"), 0o640)
	os.Symlink("f.txt", filepath.Join(src, "d", "ln"))
	var n atomic.Int64
	if err := copyToRemote(ctx, "local", "", src, dst, fileEntry{Name: "d", Dir: true}, &n, func() {}); err != nil || n.Load() != 5 {
		t.Fatalf("copy to: %v, %d bytes", err, n.Load())
	}
	if fi, err := os.Stat(filepath.Join(dst, "d", "f.txt")); err != nil || fi.Mode().Perm() != 0o640 && exeSuffix == "" { // no mode bits on Windows
		t.Fatalf("file: %v %v", fi, err)
	}
	if to, err := os.Readlink(filepath.Join(dst, "d", "ln")); err != nil || to != "f.txt" {
		t.Fatalf("link: %q %v", to, err)
	}
	abs, es, err := listRemote(ctx, "local", "", filepath.Join(dst, "d"))
	if err != nil || abs != filepath.Join(dst, "d") || len(es) != 2 {
		t.Fatalf("list: %q %v %v", abs, es, err)
	}
	back := t.TempDir()
	if err := copyFromRemote(ctx, "local", "", filepath.Join(dst, "d"), back, fileEntry{Name: "f.txt"}, &n, func() {}); err != nil || n.Load() != 10 {
		t.Fatalf("copy from: %v, %d bytes", err, n.Load())
	}
	if err := renameRemote(ctx, "local", "", filepath.Join(back, "f.txt"), filepath.Join(back, "g.txt")); err != nil {
		t.Fatal(err)
	}
	if err := mkdirRemote(ctx, "local", "", filepath.Join(back, "x", "y")); err != nil {
		t.Fatal(err)
	}
	if err := chmodRemote(ctx, "local", "", "600", []string{filepath.Join(back, "g.txt")}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(back, "g.txt")); fi.Mode().Perm() != 0o600 && exeSuffix == "" {
		t.Errorf("chmod: %v", fi.Mode())
	}
	if err := chmodRemote(ctx, "local", "", "u+x,g+r", []string{filepath.Join(back, "g.txt")}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(back, "g.txt")); fi.Mode().Perm() != 0o740 && exeSuffix == "" {
		t.Errorf("symbolic chmod: %v", fi.Mode())
	}
	// Windows keeps only the owner's write bit: a-w is read-only, u+w undoes it
	if err := chmodRemote(ctx, "local", "", "a-w", []string{filepath.Join(back, "g.txt")}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(back, "g.txt")); fi.Mode().Perm()&0o200 != 0 {
		t.Errorf("a-w: %v", fi.Mode())
	}
	chmodRemote(ctx, "local", "", "u+w", []string{filepath.Join(back, "g.txt")})
	if fi, _ := os.Stat(filepath.Join(back, "g.txt")); fi.Mode().Perm()&0o200 == 0 {
		t.Errorf("u+w: %v", fi.Mode())
	}
	if os.Geteuid() > 0 { // not ours to chmod (u+r changes nothing, even for root)
		if err := chmodLocal("u+r", []string{"/"}); err == nil {
			t.Error("chmod of /")
		}
	}
	if err := chmodRemote(ctx, "local", "", "17777", []string{filepath.Join(back, "g.txt")}); err == nil {
		t.Error("a mode past 7777")
	}
	if err := chmodRemote(ctx, "local", "", "644", []string{filepath.Join(back, "none")}); err == nil {
		t.Error("chmod of nothing")
	}
	if err := removeRemote(ctx, "local", "", []string{filepath.Join(back, "x"), filepath.Join(back, "none")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(back, "x")); err == nil {
		t.Error("not removed")
	}
	if os.Geteuid() != 0 && exeSuffix == "" { // a folder that keeps its files: mode bits
		os.Chmod(back, 0o555)
		defer os.Chmod(back, 0o755)
		if err := removeRemote(ctx, "local", "", []string{filepath.Join(back, "g.txt")}); err == nil {
			t.Error("removed from a read-only folder")
		}
	}
}

// TestParseMode: chmod's octal and symbolic modes, as GNU chmod applies
// them (but for the umask when no who is given).
func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		cur  uint64
		dir  bool
		want uint64
	}{
		{"640", 0o777, false, 0o640},
		{"4755", 0, false, 0o4755},
		{"u+x", 0o644, false, 0o744},
		{"go-w", 0o666, false, 0o644},
		{"+x", 0o600, false, 0o711},
		{"a=r", 0o4777, false, 0o444},
		{"u=rw,go=r", 0o777, false, 0o644},
		{"o-rwx", 0o777, false, 0o770},
		{"ug+rw", 0o404, false, 0o664},
		{"a+X", 0o644, false, 0o644},
		{"a+X", 0o744, false, 0o755},
		{"a+X", 0o600, true, 0o711},
		{"u+s,g+s", 0o755, false, 0o6755},
		{"o+s", 0o755, false, 0o755},
		{"+t", 0o777, true, 0o1777},
		{"u+t", 0o777, true, 0o777},
		{"g-s", 0o2755, true, 0o755},
	} {
		if got, err := parseMode(tc.mode, tc.cur, tc.dir); err != nil || got != tc.want {
			t.Errorf("%s on %o: %o %v, want %o", tc.mode, tc.cur, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "17777", "u", "u+", "u+q", "x+r", "u+x,", "rwx", "8"} {
		if _, err := parseMode(bad, 0o644, false); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	if m := unixMode(sftpMode(0o7531)); m != 0o7531 {
		t.Errorf("unixMode: %o", m)
	}
}

// TestCopyLocalFails: a cancelled copy, a missing source, a folder or file
// that can't be made, or one that can't be read fail; a half file goes.
func TestCopyLocalFails(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(src, "d"), 0o755)
	c := counter{new(atomic.Int64), func() {}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyLocal(ctx, filepath.Join(src, "f"), filepath.Join(dst, "f"), c); err == nil {
		t.Error("cancelled")
	}
	ctx = context.Background()
	for name, p := range map[string][2]string{
		"missing":     {filepath.Join(src, "none"), filepath.Join(dst, "a")},
		"file's dir":  {filepath.Join(src, "f"), filepath.Join(dst, "no", "f")},
		"folder made": {filepath.Join(src, "d"), filepath.Join(src, "f", "d")},
		"link made":   {filepath.Join(src, "d"), filepath.Join(dst, "d")},
	} {
		if name == "link made" {
			os.Symlink("f", filepath.Join(src, "d", "ln"))
			os.Mkdir(filepath.Join(dst, "d"), 0o755)
			os.WriteFile(filepath.Join(dst, "d", "ln"), nil, 0o644) // taken
		}
		if err := copyLocal(ctx, p[0], p[1], c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if os.Geteuid() != 0 && !localGo { // modes mean nothing to root, or on Windows
		os.WriteFile(filepath.Join(src, "locked"), []byte("x"), 0o000)
		os.Mkdir(filepath.Join(src, "shut"), 0o000)
		defer os.Chmod(filepath.Join(src, "shut"), 0o755)
		for _, name := range []string{"locked", "shut"} {
			if err := copyLocal(ctx, filepath.Join(src, name), filepath.Join(dst, name), c); err == nil {
				t.Errorf("unreadable %s: no error", name)
			}
		}
	}
	// a file whose read fails once open (Linux's /proc/self/mem at 0): the half file goes
	if _, err := os.Stat("/proc/self/mem"); err == nil {
		if err := copyLocal(ctx, "/proc/self/mem", filepath.Join(dst, "mem"), c); err == nil {
			t.Error("a failed read copied")
		}
		if _, err := os.Stat(filepath.Join(dst, "mem")); err == nil {
			t.Error("the half file stayed")
		}
	}
}

// TestNativeSide: the local host's side under localGo joins and climbs
// with this OS's paths; a server's with POSIX ones.
func TestNativeSide(t *testing.T) {
	n := &fileSide{remote: true, native: true, path: filepath.Join("a", "b")}
	if n.posix() || n.join("c") != filepath.Join("a", "b", "c") || n.parent() != "a" {
		t.Errorf("native: %q %q", n.join("c"), n.parent())
	}
	if r := (&fileSide{remote: true, path: "/a/b"}); !r.posix() || r.parent() != "/a" {
		t.Error("server")
	}
}
