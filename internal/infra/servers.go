package infra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	deskorgs "github.com/lfdominguez/desk/internal/orgs"
)

// An organization's servers can be one OpenSSH config: <Org>/ssh_config, or
// the file `[servers] file = "…"` in its desk.toml names (relative to the org
// folder). desk runs `ssh -F <file> <alias>`, and so can any terminal. desk's
// own data sits in comment lines OpenSSH ignores, "#@ key value" (indented
// or not; values may be "quoted"):
//
//	#@ vault acme
//	#@ password "{user}@{host}"
//	#@ group eu/web
//	#@ tags web, nginx
//	Host web1
//	    HostName 10.0.0.1
//	    User deploy
//	    #@ password "web1 root" password
//	    #@ wol 52:54:00:12:34:56 10.0.0.255:9
//	    #@ desc the public proxy
//	    #@ nagios web1.example.com
//	    #@ pin
//	Host bastion
//	    #@ hidden
//
// Lines before any #@ group or Host are the file's defaults; #@ group starts
// a section ("/" nests) to the next #@ group, whose lines before a Host are
// the group's (eu/web inherits eu's); a Host block's, from its Host line to
// the next Host, Match or #@ group, are the host's. Host beats group beats
// parent group beats file. password ITEM [FIELD] fills {host} (the alias),
// {user} (its User) and {group} (its group path) in ITEM. password-raw
// PASSWORD is the password itself, in the file, for whoever chooses that
// over a vault (keep the file 0600); the nearest level with either wins. A
// value is the rest of the line: comments go on their own lines.
//
// Include lines are followed when they resolve inside the org folder, so a
// group may continue in an included file (one without #@ group stays in the
// including line's group). Paths resolve as ssh resolves them for a -F file:
// "~/" is home and a relative path is in ~/.ssh, so write absolute or "~/"
// paths. Includes outside the org folder (Include ~/.ssh/config, your own
// defaults) are ssh's alone: desk never lists your personal hosts as the
// org's servers.
//
// "#@ hosts-cmd PATH" (relative to the org folder) runs PATH on load and on
// "reload config", 10 s at most, and keeps what it prints, ssh_config text or
// hosts lines ("name destination"), in <Org>/.desk/servers-dynamic.conf (a
// group's: servers-dynamic-<group>.conf). Include that file on the next line;
// a failed run keeps the last output.
const serversFile = "ssh_config"

// topGroup is the tree's name for the hosts outside any #@ group.
const topGroup = "servers"

// serverHost is one concrete Host alias of an org's servers file.
type serverHost struct {
	alias, addr, user, group string
	file                     string     // the file holding its Host block (applyFixes edits it)
	password                 *SecretRef // nil: none
	wol, desc, nagios, tags  string
	pin, hidden              bool
}

// serversPath is org folder's servers file, "" when it has none (it then
// keeps the old Infra-Claude/<group>/ folders).
func serversPath(org string) string {
	if p := serversTarget(org); fileExists(p) {
		return p
	}
	return ""
}

// ServersFile is where the organization folder org's servers file is, or
// goes when an import makes it.
func ServersFile(org string) string { return serversTarget(org) }

// serversTarget is where org's servers file is, or goes (an import makes it).
func serversTarget(org string) string {
	c, _ := deskorgs.Load(filepath.Dir(org), filepath.Base(org)) // a broken desk.toml: the default name
	p := serversFile
	if c.Servers.File != "" {
		p = expandHome(c.Servers.File)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(org, p)
	}
	return p
}

// serverHosts is the hosts of the servers file a pane or tab dir runs with:
// ok only for an org's Infra-Claude dir whose org has one.
func serverHosts(dir string) ([]serverHost, bool) {
	if filepath.Base(dir) != infraDir {
		return nil, false
	}
	f := serversPath(filepath.Dir(dir))
	if f == "" {
		return nil, false
	}
	hs, _ := parseServers(f, filepath.Dir(dir), false)
	return hs, true
}

type serverParse struct {
	org     string // the org folder: Includes outside it aren't followed
	run     bool   // run hosts-cmd lines
	grouped bool   // a #@ group was seen: section keys are a group's
	file    map[string]string
	groups  map[string]map[string]string
	own     []map[string]string // per host, its block's #@ keys
	hosts   []serverHost
	seen    map[string]bool
	errs    []error
}

