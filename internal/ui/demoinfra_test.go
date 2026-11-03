package ui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/infra"
)

// TestDemoInfra: the demo's servers file is Globex's, its hosts are a bash
// that answers like web-02 with /var full (until the logs are rotated), and
// the infra Claude is desk itself as demo-infra on the tab's socket.
func TestDemoInfra(t *testing.T) {
	defer func() { infra.HostCmd, infra.ClaudeCmd = nil, nil }()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "home", "Work", "Globex"), 0o755)
	if err := demoInfra(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "home", "Work", "Globex", "ssh_config")); string(b) != demoServers {
		t.Fatalf("servers file %q", b)
	}
	c := infra.HostCmd("web-02")
	if c.Args[0] != "bash" || !strings.Contains(strings.Join(c.Env, " "), "DESK_DEMO_HOST=web-02") {
		t.Fatalf("host %v %v", c.Args, c.Env)
	}
	run := func(host, script string) string {
		home := filepath.Join(dir, "hosts", host)
		os.MkdirAll(home, 0o755)
		cmd := exec.Command("bash", "-c", "source "+filepath.Join(dir, "host.rc")+"; "+script)
		cmd.Env = append(os.Environ(), "DESK_DEMO_HOST="+host, "HOME="+home)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	before := run("web-02", `df -h /var; du -sh /var/log/billing; systemctl status billing-api; echo "exit $?"; systemctl status nginx; uptime; sudo echo sudo; clear`)
	for _, w := range []string{"100% /var", "37G", "no space left on device", "exit 3", "active (running)", "load average", "sudo", "\033[2J"} {
		if !strings.Contains(before, w) {
			t.Errorf("before: no %q", w)
		}
	}
	after := run("web-02", `sudo logrotate -f x; sudo systemctl restart billing-api; df -h /var; du -sh /var/log/billing; systemctl status billing-api; echo "exit $?"`)
	for _, w := range []string{"26% /var", "1.2G", "active (running)", "exit 0"} {
		if !strings.Contains(after, w) {
			t.Errorf("after: no %q", w)
		}
	}
	if out := run("web-01", "df -h /var"); !strings.Contains(out, "26% /var") {
		t.Error("web-01's /var is fine")
	}
	exe, _ := os.Executable()
	cfg := `{"mcpServers":{"desk":{"type":"stdio","command":"desk","args":["mcp","/run/x.sock","run"]}}}`
	if c := infra.ClaudeCmd("-p", "--mcp-config", cfg, "--verbose"); strings.Join(c.Args, " ") != exe+" demo-infra /run/x.sock" {
		t.Fatalf("claude %q", c.Args)
	}
}

// TestDemoInfraFails: a world it can't write says so.
func TestDemoInfraFails(t *testing.T) {
	defer func() { infra.HostCmd, infra.ClaudeCmd = nil, nil }()
	dir := t.TempDir()
	if err := demoInfra(dir); err == nil { // no Globex folder for its servers file
		t.Error("servers file in a missing folder")
	}
	if err := demoInfra(filepath.Join(dir, "missing")); err == nil {
		t.Error("rc in a missing folder")
	}
}

// TestDemoSock: the tools socket from claude's --mcp-config, else "".
func TestDemoSock(t *testing.T) {
	for want, args := range map[string][]string{
		"/s.sock": {"--mcp-config", `{"mcpServers":{"desk":{"args":["mcp","/s.sock"]}}}`},
		"":        {"-p"},
	} {
		if got := demoSock(args); got != want {
			t.Errorf("%q: %q", args, got)
		}
	}
	for _, args := range [][]string{{"--mcp-config"}, {"--mcp-config", "{"}, {"--mcp-config", `{"mcpServers":{"desk":{"args":["mcp"]}}}`}} {
		if got := demoSock(args); got != "" {
			t.Errorf("%q: %q", args, got)
		}
	}
}

// TestDemoInfraClaude: each message plays the next turn (the last again
// after them), every command a run call on the socket, answered before the
// next step; other lines are no turn.
func TestDemoInfraClaude(t *testing.T) {
	demoPace = 0
	defer func() { demoPace = time.Millisecond }()
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var ran []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var call struct {
				Name string
				Args struct{ Command, Effect string } `json:"arguments"`
			}
			json.NewDecoder(c).Decode(&call)
			mu.Lock()
			ran = append(ran, call.Name+" "+call.Args.Effect+" "+call.Args.Command)
			mu.Unlock()
			json.NewEncoder(c).Encode(map[string]any{"text": "ok"})
			c.Close()
		}
	}()
	in := `{"type":"user","message":{"content":"why?"}}` + "\nnot json\n" + `{"type":"control_request"}` + "\n" +
		`{"type":"user","message":{"content":"yes"}}` + "\n" + `{"type":"user","message":{"content":"again"}}` + "\n"
	var out strings.Builder
	DemoInfra(sock, strings.NewReader(in), &out)
	want := []string{"run read systemctl status nginx", "run read systemctl status billing-api", "run read df -h /var", "run read du -sh /var/log/billing",
		"run write sudo logrotate -f /etc/logrotate.d/billing", "run write sudo systemctl restart billing-api", "run read systemctl status billing-api"}
	want = append(want, want[4:]...)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ran:\n%s", strings.Join(ran, "\n"))
	}
	var kinds []string
	results, tools := 0, 0
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var e struct{ Type, Subtype string }
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			t.Fatalf("not json: %s", sc.Text())
		}
		kinds = append(kinds, e.Type)
		results += strings.Count(sc.Text(), `"type":"result"`)
		tools += strings.Count(sc.Text(), `"name":"mcp__desk__run"`)
	}
	if kinds[0] != "system" || results != 3 || tools != len(want) || kinds[len(kinds)-1] != "result" {
		t.Fatalf("%d results, %d tools: %q", results, tools, kinds)
	}
	for _, w := range []string{"Found it.", `"input_tokens":30000`, `"total_cost_usd":0.19`, `"id":"ra1"`, `"tool_use_id":"rc3"`} { // not the output: its "no space left" reads as a full disk to cmd/qa
		if !strings.Contains(out.String(), w) {
			t.Errorf("no %q in the turns", w)
		}
	}
}

// TestDemoCallNoSocket: a socket that isn't there is no hang.
func TestDemoCallNoSocket(t *testing.T) {
	demoCall(filepath.Join(t.TempDir(), "none.sock"), "run", nil)
}

// TestInfraGen: without a workspace (tests) the count stays 0.
func TestInfraGen(t *testing.T) {
	if g := (&ui{}).infraGen(); g != 0 {
		t.Fatal(g)
	}
}

// TestStartInfra: the workspace starts over the orgs (here none) with the
// card tools' socket, and its redraw count is the frame's to compare.
func TestStartInfra(t *testing.T) {
	h := newHarness(t)
	for _, k := range []string{"XDG_RUNTIME_DIR", "XDG_STATE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, t.TempDir())
	}
	h.u.root = t.TempDir()
	h.u.startInfra()
	defer h.u.infra.Close()
	if h.u.infra == nil || h.u.cardSock == "" {
		t.Fatalf("workspace %v, card socket %q", h.u.infra, h.u.cardSock)
	}
	if h.u.infraGen() != h.u.infra.Gen() {
		t.Fatal("the workspace's redraw count")
	}
}
