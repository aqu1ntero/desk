package sshconfig

import (
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// ws is readconf.c's WHITESPACE.
const ws = " \t\r\n"

// strdelim is misc.c's strdelim: the next token of s, split at whitespace,
// one '=' or a quote (whose content joins the token). rest is "" with more
// false where C leaves the pointer NULL; ok is false on an unmatched quote.
func strdelim(s string) (tok, rest string, more, ok bool) {
	i := strings.IndexAny(s, ws+`"=`)
	if i < 0 {
		return s, "", false, true
	}
	if s[i] == '"' {
		j := strings.IndexByte(s[i+1:], '"')
		if j < 0 {
			return "", "", false, false
		}
		return s[:i] + s[i+1:i+1+j], strings.TrimLeft(s[i+2+j:], ws), true, true
	}
	eq := s[i] == '='
	rest = strings.TrimLeft(s[i+1:], ws)
	if !eq && strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], ws)
	}
	return s[:i], rest, true, true
}

// argvSplit is misc.c's argv_split: words with double or single quoting and the
// \\ \" \' (and, unquoted, "\ ") escapes; a word starting with # ends the line.
// ok is false on an unterminated quote.
func argvSplit(s string) (av []string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			continue
		}
		if s[i] == '#' {
			break
		}
		var arg []byte
		var quote byte
		for ; i < len(s); i++ {
			c := s[i]
			switch {
			case c == '\\' && i+1 < len(s) && (s[i+1] == '\'' || s[i+1] == '"' || s[i+1] == '\\' || quote == 0 && s[i+1] == ' '):
				i++
				arg = append(arg, s[i])
			case quote == 0 && (c == ' ' || c == '\t'):
				goto done
			case quote == 0 && (c == '"' || c == '\''):
				quote = c
			case quote != 0 && c == quote:
				quote = 0
			default:
				arg = append(arg, c)
			}
		}
	done:
		av = append(av, string(arg))
		if i >= len(s) {
			if quote != 0 {
				return nil, false
			}
			break
		}
	}
	return av, true
}

// lower is C's lowercase(): ASCII only.
func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
func isAlpha(c byte) bool { return 'a' <= c|32 && c|32 <= 'z' }
func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }
func isSpace(c byte) bool { return c == ' ' || '\t' <= c && c <= '\r' }

// strtonum is OpenBSD's strtonum: a whole base-10 number (leading space and a
// sign allowed) within [lo, hi]; errstr is "" on success.
func strtonum(s string, lo, hi int64) (int64, string) {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	var v int64
	over := false
	for ; i < len(s) && isDigit(s[i]); i++ {
		if v > (math.MaxInt64-int64(s[i]-'0'))/10 {
			over = true
		}
		v = v*10 + int64(s[i]-'0')
	}
	switch {
	case i == start || i != len(s):
		return 0, "invalid"
	case over && neg, neg && -v < lo:
		return 0, "too small"
	case over, !neg && v > hi:
		return 0, "too large"
	}
	if neg {
		v = -v
	}
	return v, ""
}

// lookupPort is getservbyname(s, "tcp"); a var so tests needn't trust /etc/services.
var lookupPort = func(s string) (int, error) { return net.LookupPort("tcp", s) }

// a2port is misc.c's a2port: a port number or tcp service name, -1 if neither.
func a2port(s string) int {
	if v, e := strtonum(s, 0, 65535); e == "" {
		return int(v)
	}
	// getservbyname is case-sensitive, Go's table isn't.
	if s == "" || s != lower(s) {
		return -1
	}
	p, err := lookupPort(s)
	if err != nil {
		return -1
	}
	return p
}

const (
	tunAny = 0x7fffffff
	tunErr = tunAny - 1
	tunMax = tunAny - 2
)

// a2tun is misc.c's a2tun with a remote part: "any", N or local:remote.
func a2tun(s string) (local, remote int) {
	one := func(s string) int {
		if strings.EqualFold(s, "any") {
			return tunAny
		}
		v, e := strtonum(s, 0, tunMax)
		if e != "" {
			return tunErr
		}
		return int(v)
	}
	l, r, found := strings.Cut(s, ":")
	if !found {
		return one(s), tunAny
	}
	remote = one(r)
	if remote == tunErr {
		return tunErr, remote
	}
	return one(l), remote
}