// parseServers reads an org's servers file and the Includes inside the org
// folder. With run, hosts-cmd lines run first. The error is what went wrong
// (a missing include, a failed hosts-cmd); the hosts are still good.
func parseServers(file, org string, run bool) ([]serverHost, error) {
	p := &serverParse{org: org, run: run, file: map[string]string{}, groups: map[string]map[string]string{}, seen: map[string]bool{}}
	p.read(file, "", 0)
	for i := range p.hosts {
		p.resolve(i)
	}
	return p.hosts, errors.Join(p.errs...)
}

func (p *serverParse) section(group string) map[string]string {
	if !p.grouped {
		return p.file
	}
	if p.groups[group] == nil {
		p.groups[group] = map[string]string{}
	}
	return p.groups[group]
}

func (p *serverParse) read(path, group string, depth int) {
	if depth > 8 {
		p.errs = append(p.errs, fmt.Errorf("%s: includes nest too deep", path))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		p.errs = append(p.errs, err)
		return
	}
	var cur []int // the current Host block's hosts; nil outside one
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if rest, ok := strings.CutPrefix(line, "#@"); ok {
			key, val := cutWord(strings.TrimSpace(rest))
			switch key {
			case "":
			case "group":
				group, cur, p.grouped = strings.Trim(unquote(val), "/"), nil, true
			case "hosts-cmd":
				if p.run {
					p.hostsCmd(unquote(val), group)
				}
			default:
				if cur != nil {
					for _, i := range cur {
						p.own[i][key] = val
					}
				} else {
					p.section(group)[key] = val
				}
			}
			continue
		}
		kw, rest := sshKeyword(line)
		args := splitQuoted(rest)
		if kw == "" || len(args) == 0 {
			continue
		}
		switch kw {
		case "host":
			cur = []int{}
			for _, a := range args {
				if strings.ContainsAny(a, "*?!") || p.seen[a] {
					continue // ponytail: a repeated alias keeps its first block's desk keys
				}
				p.seen[a] = true
				cur = append(cur, len(p.hosts))
				p.hosts = append(p.hosts, serverHost{alias: a, group: group, file: path})
				p.own = append(p.own, map[string]string{})
			}
		case "match":
			cur = nil
		case "hostname", "user":
			for _, i := range cur {
				h := &p.hosts[i]
				if kw == "hostname" && h.addr == "" {
					h.addr = args[0]
				} else if kw == "user" && h.user == "" {
					h.user = args[0]
				}
			}
		case "include":
			for _, a := range args {
				for _, f := range p.includes(a) {
					p.read(f, group, depth+1)
				}
			}
		}
	}
}

// includes is what an Include argument names inside the org folder, sorted
// as ssh's glob does.
func (p *serverParse) includes(arg string) []string {
	arg = expandHome(arg)
	if !filepath.IsAbs(arg) {
		home, _ := os.UserHomeDir()
		arg = filepath.Join(home, ".ssh", arg)
	}
	ms, _ := filepath.Glob(arg)
	var out []string
	for _, m := range ms {
		if within(p.org, m) {
			out = append(out, m)
		}
	}
	return out
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolve fills host i's desk keys: its own, its group's and the parent
// groups', then the file's.
func (p *serverParse) resolve(i int) {
	h := &p.hosts[i]
	chain := []map[string]string{p.own[i]}
	for g := h.group; ; {
		chain = append(chain, p.groups[g])
		j := strings.LastIndex(g, "/")
		if j < 0 {
			break
		}
		g = g[:j]
	}
	chain = append(chain, p.file)
	get := func(k string) (string, bool) {
		for _, m := range chain {
			if v, ok := m[k]; ok {
				return v, true
			}
		}
		return "", false
	}
	for _, m := range chain { // the nearest level with a password says which kind
		if raw := unquote(m["password-raw"]); raw != "" {
			h.password = &SecretRef{Raw: raw}
			break
		}
		if v := m["password"]; v != "" {
			fs := splitQuoted(v)
			vault, _ := get("vault")
			item := strings.NewReplacer("{host}", h.alias, "{user}", h.user, "{group}", h.group).Replace(fs[0])
			ref := &SecretRef{Vault: unquote(vault), Item: item}
			if len(fs) > 1 {
				ref.Field = fs[1]
			}
			h.password = ref
			break
		}
	}
	str := func(k string) string { v, _ := get(k); return unquote(v) }
	flag := func(k string) bool { v, ok := get(k); return ok && v != "false" && v != "no" }
	h.wol, h.desc, h.nagios, h.pin, h.hidden = str("wol"), str("desc"), str("nagios"), flag("pin"), flag("hidden")
	var tags []string
	for _, t := range strings.Split(str("tags"), ",") {
		if t = strings.TrimSpace(unquote(strings.TrimSpace(t))); t != "" {
			tags = append(tags, t)
		}
	}
	h.tags = strings.Join(tags, ", ")
}

// dynamicFile is where a hosts-cmd's output is kept.
func dynamicFile(org, group string) string {
	name := "servers-dynamic.conf"
	if group != "" {
		name = "servers-dynamic-" + strings.ReplaceAll(group, "/", "-") + ".conf"
	}
	return filepath.Join(org, ".desk", name)
}

// hostsCmd runs a hosts-cmd and keeps its output as ssh_config text.
func (p *serverParse) hostsCmd(path, group string) {
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.org, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = filepath.Dir(path)
	out, err := cmd.Output()
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("hosts-cmd %s: %w", filepath.Base(path), err))
		return
	}
	if !isSSHConfig(string(out)) {
		out = []byte(hostsToConfig(string(out)))
	}
	f := dynamicFile(p.org, group)
	os.MkdirAll(filepath.Dir(f), 0o700)
	if os.WriteFile(f+".tmp", out, 0o600) == nil {
		os.Rename(f+".tmp", f)
	}
}

