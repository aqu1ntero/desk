package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// orgWith is a fresh org's infra dir, its desk.toml holding toml.
func orgWith(t *testing.T, toml string) string {
	t.Helper()
	infra := filepath.Join(t.TempDir(), "Acme", infraDir)
	write(t, filepath.Join(orgOfInfra(infra), ".desk", "desk.toml"), toml)
	return infra
}

// TestServersFileMoves: an org's old ssh_config moves to .desk/ssh_config
// as it is read (and is the one ssh runs with); with both, the new one wins
// and the old one stays.
func TestServersFileMoves(t *testing.T) {
	org := filepath.Join(t.TempDir(), "Acme")
	write(t, filepath.Join(org, "ssh_config"), "Host web1\n")
	if p := serversPath(org); p != filepath.Join(org, ".desk", "ssh_config") || fileExists(filepath.Join(org, "ssh_config")) {
		t.Fatalf("servers file %s", p)
	}
	if hs := serverHosts(makeInfra(org)); len(hs) != 1 || hs[0].alias != "web1" {
		t.Fatalf("hosts %v", hs)
	}
	write(t, filepath.Join(org, "ssh_config"), "Host old\n")
	if sshConfigFor(filepath.Join(org, infraDir)) != filepath.Join(org, ".desk", "ssh_config") {
		t.Error("ssh runs with the old file")
	}
	if b, _ := os.ReadFile(filepath.Join(org, "ssh_config")); string(b) != "Host old\n" {
		t.Error("the old file changed")
	}
}

// TestServersRelativeAfterMove: an old desk.toml's relative [servers] file
// still means the org folder's, once desk.toml is in .desk; no ssh_config
// moves then.
func TestServersRelativeAfterMove(t *testing.T) {
	org := filepath.Join(t.TempDir(), "Acme")
	write(t, filepath.Join(org, "desk.toml"), "[servers]\nfile = \"hosts/servers.conf\"\n")
	write(t, filepath.Join(org, "ssh_config"), "Host personal\n")
	write(t, filepath.Join(org, "hosts", "servers.conf"), "Host web1\n")
	if p := ServersFile(org); p != filepath.Join(org, "hosts", "servers.conf") {
		t.Fatalf("servers file %s", p)
	}
	if !fileExists(filepath.Join(org, ".desk", "desk.toml")) || !fileExists(filepath.Join(org, "ssh_config")) {
		t.Error("desk.toml stays, or an ssh_config desk doesn't use moved")
	}
}

func hostByAlias(hs []serverHost, a string) *serverHost {
	for i := range hs {
		if hs[i].alias == a {
			return &hs[i]
		}
	}
	return nil
}

func TestServersFileScoping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "Work", "Acme")
	write(t, filepath.Join(home, ".ssh", "config"), "Host personal\n    HostName 192.0.2.99\n")
	write(t, filepath.Join(home, ".ssh", "rel.conf"), "Host relhost\n")
	write(t, filepath.Join(org, "more", "db.conf"), `
Host db1
    HostName db1.example.com
#@ group lab
Host lab1
`)
	write(t, filepath.Join(org, ".desk", "ssh_config"), `#@ vault acme
#@ password "{user}@{host}"
#@ tags all
Host bastion
    HostName 203.0.113.1
    #@ hidden

#@ group eu
#@ desc "Region A"
#@ group eu/web
#@ password "web {host}" notes
Host web1 web2
    HostName = 10.0.0.1
    User deploy
    #@ pin
    #@ wol 52:54:00:12:34:56 10.0.0.255:9
    #@ nagios web1.example.com
Host web3
    User root
    #@ password "web3 root"
    #@ vault other
Host web4
    #@ password-raw "s3 cret"
Host *.internal
    #@ pin
#@ group eu
Include ~/Work/Acme/more/*.conf
Include rel.conf
Match all
    Include ~/.ssh/config
`)
	hs, err := parseServers(filepath.Join(org, ".desk", "ssh_config"), org, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.group+":"+h.alias)
	}
	if s := strings.Join(names, " "); s != ":bastion eu/web:web1 eu/web:web2 eu/web:web3 eu/web:web4 eu:db1 lab:lab1" {
		t.Fatalf("hosts: %s", s) // personal (~/.ssh/config) and relhost (a relative Include is in ~/.ssh) are outside the org
	}
	b, w1, w2, w3, db, lab := hostByAlias(hs, "bastion"), hostByAlias(hs, "web1"), hostByAlias(hs, "web2"), hostByAlias(hs, "web3"), hostByAlias(hs, "db1"), hostByAlias(hs, "lab1")
	if !b.hidden || b.password == nil || b.password.Item != "@bastion" || b.tags != "all" {
		t.Errorf("bastion: %+v %+v", b, b.password)
	}
	if w1.addr != "10.0.0.1" || w1.user != "deploy" || !w1.pin || !w2.pin || w1.wol != "52:54:00:12:34:56 10.0.0.255:9" || w1.nagios != "web1.example.com" {
		t.Errorf("web1: %+v", w1)
	}
	if *w1.password != (SecretRef{Vault: "acme", Item: "web web1", Field: "notes"}) || w2.password.Item != "web web2" || w1.desc != "Region A" || w1.tags != "all" {
		t.Errorf("web1 password/desc: %+v %+v", w1.password, w1)
	}
	if *w3.password != (SecretRef{Vault: "other", Item: "web3 root"}) || w3.pin {
		t.Errorf("web3: %+v", w3.password)
	}
	// its own password, in the file, beats its group's vault item
	if pw, err := serverPassword(org, hostByAlias(hs, "web4").password); pw != "s3 cret" || err != nil {
		t.Errorf("web4: %q %v", pw, err)
	}
	if db.file != filepath.Join(org, "more", "db.conf") || db.desc != "Region A" || db.password.Item != "@db1" || lab.group != "lab" || lab.desc != "" {
		t.Errorf("included: %+v / %+v", db, lab)
	}
}

func TestServersTreeAndSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "Work")
	org := filepath.Join(root, "Acme")
	write(t, filepath.Join(root, "Globex", oldInfraDir, "prod", "hosts"), "db1\n") // the old layout: no servers
	write(t, filepath.Join(org, "servers.conf"), `#@ vault pb
#@ password "{host} / {user}"
Host gw
    User admin
#@ group eu/web
Host web1
    User deploy
#@ group eu/db
Host db1
    #@ hidden
Host db2
    #@ pin
    #@ desc primary
#@ group lab
#@ hosts-cmd bin/inventory.sh
Include ~/Work/Acme/.desk/servers-dynamic-lab.conf
`)
	write(t, filepath.Join(org, ".desk", "desk.toml"), "[servers]\nfile = \"servers.conf\"\n")
	write(t, filepath.Join(org, "bin", "inventory.sh"), "#!/bin/sh\necho 'pi deploy@192.0.2.7:2222'\n")
	os.Chmod(filepath.Join(org, "bin", "inventory.sh"), 0o755)
	var got []string
	tree := loadTree(root)
	infra := filepath.Join(org, infraDir)
	for _, n := range tree {
		got = append(got, strings.Repeat(">", n.depth)+n.label)
		if n.host != "" && n.org == "Acme" && n.dir != infra {
			t.Errorf("dir of %s: %s", n.host, n.dir)
		}
	}
	if s := strings.Join(got, " "); s != "Acme >servers >>gw >eu/web >>web1 >eu/db >>db2 >lab >>pi" {
		t.Fatalf("tree: %s", s)
	}
	if !fileExists(infra) || fileExists(filepath.Join(root, "Globex", infraDir)) {
		t.Error("the infra dir is made for orgs with a servers file only")
	}
	if tree[2].group != "servers" || !tree[6].pin || tree[6].desc != "primary" {
		t.Errorf("nodes: %+v %+v", tree[2], tree[6])
	}
	if o := sshOpts("web1", infra); o.File != filepath.Join(org, "servers.conf") {
		t.Errorf("config %q", o.File)
	}
	if r := passwordRef(infra, "web1"); r == nil || *r != (SecretRef{Vault: "pb", Item: "web1 / deploy"}) {
		t.Errorf("template: %+v", r)
	}
	if r := passwordRef(infra, "pi"); r == nil || r.Item != "pi / deploy" {
		t.Errorf("dynamic host: %+v", r)
	}
	orgsMu.Lock()
	orgs[infra] = &orgConfig{Vaults: []VaultCfg{{Name: "pb", Type: "passbolt"}},
		vaults: map[string]vault{"pb": fakeVault{"id1": {ID: "id1", Name: "web1 / deploy", Password: "s3cret"}}}}
	orgsMu.Unlock()
	if pw, err := serverPassword(infra, passwordRef(infra, "web1")); err != nil || pw != "s3cret" {
		t.Errorf("password: %q %v", pw, err)
	}
	m := model{tree: tree}
	tab := m.newTab("Acme", "eu/web", infra, "web1")
	if tab.orgCfg != orgs[infra] || runbookDir(tab) != filepath.Join(infra, "runbooks") {
		t.Errorf("tab config / runbooks: %s", runbookDir(tab))
	}
	m.group = m.groupDir(3) // eu/web's group view has its tab, not the org's others
	m.newTab("Acme", "lab", infra, "pi")
	if ts := m.groupTabs(); len(ts) != 1 || ts[0] != tab {
		t.Errorf("group view: %d tabs", len(ts))
	}
}

func TestApplyFixesServersFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "Work", "Acme")
	infra := filepath.Join(org, infraDir)
	os.MkdirAll(infra, 0o755)
	write(t, filepath.Join(org, "inc.conf"), "Host old2\n    HostName 192.0.2.2\n")
	write(t, filepath.Join(org, ".desk", "ssh_config"), "#@ group a\nHost old1\n    HostName 192.0.2.1\n#@ group b\n#@ vault x\nHost new1\nInclude ~/Work/Acme/inc.conf\n")
	fixes := []legacyFix{{"HostKeyAlgorithms", []string{"ssh-rsa"}}}
	for _, h := range []string{"old1", "old2"} {
		if err := applyFixes(hostFile(infra, h), h, fixes); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(org, ".desk", "ssh_config"))
	if !strings.Contains(string(b), "Host old1\n    # old server") || strings.Count(string(b), "HostKeyAlgorithms") != 1 {
		t.Errorf("main file:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(org, "inc.conf")); !strings.Contains(string(b), "Host old2\n    # old server") {
		t.Errorf("included file:\n%s", b)
	}
	applyFixes(hostFile(infra, "old1"), "old1", []legacyFix{{"HostKeyAlgorithms", []string{"ssh-dss"}}})
	if b, _ := os.ReadFile(filepath.Join(org, ".desk", "ssh_config")); !strings.Contains(string(b), "HostKeyAlgorithms +ssh-rsa,ssh-dss\n    HostName 192.0.2.1\n#@ group b") {
		t.Errorf("merge must stay inside old1's block:\n%s", b)
	}
}
