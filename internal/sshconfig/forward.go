package sshconfig

import "strings"

// StreamLocal is a Forward port whose end is a Unix socket path.
const StreamLocal = -2

// Forward is one LocalForward, RemoteForward or DynamicForward. A dynamic
// one connects to "socks" (port 0). ListenHostSet tells an empty ListenHost
// given as "[]" (all addresses) from none given (the default bind).
type Forward struct {
	ListenHost    string
	ListenHostSet bool
	ListenPort    int
	ListenPath    string
	ConnectHost   string
	ConnectPort   int
	ConnectPath   string
}

// fwdField is readconf.c's parse_fwd_field: the next ':'-separated field of
// s ("[...]" taken literally, \ escaping otherwise), whether it names a path,
// and what follows. ok is false at the end or on a malformed field, with
// rest left at the field (the caller's trailing-garbage check).
func fwdField(s string) (field string, path bool, rest string, ok bool) {
	if s == "" {
		return "", false, "", false
	}
	if s[0] == '[' {
		end := strings.IndexByte(s, ']')
		if end < 0 || end+1 < len(s) && s[end+1] != ':' {
			return "", false, s, false
		}
		rest = s[end+1:]
		if rest != "" {
			rest = rest[1:]
		}
		return s[1:end], strings.Contains(s[1:end], "/"), rest, true
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 == len(s) {
				return "", false, s, false
			}
			i++ // the escaped byte is taken as is, even a / or a :
			b = append(b, s[i])
			continue
		case '/':
			path = true
		case ':':
			return string(b), path, s[i+1:], true
		}
		b = append(b, s[i])
	}
	return string(b), path, "", true
}

// parseForward is readconf.c's parse_forward:
// [listenhost:]listenport|listenpath:connecthost:connectport|connectpath,
// or [listenhost:]listenport for a dynamic one; ${VAR}s expand first.
func parseForward(spec string, dynamic, remote bool) (Forward, bool) {
	s, err := expand(spec, nil, true, false)
	if err != nil {
		return Forward{}, false
	}
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	type arg struct {
		s    string
		path bool
	}
	var a []arg
	for len(a) < 4 {
		f, path, rest, ok := fwdField(s)
		if !ok {
			s = rest
			break
		}
		a = append(a, arg{f, path})
		s = rest
	}
	var f Forward
	n := len(a)
	if s != "" {
		n = 0
	}
	setListen := func(host string, port string) {
		f.ListenHost, f.ListenHostSet, f.ListenPort = host, true, a2port(port)
	}
	switch n {
	case 1:
		if a[0].path {
			f.ListenPath, f.ListenPort = a[0].s, StreamLocal
		} else {
			f.ListenPort = a2port(a[0].s)
		}
		f.ConnectHost = "socks"
	case 2:
		switch {
		case a[0].path && a[1].path:
			f.ListenPath, f.ListenPort = a[0].s, StreamLocal
			f.ConnectPath, f.ConnectPort = a[1].s, StreamLocal
		case a[1].path:
			f.ListenPort = a2port(a[0].s)
			f.ConnectPath, f.ConnectPort = a[1].s, StreamLocal
		default:
			setListen(a[0].s, a[1].s)
			f.ConnectHost = "socks"
		}
	case 3:
		switch {
		case a[0].path:
			f.ListenPath, f.ListenPort = a[0].s, StreamLocal
			f.ConnectHost, f.ConnectPort = a[1].s, a2port(a[2].s)
		case a[2].path:
			setListen(a[0].s, a[1].s)
			f.ConnectPath, f.ConnectPort = a[2].s, StreamLocal
		default:
			f.ListenPort = a2port(a[0].s)
			f.ConnectHost, f.ConnectPort = a[1].s, a2port(a[2].s)
		}
	case 4:
		setListen(a[0].s, a[1].s)
		f.ConnectHost, f.ConnectPort = a[2].s, a2port(a[3].s)
	}
	const maxHost, maxSun = 1025, 108 // NI_MAXHOST, sizeof sun_path
	switch {
	case n == 0,
		dynamic && n > 2,
		!dynamic && n < 3 && f.ConnectPath == "" && f.ListenPath == "",
		!dynamic && f.ConnectPort <= 0 && f.ConnectPath == "",
		f.ListenPort < 0 && f.ListenPath == "",
		!remote && f.ListenPort == 0,
		len(f.ConnectHost) >= maxHost, len(f.ListenHost) >= maxHost,
		len(f.ConnectPath) >= maxSun, len(f.ListenPath) >= maxSun:
		return Forward{}, false
	}
	return f, true
}

// Hop is one ProxyJump host; Port is 0 when not given.
type Hop struct {
	User, Host string
	Port       int
}

// parseHop is one ProxyJump element: an ssh:// URI or [user@]host[:port].
func parseHop(s string, strict bool) (Hop, bool) {
	u, h, port, r := parseSSHURI(s)
	if r == 1 {
		var ok bool
		if u, h, port, ok = parseUserHostPort(s); !ok {
			return Hop{}, false
		}
	} else if r < 0 {
		return Hop{}, false
	}
	if strict && (!validHostname(h) || u != "" && !validRuser(u)) {
		return Hop{}, false
	}
	return Hop{u, h, port}, true
}

// parseJump is readconf.c's parse_jump: it checks every hop (the
// command-line's strictly) and keeps the last as the jump host, the rest as
// jumpextra. It takes effect only when active and neither a ProxyCommand nor
// a jump host is set yet.
func (o *options) parseJump(s string, strict, active bool) bool {
	if strings.EqualFold(s, "none") {
		if _, set := o.strs["jumphost"]; active && !set {
			o.strs["jumphost"] = "none"
			o.ints["jumpport"] = 0
		}
		return true
	}
	orig, _, _ := strings.Cut(s, "#")
	orig = strings.TrimRight(orig, " \t\n\v\f\r")
	hops := strings.Split(orig, ",")
	var last Hop
	for i := len(hops) - 1; i >= 0; i-- {
		h, ok := parseHop(hops[i], strict)
		if !ok {
			return false
		}
		if i == len(hops)-1 {
			last = h
		}
	}
	_, pc := o.strs["proxycommand"]
	_, jh := o.strs["jumphost"]
	if active && !pc && !jh {
		if last.User != "" {
			o.strs["jumpuser"] = last.User
		}
		o.strs["jumphost"] = last.Host
		o.ints["jumpport"] = last.Port
		o.strs["proxycommand"] = "none"
		if i := strings.LastIndexByte(orig, ','); i >= 0 {
			o.strs["jumpextra"] = orig[:i]
		}
	}
	return true
}