// convtime is misc.c's convtime: 1h30m-style durations in whole seconds, -1 if invalid.
func convtime(s string) int {
	if s == "" {
		return -1
	}
	total := 0.0
	seenSec := false
	for p := 0; p < len(s); {
		if !isDigit(s[p]) && s[p] != '.' {
			return -1
		}
		end := p
		for end < len(s) && (isDigit(s[end]) || s[end] == '.') {
			end++
		}
		num := s[p:end]
		val, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return -1
		}
		mult := 1.0
		unit := byte(0)
		if end < len(s) {
			unit = s[end]
		}
		switch unit {
		case 0, 's', 'S':
			if seenSec {
				return -1
			}
			seenSec = true
		case 'm', 'M':
			mult = 60
		case 'h', 'H':
			mult = 3600
		case 'd', 'D':
			mult = 86400
		case 'w', 'W':
			mult = 604800
		default:
			return -1
		}
		if strings.Contains(num, ".") && (mult > 1 || !isDigit(num[len(num)-1])) {
			return -1
		}
		total += val * mult
		p = end
		if unit != 0 {
			p++
		}
	}
	if total > math.MaxInt32 {
		return -1
	}
	return int(total)
}

// scanScaled is OpenBSD's scan_scaled: a number with an optional B/K/M/G/T/P/E
// (powers of 1024) suffix, fractions allowed before the suffix.
func scanScaled(s string) (int64, bool) {
	const maxDigits = 21
	p := 0
	for p < len(s) && isSpace(s[p]) {
		p++
	}
	sign := int64(0)
	for p < len(s) && (s[p] == '-' || s[p] == '+') {
		if sign != 0 {
			return 0, false
		}
		sign = 1
		if s[p] == '-' {
			sign = -1
		}
		p++
	}
	var whole, fpart int64
	ndigits, fract := 0, 0
	for ; p < len(s) && (isDigit(s[p]) || s[p] == '.'); p++ {
		if s[p] == '.' {
			if fract > 0 {
				return 0, false
			}
			fract = 1
			continue
		}
		d := int64(s[p] - '0')
		if fract > 0 {
			if fract >= maxDigits-1 {
				continue
			}
			fract++
			if fpart > math.MaxInt64/10 || d > math.MaxInt64-fpart*10 {
				return 0, false
			}
			fpart = fpart*10 + d
			continue
		}
		ndigits++
		if ndigits >= maxDigits || whole > math.MaxInt64/10 || d > math.MaxInt64-whole*10 {
			return 0, false
		}
		whole = whole*10 + d
	}
	if sign != 0 {
		whole *= sign
	}
	if p == len(s) {
		return whole, true
	}
	i := strings.IndexByte("BKMGTPE", s[p]&^0x20)
	if i < 0 || p+1 < len(s) && isAlnum(s[p+1]) {
		return 0, false
	}
	fact := int64(1) << (10 * i)
	if whole > math.MaxInt64/fact || whole < math.MinInt64/fact {
		return 0, false
	}
	whole *= fact
	muls, divs := i, 0
	if fract > 0 {
		divs = fract - 1
	}
	for muls > 0 && divs > 0 {
		if fpart <= math.MaxInt64/1024 {
			fpart *= 1024
			muls--
		} else {
			fpart /= 10
			divs--
		}
	}
	for ; muls > 0; muls-- {
		fpart *= 1024
	}
	for ; divs > 0; divs-- {
		fpart /= 10
	}
	if sign == -1 {
		return whole - fpart, true
	}
	return whole + fpart, true
}

// ipqosNames is misc.c's ipqos table; none is INT_MAX, the deprecated names INT_MIN.
var ipqosNames = []struct {
	name string
	v    int
}{
	{"none", math.MaxInt32}, {"af11", 0x28}, {"af12", 0x30}, {"af13", 0x38},
	{"af21", 0x48}, {"af22", 0x50}, {"af23", 0x58}, {"af31", 0x68}, {"af32", 0x70},
	{"af33", 0x78}, {"af41", 0x88}, {"af42", 0x90}, {"af43", 0x98}, {"cs0", 0},
	{"cs1", 0x20}, {"cs2", 0x40}, {"cs3", 0x60}, {"cs4", 0x80}, {"cs5", 0xa0},
	{"cs6", 0xc0}, {"cs7", 0xe0}, {"ef", 0xb8}, {"le", 0x04}, {"va", 0xb0},
	{"lowdelay", math.MinInt32}, {"throughput", math.MinInt32}, {"reliability", math.MinInt32},
}