// isSSHConfig tells ssh_config text from hosts lines: it has a Host or Match.
func isSSHConfig(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if kw, _ := sshKeyword(strings.TrimSpace(l)); kw == "host" || kw == "match" {
			return true
		}
	}
	return false
}

// hostsToConfig turns hosts lines ("name destination" or "destination", "#"
// comments) into Host blocks.
func hostsToConfig(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		l, _, _ = strings.Cut(l, "#")
		if f := strings.Fields(l); len(f) > 0 {
			b.WriteString(hostBlock(f[0], f[min(1, len(f)-1)]))
		}
	}
	return b.String()
}

// hostBlock is the Host block for a hosts line: destination user@host:port,
// ssh://user@host:port or a bare host (one that is also the name needs no
// HostName, so an alias of ~/.ssh/config still resolves there).
func hostBlock(name, dest string) string {
	user, host, port := splitDest(dest)
	s := "Host " + name + "\n"
	if host != name {
		s += "    HostName " + host + "\n"
	}
	if user != "" {
		s += "    User " + user + "\n"
	}
	if port != "" {
		s += "    Port " + port + "\n"
	}
	return s
}

func splitDest(d string) (user, host, port string) {
	d = strings.TrimPrefix(d, "ssh://")
	if i := strings.LastIndex(d, "@"); i >= 0 {
		user, d = d[:i], d[i+1:]
	}
	if strings.HasPrefix(d, "[") {
		if i := strings.Index(d, "]"); i > 0 {
			return user, d[1:i], strings.TrimPrefix(d[i+1:], ":")
		}
	}
	if h, p, ok := strings.Cut(d, ":"); ok && !strings.Contains(p, ":") {
		return user, h, p
	}
	return user, d, ""
}

// sshKeyword splits an ssh_config line into its keyword (lower case) and the
// rest; "Key=value" and "Key = value" work as ssh reads them.
func sshKeyword(line string) (kw, rest string) {
	if line == "" || line[0] == '#' {
		return "", ""
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return strings.ToLower(line), ""
	}
	rest = strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	return strings.ToLower(line[:i]), rest
}

// cutWord splits off s's first word.
func cutWord(s string) (string, string) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

// splitQuoted splits s at spaces, keeping "quoted parts" whole.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted, has = !quoted, true
		case (r == ' ' || r == '\t') && !quoted:
			if has {
				out = append(out, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

// serverTree is the sidebar rows of an org with a servers file: groups in
// the order they first appear, hidden hosts left out.
func serverTree(org, infra, file string) []node {
	hs, err := parseServers(file, filepath.Dir(infra), true)
	out := []node{{depth: 0, label: org, org: org, dir: infra}}
	if err != nil { // shown in the sidebar: a GUI can't print
		out = append(out, node{depth: 1, label: "(" + filepath.Base(file) + ": " + firstLine(err.Error(), 50) + ")"})
	}
	var order []string
	by := map[string][]serverHost{}
	for _, h := range hs {
		g := h.group
		if g == "" {
			g = topGroup
		}
		if h.hidden {
			continue
		}
		if by[g] == nil {
			order = append(order, g)
		}
		by[g] = append(by[g], h)
	}
	for _, g := range order {
		out = append(out, node{depth: 1, label: g})
		for _, h := range by[g] {
			out = append(out, node{depth: 2, label: h.alias, org: org, group: g, dir: infra, host: h.alias, addr: h.addr,
				wol: h.wol, nagios: h.nagios, desc: h.desc, tags: h.tags, pin: h.pin})
		}
	}
	return out
}
