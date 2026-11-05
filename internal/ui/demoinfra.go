package ui

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/lfdominguez/desk/internal/infra"
)

// demoServers is Globex's servers file in the demo: its hosts are bash with
// demoHostRC, never ssh.
const demoServers = `#@ group web
Host web-01
    HostName 192.0.2.11
    User deploy
    #@ desc public proxy and billing API
Host web-02
    HostName 192.0.2.12
    User deploy
    #@ desc public proxy and billing API
#@ group db
Host db-01
    HostName 192.0.2.21
    User postgres
    #@ desc billing's Postgres primary
Host db-02
    HostName 192.0.2.22
    User postgres
    #@ desc its replica
`

// demoHostRC makes a local bash look like a server: its prompt, and canned
// answers for the commands the infra Claude's script runs (web-02's /var is
// full until the logs are rotated).
const demoHostRC = `export HISTFILE=/dev/null
PS1='deploy@'"$DESK_DEMO_HOST"':~$ '
cd "$HOME"
clear() { printf '\033[H\033[2J\033[3J'; }
sudo() { "$@"; }
full() { [ "$DESK_DEMO_HOST" = web-02 ] && [ ! -e ~/.rotated ]; }
uptime() { echo " 14:32:07 up 41 days,  3:12,  1 user,  load average: 0.21, 0.18, 0.15"; }
logrotate() { touch ~/.rotated; }
df() {
	echo "Filesystem      Size  Used Avail Use% Mounted on"
	if full; then echo "/dev/sda3        40G   40G     0 100% /var"; else echo "/dev/sda3        40G  9.8G   28G  26% /var"; fi
}
du() { if full; then echo "37G	/var/log/billing"; else echo "1.2G	/var/log/billing"; fi; }
systemctl() {
	case "$1 $2" in
	"status nginx") printf '\033[32m●\033[0m nginx.service - A high performance web server\n     Loaded: loaded (/lib/systemd/system/nginx.service; enabled)\n     Active: \033[32mactive (running)\033[0m since Mon 2026-09-24 11:20:41 UTC; 41 days ago\n' ;;
	"status billing-api")
		if full; then printf '\033[31m×\033[0m billing-api.service - Globex billing API\n     Loaded: loaded (/etc/systemd/system/billing-api.service; enabled)\n     Active: \033[31mfailed\033[0m (Result: exit-code) since Wed 2026-11-04 14:25:02 UTC; 7min ago\n    Process: 81723 ExecStart=/opt/billing/api (code=exited, status=1/FAILURE)\n\nNov 04 14:25:02 web-02 billing-api[81723]: write /var/lib/billing/spool/0042: no space left on device\n'; return 3
		else printf '\033[32m●\033[0m billing-api.service - Globex billing API\n     Active: \033[32mactive (running)\033[0m since Wed 2026-11-04 14:33:10 UTC; 2s ago\n'; fi ;;
	"restart billing-api") ;;
	esac
}
`

// demoInfra makes the demo's infra workspace: Globex's servers, faked hosts
// and a scripted infra Claude (desk itself, as demo-infra).
func demoInfra(dir string) error {
	rc := filepath.Join(dir, "host.rc")
	if err := os.WriteFile(rc, []byte(demoHostRC), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "home", "Work", "Globex", ".desk", "ssh_config"), []byte(demoServers), 0o644); err != nil {
		return err
	}
	infra.HostCmd = func(host string) *exec.Cmd {
		home := filepath.Join(dir, "hosts", host) // each host its own state
		os.MkdirAll(home, 0o755)
		c := exec.Command("bash", "--rcfile", rc, "-i")
		c.Env = []string{"DESK_DEMO_HOST=" + host, "HOME=" + home}
		return c
	}
	exe, err := os.Executable()
	infra.ClaudeCmd = func(args ...string) *exec.Cmd { return exec.Command(exe, "demo-infra", demoSock(args)) }
	return err
}

// demoSock is the tab's tools socket in the --mcp-config claude gets.
func demoSock(args []string) string {
	i := slices.Index(args, "--mcp-config")
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	var cfg struct {
		MCPServers map[string]struct{ Args []string } `json:"mcpServers"`
	}
	json.Unmarshal([]byte(args[i+1]), &cfg)
	if a := cfg.MCPServers["desk"].Args; len(a) > 1 {
		return a[1]
	}
	return ""
}

// demoRun is one step of the infra Claude's script: words, or a command run
// in the shared terminal through the tab's tools socket.
type demoRun struct {
	ms     int
	text   string
	cmd    string
	effect string
	reason string
}

var demoInfraTurns = [][]demoRun{
	{
		{400, "I'll check what's behind the 502 on web-02: nginx first, then the API it proxies to.", "", "", ""},
		{500, "", "systemctl status nginx", "read", "Is the proxy itself up?"},
		{600, "", "systemctl status billing-api", "read", "nginx is up: is the API behind it?"},
		{600, "", "df -h /var", "read", "The API failed writing its spool: how full is /var?"},
		{500, "", "du -sh /var/log/billing", "read", "What is filling /var?"},
		{600, "**Found it.** `/var` on web-02 is **100% full**, so `billing-api` crashed (*no space left on device*) and nginx answers 502.\n\n" +
			"`/var/log/billing` holds 37 GB: its logs haven't been rotated since Oct 30.\n\nShall I rotate them and restart `billing-api`?", "", "", ""},
	},
	{
		{400, "Rotating the logs, then restarting the API.", "", "", ""},
		{500, "", "sudo logrotate -f /etc/logrotate.d/billing", "write", "Compress and drop the old billing logs"},
		{600, "", "sudo systemctl restart billing-api", "write", "Start the API again now that /var has room"},
		{500, "", "systemctl status billing-api", "read", "Did it come back?"},
		{500, "Done: `/var` is down to 26% and `billing-api` is running again, so the 502 is gone. I'd add a size cap to its logrotate config so it can't fill up again.", "", "", ""},
	},
}

// DemoInfra is `desk demo-infra <sock>`, the demo's infra Claude: it speaks
// claude's stream-json on in and out, and each message the user sends plays
// the next turn of demoInfraTurns (the last one again after that), running
// its commands in the tab's terminal for real.
func DemoInfra(sock string, in io.Reader, out io.Writer) {
	emit := func(line string) { io.WriteString(out, line+"\n") }
	emit(`{"type":"system","subtype":"init","session_id":"demo-infra"}`)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	turn := 0
	for sc.Scan() {
		var m struct{ Type string }
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "user" {
			continue
		}
		for i, st := range demoInfraTurns[min(turn, len(demoInfraTurns)-1)] {
			time.Sleep(time.Duration(st.ms) * demoPace)
			if st.text != "" {
				emit(demoText(st.text))
				continue
			}
			id := "r" + string(rune('a'+turn)) + string(rune('0'+i))
			args := map[string]any{"command": st.cmd, "effect": st.effect, "reason": st.reason, "timeout_seconds": 20}
			emit(demoTool(id, "mcp__desk__run", args, ""))
			demoCall(sock, "run", args)
			emit(demoDone(id))
		}
		emit(demoUsage(18000 + 6000*turn))
		emit(demoResult(0.09 + 0.05*float64(turn)))
		turn++
	}
}

// demoCall is a tool call on the tab's socket, as desk mcp makes them.
// It waits for the answer: the command has finished then.
func demoCall(sock, name string, args map[string]any) {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return
	}
	defer c.Close()
	a, _ := json.Marshal(args)
	json.NewEncoder(c).Encode(map[string]any{"name": name, "arguments": json.RawMessage(a)})
	io.Copy(io.Discard, c)
}