func parseIPQoS(s string) int {
	for _, q := range ipqosNames {
		if strings.EqualFold(s, q.name) {
			return q.v
		}
	}
	v, e := strtonum(s, 0, 255)
	if e != "" {
		return -1
	}
	return int(v)
}

func iptos2str(v int) string {
	for _, q := range ipqosNames {
		if q.v == v {
			return q.name
		}
	}
	return "0x" + strconv.FormatInt(int64(v), 16)
}

// validDomain is misc.c's valid_domain: the name lowercased (with makelower)
// and without its trailing dot, or why it isn't one.
func validDomain(name string, makelower bool) (string, string) {
	if name == "" {
		return "", "empty domain name"
	}
	if !isAlnum(name[0]) && name[0] != '_' {
		return "", "domain name \"" + name + "\" starts with invalid character"
	}
	if makelower {
		name = lower(name)
	}
	for i := 0; i < len(name); i++ {
		c := lower(name[i : i+1])[0]
		if i > 0 && name[i-1] == '.' && c == '.' {
			return "", "domain name \"" + name + "\" contains consecutive separators"
		}
		if c != '.' && c != '-' && !isAlnum(c) && c != '_' {
			return "", "domain name \"" + name + "\" contains invalid characters"
		}
	}
	return strings.TrimSuffix(name, "."), ""
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) && s[i] != '_' {
			return false
		}
	}
	return true
}

// validHostname is readconf.c's ssh_valid_hostname.
func validHostname(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("'`\"$\\;&<>|(){},", s[i]) >= 0 || isSpace(s[i]) || s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// validRuser is readconf.c's ssh_valid_ruser.
func validRuser(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte("'`\";&<>|(){}", c) >= 0 ||
			isSpace(c) && i+1 < len(s) && s[i+1] == '-' || c == '\\' && i+1 == len(s) {
			return false
		}
	}
	return true
}

// hpdelim2 is misc.c's hpdelim2: the host field of s (up to ':' or '/', a
// [bracketed] one whole), what follows it and the delimiter seen.
func hpdelim2(s string) (field, rest string, more bool, delim byte, ok bool) {
	i := 0
	if strings.HasPrefix(s, "[") {
		j := strings.IndexByte(s, ']')
		if j < 0 {
			return "", "", false, 0, false
		}
		i = j + 1
	} else if j := strings.IndexAny(s, ":/"); j >= 0 {
		i = j
	} else {
		i = len(s)
	}
	switch {
	case i == len(s):
		return s, "", false, 0, true
	case s[i] == ':' || s[i] == '/':
		return s[:i], s[i+1:], true, s[i], true
	}
	return "", "", false, 0, false
}

// hpdelim is hpdelim2 accepting only ':'.
func hpdelim(s string) (field, rest string, more, ok bool) {
	f, r, m, d, ok := hpdelim2(s)
	if d == '/' {
		return "", "", false, false
	}
	return f, r, m, ok
}

func cleanhostname(h string) string {
	if len(h) >= 2 && h[0] == '[' && h[len(h)-1] == ']' {
		return h[1 : len(h)-1]
	}
	return h
}

// parseUserHostPort is misc.c's parse_user_host_port: [user@]host[:port], port -1 when absent.
func parseUserHostPort(s string) (user, host string, port int, ok bool) {
	port = -1
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		if i == 0 {
			return "", "", 0, false
		}
		user, s = s[:i], s[i+1:]
	}
	h, rest, more, ok := hpdelim(s)
	if !ok || h == "" {
		return "", "", 0, false
	}
	if more && rest != "" {
		if port = a2port(rest); port <= 0 {
			return "", "", 0, false
		}
	}
	return user, cleanhostname(h), port, true
}

func urldecode(s string) (string, bool) {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			b = append(b, ' ')
		case '%':
			if i+2 >= len(s) {
				return "", false
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil || v == 0 {
				return "", false
			}
			b = append(b, byte(v))
			i += 2
		default:
			b = append(b, s[i])
		}
	}
	return string(b), true
}

