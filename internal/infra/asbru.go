package infra

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Ásbrú Connection Manager (asbru-cm) keeps its connections as a tree of
// groups and hosts keyed by UUID: its asbru.yml (written on a clean exit next
// to the binary store it really uses) under "environments", or a tree export
// at the top level under __PAC__EXPORTED__. Importing turns its SSH hosts
// into a servers file: groups become #@ group paths, jump hosts hidden Host
// blocks behind ProxyJump, the ssh options it keeps as a command line become
// config lines. Passwords stay behind (an ssh_config can't hold them; desk's
// come from #@ password), and so do hosts written with Ásbrú's variables.

// AsbruPath is Ásbrú's config: $ASBRU_CFG/asbru.yml, else ~/.config/asbru.
func AsbruPath() string {
	dir := os.Getenv("ASBRU_CFG")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "asbru")
	}
	return filepath.Join(dir, "asbru.yml")
}

type asbruNode struct {
	name, parent string
	group        bool
	v            map[string]any
}

type asbruTree struct {
	nodes    map[string]*asbruNode
	defaults map[string]any
}

// asbruRoots are the parents of top-level nodes: the tree's root, an export's.
var asbruRoots = map[string]bool{"__PAC__ROOT__": true, "__PAC__EXPORTED__": true, "": true}

func readAsbru(data []byte) (*asbruTree, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not Ásbrú's YAML: %w", err)
	}
	t := &asbruTree{nodes: map[string]*asbruNode{}}
	t.defaults, _ = doc["defaults"].(map[string]any)
	envs, ok := doc["environments"].(map[string]any)
	if !ok {
		if _, export := doc["__PAC__EXPORTED__"]; !export {
			return nil, fmt.Errorf("no Ásbrú connections in it (neither environments nor an export)")
		}
		envs = doc
	}
	for id, raw := range envs {
		v, ok := raw.(map[string]any)
		name := strings.TrimSpace(strings.TrimSuffix(asbruStr(v["name"]), " - copy")) // an export renames its copies
		if !ok || name == "" || strings.HasPrefix(id, "__PAC") || strings.HasPrefix(id, "HASH") || strings.HasPrefix(id, "_tmp_") || strings.HasPrefix(id, "pacshell_PID") {
			continue
		}
		t.nodes[id] = &asbruNode{name: name, parent: asbruStr(v["parent"]), group: asbruStr(v["_is_group"]) == "1", v: v}
	}
	return t, nil
}

// asbruStr is a YAML scalar as text: Perl's YAML writes numbers and 0/1 flags
// bare, ~ for none.
func asbruStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return map[bool]string{true: "1", false: "0"}[x]
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// path is the group path of node id ("Prod/Web"), from its parents; a cycle
// or a missing parent ends it there.
func (t *asbruTree) path(id string) string {
	var parts []string
	seen := map[string]bool{}
	for p := t.nodes[id].parent; !asbruRoots[p] && !seen[p]; {
		n := t.nodes[p]
		if n == nil {
			break
		}
		seen[p] = true
		parts = append(parts, strings.ReplaceAll(n.name, "/", "-"))
		p = n.parent
	}
	slices.Reverse(parts)
	return strings.Join(parts, "/")
}

