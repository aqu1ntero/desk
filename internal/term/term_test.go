package term

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestEcho: what the program prints lands on the grid, typed keys reach
// it, and its exit is seen.
func TestEcho(t *testing.T) {
	changed := make(chan struct{}, 64)
	tm, err := Start(exec.Command("sh", "-c", "printf 'hi\\n'; read x; echo got:$x"), 40, 5, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	screen := func() string {
		runs, _, _, _ := tm.Grid(0)
		var b strings.Builder
		for _, row := range runs {
			for _, r := range row {
				b.WriteString(r.Text)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}
	wait := func(what string) {
		for end := time.Now().Add(5 * time.Second); !strings.Contains(screen(), what); {
			if time.Now().After(end) {
				t.Fatalf("no %q on screen:\n%s", what, screen())
			}
			select {
			case <-changed:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	wait("hi")
	for _, r := range "ok" {
		tm.SendKey(uv.KeyPressEvent{Code: r, Text: string(r)})
	}
	tm.SendKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	wait("got:ok")
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if done, code := tm.Exited(); done {
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			return
		}
	}
	t.Fatal("exit not seen")
}