// parseSSHURI is readconf.c's parse_ssh_uri over misc.c's parse_uri:
// ssh://[user[;params]@]host[:port] with no path. r is 0 for a URI, 1 when s
// isn't one, -1 for a bad one.
func parseSSHURI(s string) (user, host string, port, r int) {
	s, isURI := strings.CutPrefix(s, "ssh://")
	if !isURI {
		return "", "", 0, 1
	}
	port = -1
	if i := strings.IndexByte(s, '@'); i >= 0 {
		u, _, _ := strings.Cut(s[:i], ";")
		var ok bool
		if user, ok = urldecode(u); u == "" || !ok {
			return "", "", 0, -1
		}
		s = s[i+1:]
	}
	h, rest, more, delim, ok := hpdelim2(s)
	if !ok || h == "" {
		return "", "", 0, -1
	}
	host = cleanhostname(h)
	if _, bad := validDomain(host, false); bad != "" {
		return "", "", 0, -1
	}
	if more && delim == ':' && rest != "" {
		p, path, _ := strings.Cut(rest, "/")
		if port = a2port(p); port <= 0 {
			return "", "", 0, -1
		}
		rest = path
	}
	if more && rest != "" {
		return "", "", 0, -1 // a path isn't allowed
	}
	return user, host, port, 0
}

// inetAton is glibc's inet_aton as getaddrinfo(AI_NUMERICHOST) uses it:
// 1 to 4 parts, each decimal, 0octal or 0xhex, the last filling what's left.
func inetAton(s string) (netip.Addr, bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	var vals []uint64
	for _, p := range parts {
		if p == "" || !isDigit(p[0]) {
			return netip.Addr{}, false
		}
		base, digits := 10, p
		if len(p) > 1 && p[0] == '0' {
			base, digits = 8, p[1:]
			if digits[0]|0x20 == 'x' {
				base, digits = 16, digits[1:]
				if digits == "" {
					return netip.Addr{}, false
				}
			}
		}
		v := uint64(0)
		if digits != "" {
			var err error
			if v, err = strconv.ParseUint(digits, base, 32); err != nil || strings.ContainsAny(digits, "+-_") {
				return netip.Addr{}, false
			}
		}
		vals = append(vals, v)
	}
	n := len(vals)
	for _, v := range vals[:n-1] {
		if v > 0xff {
			return netip.Addr{}, false
		}
	}
	if vals[n-1] > 0xffffffff>>(8*(n-1)) {
		return netip.Addr{}, false
	}
	ip := vals[n-1]
	for i, v := range vals[:n-1] {
		ip |= v << (8 * (3 - i))
	}
	return netip.AddrFrom4([4]byte{byte(ip >> 24), byte(ip >> 16), byte(ip >> 8), byte(ip)}), true
}

// Address families, as Linux numbers them (only compared and printed).
const (
	afUnspec = 0
	afInet   = 2
	afInet6  = 10
)

// zoneOK is whether an IPv6 scope names an interface (or is a number), as
// getaddrinfo requires; a var so tests needn't depend on the host's interfaces.
var zoneOK = func(z string) bool {
	if _, e := strtonum(z, 0, math.MaxUint32); e == "" {
		return true
	}
	_, err := net.InterfaceByName(z)
	return err == nil
}

// numericAddr parses s as getaddrinfo(AI_NUMERICHOST) would for family.
func numericAddr(s string, family int) (netip.Addr, bool) {
	if strings.Contains(s, ":") {
		a, err := netip.ParseAddr(s)
		if err != nil || family == afInet || a.Zone() != "" && !zoneOK(a.Zone()) {
			return netip.Addr{}, false
		}
		return a, true
	}
	a, ok := inetAton(s)
	return a, ok && family != afInet6
}

// isAddrFast is ssh.c's is_addr_fast: names that can only be addresses.
func isAddrFast(s string) bool {
	return strings.ContainsAny(s, "%:") || strings.Trim(s, "0123456789.") == ""
}

// vis is OpenBSD's vis(3) with VIS_WHITE, as escapechar is printed.
func vis(c byte) string {
	switch {
	case c > ' ' && c < 0x7f && c != '\\':
		return string(c)
	case c == '\\':
		return `\\`
	case c&0x7f == ' ':
		return `\` + strconv.FormatInt(int64(c)>>6&7, 8) + strconv.FormatInt(int64(c)>>3&7, 8) + strconv.FormatInt(int64(c)&7, 8)
	}
	s := `\`
	if c&0x80 != 0 {
		c &= 0x7f
		s += "M"
	}
	switch {
	case c == 0x7f:
		return s + "^?"
	case c < 0x20:
		return s + "^" + string(c+'@')
	}
	return s + "-" + string(c)
}