// AsbruGroups is the top-level groups of Ásbrú's file data, sorted: what an
// organization can import alone.
func AsbruGroups(data []byte) ([]string, error) {
	t, err := readAsbru(data)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range t.nodes {
		if n.group && asbruRoots[n.parent] {
			out = append(out, strings.ReplaceAll(n.name, "/", "-"))
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// asbruHost is one host's config lines, in order; set replaces a keyword
// already there (the ssh options Ásbrú keeps win over its fields).
type asbruHost struct {
	alias, group string
	lines        [][2]string
	notes        []string
	old          bool // a jump host the servers file has: not written again
}

func (h *asbruHost) set(k, v string) {
	for i := range h.lines {
		if strings.EqualFold(h.lines[i][0], k) {
			h.lines[i][1] = v
			return
		}
	}
	h.lines = append(h.lines, [2]string{k, v})
}

// AsbruConfig turns Ásbrú's file data into servers file text: only the hosts
// under group (a path like "Prod" or "Prod/Web", "" for all), their groups
// relative to it. taken is the servers file's hosts, alias → where they
// connect (asbruWhere); a host already there under one of the aliases it
// could get is left out, and a name another host has gets its group in
// front. It returns the aliases written and one line per host left out,
// saying why.
func AsbruConfig(data []byte, group string, taken map[string]string) (string, []string, []string, error) {
	t, err := readAsbru(data)
	if err != nil {
		return "", nil, nil, err
	}
	ids := slices.Sorted(maps.Keys(t.nodes))
	file, used := map[string]string{}, map[string]bool{} // the servers file's aliases, this import's
	for a, w := range taken {
		file[strings.ToLower(a)] = w
	}
	var hosts []*asbruHost
	var skipped []string
	jumps := map[string]*asbruHost{}
	for _, id := range ids {
		n := t.nodes[id]
		if n.group {
			continue
		}
		p := t.path(id)
		rel, ok := asbruUnder(p, group)
		if !ok {
			continue
		}
		where := strings.TrimPrefix(p+"/"+n.name, "/")
		if m := asbruStr(n.v["method"]); !strings.EqualFold(m, "ssh") {
			skipped = append(skipped, where+": "+cmpOr(m, "no method")+", not SSH")
			continue
		}
		h, why := t.host(n, rel, jumps, taken)
		if h == nil {
			skipped = append(skipped, where+": "+why)
			continue
		}
		h.alias = asbruAlias(n.name, p, h.where(), file, used) // p: a group imported alone still names its hosts
		if h.alias == "" {
			skipped = append(skipped, where+": already in the servers file")
			continue
		}
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return "", nil, skipped, nil
	}
	names := make([]string, len(hosts))
	for i, h := range hosts {
		names[i] = h.alias
	}
	for _, j := range jumps {
		if !j.old {
			hosts = append(hosts, j)
		}
	}
	slices.SortStableFunc(hosts, func(a, b *asbruHost) int {
		return strings.Compare(cmpOr(a.group, topGroup)+"\x00"+a.alias, cmpOr(b.group, topGroup)+"\x00"+b.alias)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# Imported from Ásbrú Connection Manager on %s: %d servers.\n", time.Now().Format(time.DateOnly), len(names))
	last := "\x00"
	for _, h := range hosts {
		h.supported()
		if g := cmpOr(h.group, topGroup); g != last { // a group always: the hosts may go after others' groups
			fmt.Fprintf(&b, "\n#@ group %s\n", g)
			last = g
		}
		b.WriteString("Host " + h.alias + "\n")
		for _, l := range h.lines {
			b.WriteString(strings.TrimRight("    "+l[0]+" "+l[1], " ") + "\n")
		}
		for _, n := range h.notes {
			b.WriteString("    # " + n + "\n")
		}
	}
	slices.Sort(names)
	return b.String(), names, skipped, nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// asbruUnder is path relative to group, when it is inside it.
func asbruUnder(path, group string) (string, bool) {
	group = strings.Trim(group, "/")
	switch {
	case group == "":
		return path, true
	case path == group:
		return "", true
	case strings.HasPrefix(path, group+"/"):
		return path[len(group)+1:], true
	}
	return "", false
}

// asbruWhere is what a host connects to, as an import tells hosts apart:
// its address (the alias when it has no HostName) and port.
func asbruWhere(alias, addr, port string) string {
	if port == "" || port == "0" {
		port = "22"
	}
	return strings.ToLower(cmpOr(addr, alias)) + ":" + port
}

// where is h's asbruWhere.
func (h *asbruHost) where() string {
	var addr, port string
	for _, l := range h.lines {
		switch l[0] {
		case "HostName":
			addr = l[1]
		case "Port":
			port = l[1]
		}
	}
	return asbruWhere(h.alias, addr, port)
}

// asbruAccents are the letters with marks a slug keeps as their plain one.
var asbruAccents = map[rune]rune{}

func init() {
	for plain, marked := range map[rune]string{'a': "áàâäãå", 'e': "éèêë", 'i': "íìîï", 'o': "óòôöõ", 'u': "úùûü", 'n': "ñ", 'c': "ç", 'y': "ýÿ",
		'A': "ÁÀÂÄÃÅ", 'E': "ÉÈÊË", 'I': "ÍÌÎÏ", 'O': "ÓÒÔÖÕ", 'U': "ÚÙÛÜ", 'N': "Ñ", 'C': "Ç", 'Y': "Ý"} {
		for _, r := range marked {
			asbruAccents[r] = plain
		}
	}
}

// asbruAlias is a Host alias for name: no spaces or patterns, unique (the
// group's last name in front, then a number); "" when the file has the host
// (where) under one of those aliases already: a former import's. A name
// the file gives another host is not this one: Ásbrú repeats names across
// groups (each client its "db"), which an import group by group meets.
func asbruAlias(name, group, where string, file map[string]string, used map[string]bool) string {
	slug := func(s string) string {
		s = strings.Join(strings.Fields(s), "-")
		return strings.Map(func(r rune) rune {
			if p, ok := asbruAccents[r]; ok {
				return p
			}
			if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				return r
			}
			return -1
		}, s)
	}
	a := cmpOr(slug(name), "host")
	try := []string{a}
	if g := slug(filepath.Base(group)); g != "" && g != "." {
		try = append(try, g+"-"+a)
	}
	for i := 2; i < 100; i++ {
		try = append(try, fmt.Sprintf("%s-%d", a, i))
	}
	for _, c := range try {
		if w, ok := file[strings.ToLower(c)]; ok && w == where {
			return ""
		}
	}
	for _, c := range try {
		if _, ok := file[strings.ToLower(c)]; !ok && !used[strings.ToLower(c)] {
			used[strings.ToLower(c)] = true
			return c
		}
	}
	return ""
}

// host is node's Host block (its alias still to give), or nil and why not.
func (t *asbruTree) host(n *asbruNode, group string, jumps map[string]*asbruHost, taken map[string]string) (*asbruHost, string) {
	f := func(k string) string { return strings.TrimSpace(asbruStr(n.v[k])) }
	ip, user, port := f("ip"), f("user"), f("port")
	if f("auth type") == "publickey" && f("passphrase user") != "" { // Ásbrú logs in as it then (and blanks user)
		user = f("passphrase user")
	}
	for _, s := range []string{ip, user, port} {
		if strings.Contains(s, "<") && strings.Contains(s, ">") {
			return nil, "uses Ásbrú variables (" + s + ")"
		}
	}
	if ip == "" {
		return nil, "no address"
	}
	h := &asbruHost{group: group}
	h.set("HostName", ip)
	if user != "" {
		h.set("User", user)
	}
	if port != "" && port != "0" && port != "22" {
		h.set("Port", port)
	}
	if k := f("public key"); k != "" {
		h.set("IdentityFile", k)
		if f("auth type") == "publickey" && f("auth fallback") == "0" {
			h.set("PreferredAuthentications", "publickey")
		}
	}
	proxy := f("use proxy")
	get := func(k string) string { return strings.TrimSpace(asbruStr(n.v[k])) }
	if proxy == "0" || proxy == "" { // the global setting
		switch asbruStr(t.defaults["proxy"]) {
		case "Jump":
			proxy, get = "3", func(k string) string { return strings.TrimSpace(asbruStr(t.defaults[k])) }
		case "Proxy":
			proxy, get = "1", func(k string) string { return strings.TrimSpace(asbruStr(t.defaults[k])) }
		}
	}
	switch proxy {
	case "1": // SOCKS 5
		if p := get("proxy ip"); p != "" {
			h.set("ProxyCommand", "nc -X 5 -x "+p+":"+cmpOr(get("proxy port"), "8080")+" %h %p")
		}
	case "3":
		if j := get("jump ip"); j != "" {
			h.set("ProxyJump", asbruJump(j, get("jump port"), get("jump user"), get("jump key"), jumps, taken))
		}
	}
	if d := strings.TrimSpace(strings.SplitN(f("description"), "\n", 2)[0]); d != "" && !strings.HasPrefix(d, "Connection with '") { // not Ásbrú's default one
		h.set("#@ desc", d)
	}
	asbruOptions(h, f("options"))
	return h, ""
}

// asbruJump is the alias of the hidden Host block a jump host gets, one per
// address, port, user and key, for ProxyJump. One the servers file has
// already (a former import's) is used as it is.
func asbruJump(ip, port, user, key string, jumps map[string]*asbruHost, taken map[string]string) string {
	k := strings.Join([]string{ip, port, user, key}, "\x00")
	if j := jumps[k]; j != nil {
		return j.alias
	}
	alias := "jump-" + strings.NewReplacer(":", "-", "[", "", "]", "").Replace(ip)
	if port != "" && port != "22" && port != "0" {
		alias += "-" + port
	}
	clash := func(a string) bool {
		if w, ok := taken[a]; ok && w != asbruWhere(a, ip, port) { // the file's host of that name goes elsewhere
			return true
		}
		return slices.ContainsFunc(slices.Collect(maps.Values(jumps)), func(o *asbruHost) bool { return o.alias == a }) // another user or key there
	}
	for n := 2; clash(alias); n++ {
		alias = strings.TrimSuffix(alias, "-"+strconv.Itoa(n-1)) + "-" + strconv.Itoa(n)
	}
	_, old := taken[alias]
	j := &asbruHost{alias: alias, old: old, lines: [][2]string{{"HostName", ip}, {"#@ hidden", ""}}}
	if user != "" {
		j.set("User", user)
	}
	if port != "" && port != "22" && port != "0" {
		j.set("Port", port)
	}
	if key != "" {
		j.set("IdentityFile", key)
	}
	jumps[k] = j
	return alias
}

// asbruOptions turns the ssh command line options Ásbrú keeps per host into
// config lines; what has no config keyword is left as a comment.
func asbruOptions(h *asbruHost, opts string) {
	args := splitQuoted(opts)
	flags := map[byte][2]string{'X': {"ForwardX11", "yes"}, 'Y': {"ForwardX11Trusted", "yes"}, 'A': {"ForwardAgent", "yes"},
		'C': {"Compression", "yes"}, '4': {"AddressFamily", "inet"}, '6': {"AddressFamily", "inet6"}, 'g': {"GatewayPorts", "yes"}}
	quiet := "xa12" // the defaults, or protocol choices ssh no longer has
	withArg := map[string]string{"-p": "Port", "-i": "IdentityFile", "-l": "User", "-J": "ProxyJump", "-D": "DynamicForward", "-L": "LocalForward", "-R": "RemoteForward"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-o":
			k, v, ok := strings.Cut(next(), "=")
			if !ok {
				k, v, _ = strings.Cut(k, " ")
			}
			if k = strings.TrimSpace(k); k != "" {
				h.set(k, strings.TrimSpace(v))
			}
		case withArg[a] != "":
			v := next()
			if a == "-L" || a == "-R" { // [bind:]port:host:hostport → [bind:]port host:hostport
				if p := strings.Split(v, ":"); len(p) >= 3 {
					v = strings.Join(p[:len(p)-2], ":") + " " + strings.Join(p[len(p)-2:], ":")
				}
				h.lines = append(h.lines, [2]string{withArg[a], v}) // several are fine
				continue
			}
			h.set(withArg[a], v)
		case len(a) > 1 && a[0] == '-' && strings.Trim(a[1:], "XYACg46xa12") == "":
			for _, c := range []byte(a[1:]) {
				if kv, ok := flags[c]; ok {
					h.set(kv[0], kv[1])
				} else if !strings.ContainsRune(quiet, rune(c)) {
					h.notes = append(h.notes, "Ásbrú ssh option not imported: -"+string(c))
				}
			}
		default:
			h.notes = append(h.notes, "Ásbrú ssh option not imported: "+a)
		}
	}
}

// ImportAsbru adds the SSH hosts of Ásbrú's file (under group, "" all) to
// the servers file of the organization folder org, making it when it has
// none; hosts it has already (same address and port) are left out, and a
// name another host has gets the group in front. It returns the file
// and what ImportAsbru added and skipped.
func ImportAsbru(org, file, group string) (string, []string, []string, error) {
	if err := AsbruCurrent(file); err != nil {
		return "", nil, nil, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", nil, nil, err
	}
	path := serversTarget(org)
	taken := map[string]string{}
	old, err := os.ReadFile(path)
	if err == nil {
		hs, _ := parseServers(path, org, false)
		for _, h := range hs {
			taken[h.alias] = asbruWhere(h.alias, h.addr, h.port)
		}
	} else if !os.IsNotExist(err) {
		return "", nil, nil, err
	}
	text, added, skipped, err := AsbruConfig(data, group, taken)
	if err != nil || len(added) == 0 {
		return path, nil, skipped, err
	}
	if len(old) > 0 {
		text = "\n" + text
		if old[len(old)-1] != '\n' {
			text = "\n" + text
		}
	}
	os.MkdirAll(filepath.Dir(path), 0o755) // a [servers] file in a folder not made yet
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, nil, err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return path, added, skipped, err
}

// ServersMain is `desk servers import-asbru [-org DIR] [-group PATH] [FILE]`:
// Ásbrú's SSH hosts as a servers file, on stdout, or added to the servers file
// of the organization folder DIR.
func ServersMain(args []string, out, errw io.Writer) int {
	if len(args) == 0 || args[0] != "import-asbru" {
		fmt.Fprintln(errw, "usage: desk servers import-asbru [-org DIR] [-group PATH] [FILE]  (FILE: "+AsbruPath()+")")
		return 2
	}
	fs := flag.NewFlagSet("import-asbru", flag.ContinueOnError)
	fs.SetOutput(errw)
	org := fs.String("org", "", "add them to this organization folder's servers file (else print it)")
	group := fs.String("group", "", "only the hosts in this Ásbrú group (a path: Prod/Web)")
	if fs.Parse(args[1:]) != nil {
		return 2
	}
	file := cmpOr(fs.Arg(0), AsbruPath())
	var added, skipped []string
	var where string
	var err error
	if *org == "" {
		var data []byte
		if err = AsbruCurrent(file); err == nil {
			data, err = os.ReadFile(file)
		}
		if err == nil {
			var text string
			text, added, skipped, err = AsbruConfig(data, *group, nil)
			fmt.Fprint(out, text)
		}
	} else {
		where, added, skipped, err = ImportAsbru(*org, file, *group)
	}
	if err != nil {
		fmt.Fprintln(errw, "desk servers:", err)
		return 1
	}
	for _, s := range skipped {
		fmt.Fprintln(errw, "skipped", s)
	}
	if where != "" {
		fmt.Fprintf(errw, "%d servers added to %s\n", len(added), where)
	}
	return 0
}
