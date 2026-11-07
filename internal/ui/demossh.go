package ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// The demo's servers over SSH: panes are HostCmd's bash, but the files split
// and the changes tool speak SSH, so each host also gets a listener on
// 127.0.0.1 in desk's own process (not sshtest, which needs package testing):
// sftp over an in-memory tree of demoHostFiles, and demoChanges for the
// changes tool's script. Every login is let in.

// demoSSH starts a server per host and returns the Match blocks that send
// each host to its own, for the top of the servers file (first value wins);
// home's known_hosts gets their key, as globex-demo.
func demoSSH(home string, hosts []string) (string, error) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader) // neither fails for ed25519
	signer, _ := ssh.NewSignerFromKey(priv)
	conf := &ssh.ServerConfig{NoClientAuth: true}
	conf.AddHostKey(signer)
	b := strings.Builder{}
	b.WriteString("# The demo's servers: desk itself, on this machine.\n")
	for _, h := range hosts {
		fs := demoFS(h)
		// ponytail: the listeners live as long as the process; a demo world is
		// made once per run (tests make a few).
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go demoServe(c, conf, h, fs)
			}
		}()
		fmt.Fprintf(&b, "Match originalhost %s\n    HostName 127.0.0.1\n    Port %d\n    HostKeyAlias globex-demo\n    IdentityAgent none\n",
			h, ln.Addr().(*net.TCPAddr).Port)
	}
	kh := filepath.Join(home, ".ssh", "known_hosts")
	os.MkdirAll(filepath.Dir(kh), 0o700)
	return b.String(), os.WriteFile(kh, append([]byte("globex-demo "), ssh.MarshalAuthorizedKey(signer.PublicKey())...), 0o600)
}

// demoFS is host's in-memory tree, filled through an sftp client of its
// own (in memory, so a failure only leaves a file out).
func demoFS(host string) sftp.Handlers {
	h := sftp.InMemHandler()
	a, b := net.Pipe()
	go sftp.NewRequestServer(b, h).Serve()
	c, err := sftp.NewClientPipe(a, a)
	if err != nil {
		return h
	}
	defer c.Close()
	for p, text := range demoHostFiles(host) {
		c.MkdirAll(path.Dir(p))
		if f, err := c.Create(p); err == nil {
			f.Write([]byte(text))
			f.Close()
		}
	}
	return h
}

func demoServe(c net.Conn, conf *ssh.ServerConfig, host string, fs sftp.Handlers) {
	_, chans, reqs, err := ssh.NewServerConn(c, conf)
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.Prohibited, "not on the demo's servers")
			continue
		}
		if ch, creqs, err := nc.Accept(); err == nil {
			go demoChannel(ch, creqs, host, fs)
		}
	}
}

// demoChannel is a session: sftp, or a command (the changes tool's gets
// demoChanges, any other exits 127).
func demoChannel(ch ssh.Channel, reqs <-chan *ssh.Request, host string, fs sftp.Handlers) {
	defer ch.Close()
	for r := range reqs {
		var p struct{ S string } // subsystem's name, exec's command
		ssh.Unmarshal(r.Payload, &p)
		switch {
		case r.Type == "subsystem" && p.S == "sftp":
			r.Reply(true, nil)
			sftp.NewRequestServer(ch, fs, sftp.WithStartDirectory(demoHome(host))).Serve()
			return
		case r.Type == "exec":
			r.Reply(true, nil)
			code := 0
			if strings.Contains(p.S, "== packages") {
				io.WriteString(ch, demoChanges(host))
			} else {
				io.WriteString(ch.Stderr(), "sh: not on the demo's servers\n")
				code = 127
			}
			ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{uint32(code)}))
			return
		default:
			r.Reply(false, nil)
		}
	}
}

// demoHome is where host's files split opens: its user's home.
func demoHome(host string) string {
	if strings.HasPrefix(host, "db-") {
		return "/var/lib/postgresql"
	}
	return "/home/deploy"
}

// demoHostFiles is what host's files split shows.
func demoHostFiles(host string) map[string]string {
	if strings.HasPrefix(host, "db-") {
		return map[string]string{
			"/var/lib/postgresql/backup.sh":              "#!/bin/sh\npg_dump -Fc billing > /var/backups/billing-$(date +%F).dump\n",
			"/var/lib/postgresql/16/main/PG_VERSION":     "16\n",
			"/etc/postgresql/16/main/postgresql.conf":    "listen_addresses = '*'\nmax_connections = 200\nshared_buffers = 4GB\n",
			"/etc/postgresql/16/main/pg_hba.conf":        "host  billing  billing  192.0.2.0/24  scram-sha-256\n",
			"/var/log/postgresql/postgresql-16-main.log": "2026-11-04 14:25:03 UTC LOG:  could not receive data from client: Connection reset by peer\n",
			"/var/backups/billing-2026-11-04.dump":       "PGDMP\n",
		}
	}
	return map[string]string{
		"/home/deploy/deploy.sh":                "#!/bin/sh\n# deploy.sh VERSION: unpack a release and switch to it\nset -e\ntar -C releases -xzf billing-api-$1.tar.gz\nln -sfn releases/$1 current\nsudo systemctl restart billing-api\n",
		"/home/deploy/releases/4.12.0/VERSION":  "4.12.0\n",
		"/home/deploy/releases/4.12.1/VERSION":  "4.12.1\n",
		"/home/deploy/NOTES.md":                 "# " + host + "\n\nnginx in front of billing-api on :8080. Logs in /var/log/billing, rotated daily.\n",
		"/etc/nginx/sites-enabled/billing.conf": "server {\n    listen 443 ssl;\n    server_name billing.example.com;\n    location / { proxy_pass http://127.0.0.1:8080; }\n}\n",
		"/etc/logrotate.d/billing":              "/var/log/billing/*.log {\n    daily\n    rotate 7\n    compress\n}\n",
		"/var/log/billing/api.log":              "2026-11-04T14:24:58Z INFO  export batch 0041 written\n2026-11-04T14:25:02Z ERROR spool write failed\n",
		"/var/log/nginx/error.log":              "2026/11/04 14:25:04 [error] 812#812: *5521 connect() failed (111: Connection refused) while connecting to upstream\n",
	}
}

// demoChanges is what the changes tool's script prints on host: web-02's
// API went down at 14:25 after Tuesday's upgrade.
func demoChanges(host string) string {
	if host == "web-02" {
		return `== packages
2026-11-03 09:12:44 upgrade billing-api:amd64 4.12.0 4.12.1
== files changed under /etc
/etc/billing/api.env
== services started, stopped, failed
2026-11-03T09:13:02+0000  Started billing-api.service - Globex billing API.
2026-11-04T14:25:02+0000  billing-api.service: Failed with result 'exit-code'.
== logins
deploy   pts/0        198.51.100.7     Wed Nov  4 14:29:51 2026   still logged in
== reboots
== containers
`
	}
	return `== packages
2026-11-03 06:25:11 upgrade tzdata:all 2026a-1 2026b-1
== files changed under /etc
== services started, stopped, failed
2026-11-04T06:25:00+0000  Started apt-daily-upgrade.service - Daily apt upgrade and clean activities.
== logins
== reboots
== containers
`
}
