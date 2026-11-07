package sshconfig

import (
	"errors"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	for _, c := range []struct {
		s, p string
		want bool
	}{
		{"", "", true}, {"a", "", false}, {"", "*", true}, {"abc", "a*c", true}, {"abc", "a?c", true},
		{"ac", "a?c", false}, {"abcbc", "*bc", true}, {"abcbd", "*bc", false}, {"a.example.com", "*.example.com", true},
		{"example.com", "*.example.com", false}, {"aXbXc", "a*b*c", true}, {"abc", "abcd", false}, {"abc", "ab", false},
		{"ab", "a**", true}, {"x", "?", true}, {"xy", "?", false}, {"mississippi", "*ss*ss*pi", true},
	} {
		if got := matchPattern(c.s, c.p); got != c.want {
			t.Errorf("matchPattern(%q, %q) = %v", c.s, c.p, got)
		}
	}
}

func TestMatchPatternList(t *testing.T) {
	for _, c := range []struct {
		s, list string
		lower   bool
		want    int
	}{
		{"a", "a,b", false, 1}, {"b", "a,b", false, 1}, {"c", "a,b", false, 0}, {"a", "!a,a", false, -1},
		{"a", "a,!a", false, -1}, {"a", "!b", false, 0}, {"A", "a", false, 0}, {"a", "A", true, 1},
		{"a", "", false, 0}, {"a", ",a", false, 1}, {"a", strings.Repeat("a", 1023), false, 0},
		{"a", strings.Repeat("x", 1022) + ",a", false, 1},
	} {
		if got := matchPatternList(c.s, c.list, c.lower); got != c.want {
			t.Errorf("matchPatternList(%q, %q) = %d, want %d", c.s, c.list, got, c.want)
		}
	}
	if matchHostname("A.Example.COM", "*.EXAMPLE.com") != 1 {
		t.Error("matchHostname folds case")
	}
}

func TestMatchCIDRList(t *testing.T) {
	ip := netip.MustParseAddr("192.0.2.7")
	ip6 := netip.MustParseAddr("2001:db8::1%eth0")
	for _, c := range []struct {
		addr *netip.Addr
		list string
		want int
	}{
		{&ip, "192.0.2.0/24", 1}, {&ip, "198.51.100.0/24,192.0.2.7", 1}, {&ip, "192.0.2.6", 0},
		{&ip6, "2001:db8::/32", 1}, {&ip6, "192.0.2.0/24", 0}, {&ip, "2001:db8::/32", 0},
		{nil, "192.0.2.0/24", 0}, {nil, "192.0.2.1/24", -1}, {nil, "", -1}, {nil, "a,", -1},
		{nil, "10.0.0.0/33", -1}, {nil, "10.0.0.0/x", -1}, {nil, "x", -1}, {nil, strings.Repeat("1", 50), -1},
		{nil, "10.1/8", -1}, {&ip, "0.0.0.0/0", 1}, {nil, "::1/129", -1}, {nil, "1.2.3.4.5", -1}, {nil, "0.0.0.0/x", -1},
		{nil, "2001:0db8:0000:0000:0000:0000:0000:0000/0032", 0},        // 44 bytes: still short enough
		{nil, "2001:0db8:0000:0000:0000:0000:0000:0000/0000000032", -1}, // 50: too long, valid or not
	} {
		if got := matchCIDRList(c.addr, c.list); got != c.want {
			t.Errorf("matchCIDRList(%v, %q) = %d, want %d", c.addr, c.list, got, c.want)
		}
	}
}

func TestArgvSplit(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"a b\tc", []string{"a", "b", "c"}, true},
		{`"a b" 'c d'`, []string{"a b", "c d"}, true},
		{`a"b c"d`, []string{"ab cd"}, true},
		{`a\ b \"x\" \\ \'`, []string{"a b", `"x"`, `\`, "'"}, true},
		{`"a\ b" \n x\`, []string{`a\ b`, `\n`, `x\`}, true},
		{`a #b c`, []string{"a"}, true},
		{`a#b c`, []string{"a#b", "c"}, true},
		{`"" x`, []string{"", "x"}, true},
		{`"a`, nil, false},
		{`a 'b`, nil, false},
		{"   ", nil, true},
		{`"it's"`, []string{"it's"}, true},
	} {
		got, ok := argvSplit(c.in)
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("argvSplit(%q) = %q %v", c.in, got, ok)
		}
	}
}

func TestStrdelim(t *testing.T) {
	for _, c := range []struct {
		in, tok, rest string
		more, ok      bool
	}{
		{"Host x", "Host", "x", true, true},
		{"Host=x", "Host", "x", true, true},
		{"Host = x", "Host", "x", true, true},
		{"Host  ==x", "Host", "=x", true, true},
		{"Host==x", "Host", "=x", true, true},
		{`"Host" x`, "Host", "x", true, true},
		{`a"b c"=d`, "ab c", "=d", true, true},
		{`"open`, "", "", false, false},
		{"alone", "alone", "", false, true},
		{" lead", "", "lead", true, true},
	} {
		tok, rest, more, ok := strdelim(c.in)
		if tok != c.tok || rest != c.rest || more != c.more || ok != c.ok {
			t.Errorf("strdelim(%q) = %q %q %v %v", c.in, tok, rest, more, ok)
		}
	}
}

func TestConvtime(t *testing.T) {
	for in, want := range map[string]int{
		"0": 0, "90": 90, "90s": 90, "90S": 90, "1m": 60, "1h30m": 5400, "2d": 172800, "1w": 604800,
		"1W1D1H1M1": 694861, "1.5": 1, "1.9s": 1, "": -1, "x": -1, "1x": -1, "1s1": -1, "1.5m": -1,
		"1.s": -1, ".": -1, "1.2.3": -1, "1e5": -1, "3000000000": -1, "1m1m": 120, "-1": -1,
		"2147483647": 2147483647, "2147483648": -1, ".5": 0,
	} {
		if got := convtime(in); got != want {
			t.Errorf("convtime(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestScanScaled(t *testing.T) {
	for in, want := range map[string]int64{
		"0": 0, "16": 16, "1K": 1024, "1k": 1024, "1M": 1 << 20, "1G": 1 << 30, "1T": 1 << 40, "1P": 1 << 50,
		"1E": 1 << 60, "1B": 1, "1.5K": 1536, "2.25k": 2304, " 7": 7, "+5": 5, "-5": -5, "-1.5K": -1536,
		"1.": 1, "0.5M": 524288, "1.000000000000000000000001K": 1024, "7.9": 7, "1.5": 1, "-1K": -1024,
		// From ssh -G -o "RekeyLimit …".
		"0.1234567890123456789E": 142335986942043238, "9.999999999999P": 11258999068425112,
		"1.00000000000000000099E": 1152921504606846977, "1.0000000000000000000099E": 1152921504606846976,
		"0.999999999999999999K": 1023, "00000000000000000001K": 1024, "1K-": 1024, "2M.": 2 << 20,
		"0.00000000000000000089E": 0, "0.9223372036854775807K": 944, "9223372036854775807": 1<<63 - 1,
	} {
		if got, ok := scanScaled(in); !ok || got != want {
			t.Errorf("scanScaled(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"1X", "1Kb", "1..2", "--1", "+-1", "99999999999999999999", "1234567890123456789012",
		"9E", "16E", "-9E", "1.5.", "x", "0.9999999999999999999K", "000000000000000000001K",
		"0.9223372036854775808K", "9223372036854775808"} {
		if v, ok := scanScaled(in); ok {
			t.Errorf("scanScaled(%q) = %d, want an error", in, v)
		}
	}
}

func TestStrtonum(t *testing.T) {
	for _, c := range []struct {
		in     string
		lo, hi int64
		v      int64
		e      string
	}{
		{"5", 0, 10, 5, ""}, {" +5", 0, 10, 5, ""}, {"-0", 0, 10, 0, ""}, {"-1", 0, 10, 0, "too small"},
		{"11", 0, 10, 0, "too large"}, {"", 0, 10, 0, "invalid"}, {"5 ", 0, 10, 0, "invalid"},
		{"+", 0, 10, 0, "invalid"}, {"99999999999999999999", 0, 10, 0, "too large"},
		{"-99999999999999999999", -10, 10, 0, "too small"}, {"\t5", 0, 10, 5, ""},
		{"99999999999999999999", 0, 1<<63 - 1, 0, "too large"}, {"-3", -5, 5, -3, ""}, {"9223372036854775807", 0, 1<<63 - 1, 1<<63 - 1, ""},
	} {
		v, e := strtonum(c.in, c.lo, c.hi)
		if v != c.v || e != c.e {
			t.Errorf("strtonum(%q) = %d %q", c.in, v, e)
		}
	}
}

func TestA2port(t *testing.T) {
	old := lookupPort
	lookupPort = func(s string) (int, error) {
		switch s {
		case "ssh":
			return 22, nil
		case "": // as Go's has it
			return 0, nil
		}
		return 0, errors.New("unknown")
	}
	t.Cleanup(func() { lookupPort = old })
	for in, want := range map[string]int{"22": 22, "0": 0, "65535": 65535, "65536": -1, "ssh": 22, "SSH": -1, "": -1, "nope": -1, "-1": -1} {
		if got := a2port(in); got != want {
			t.Errorf("a2port(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestA2tun(t *testing.T) {
	for in, want := range map[string][2]int{
		"any": {tunAny, tunAny}, "ANY": {tunAny, tunAny}, "3": {3, tunAny}, "1:2": {1, 2}, "any:4": {tunAny, 4},
		"1:any": {1, tunAny}, "x": {tunErr, tunAny}, "1:x": {tunErr, tunErr}, "x:1": {tunErr, 1}, "2147483645": {2147483645, tunAny},
		"2147483646": {tunErr, tunAny},
	} {
		if l, r := a2tun(in); l != want[0] || r != want[1] {
			t.Errorf("a2tun(%q) = %d:%d, want %v", in, l, r, want)
		}
	}
}

func TestIPQoS(t *testing.T) {
	for in, want := range map[string]int{"ef": 0xb8, "EF": 0xb8, "cs0": 0, "none": 1<<31 - 1, "lowdelay": -1 << 31, "16": 16, "255": 255, "256": -1, "x": -1} {
		if got := parseIPQoS(in); got != want {
			t.Errorf("parseIPQoS(%q) = %d", in, got)
		}
	}
	for v, want := range map[int]string{0xb8: "ef", 0: "cs0", 16: "0x10", 1<<31 - 1: "none", 0x04: "le"} {
		if got := iptos2str(v); got != want {
			t.Errorf("iptos2str(%d) = %q", v, got)
		}
	}
}

func TestInetAton(t *testing.T) {
	for in, want := range map[string]string{
		"127.1": "127.0.0.1", "0x7f.1": "127.0.0.1", "010.0.0.1": "8.0.0.1", "1.2.3": "1.2.0.3", "192.0.2.01": "192.0.2.1",
		"4294967295": "255.255.255.255", "1.16777215": "1.255.255.255", "1.2.65535": "1.2.255.255", "0": "0.0.0.0",
		"0X10.0.0.1": "16.0.0.1", "00": "0.0.0.0",
	} {
		if a, ok := inetAton(in); !ok || a.String() != want {
			t.Errorf("inetAton(%q) = %v %v, want %s", in, a, ok, want)
		}
	}
	for _, in := range []string{"999.1.1.1", "1.2.3.4.5", "", "1..2", "1.2.3.", "08", "0x", "4294967296", "1.16777216",
		"1.2.65536", "256.1", "a.1", "1.+2", "0x1g", "1.2.3.256"} {
		if a, ok := inetAton(in); ok {
			t.Errorf("inetAton(%q) = %v, want none", in, a)
		}
	}
}

func TestNumericAddr(t *testing.T) {
	old := zoneOK
	zoneOK = func(z string) bool { return z == "eth0" || z == "1" }
	t.Cleanup(func() { zoneOK = old })
	for _, c := range []struct {
		in     string
		family int
		want   string
	}{
		{"0:0::1", afUnspec, "::1"}, {"FE80::1", afUnspec, "fe80::1"}, {"fe80::1%eth0", afUnspec, "fe80::1%eth0"},
		{"fe80::1%zz", afUnspec, ""}, {"::1", afInet, ""}, {"::1", afInet6, "::1"}, {"127.1", afInet6, ""},
		{"127.1", afInet, "127.0.0.1"}, {"1:2", afUnspec, ""}, {"::ffff:192.0.2.1", afUnspec, "::ffff:192.0.2.1"},
	} {
		a, ok := numericAddr(c.in, c.family)
		if got := a.String(); ok != (c.want != "") || ok && got != c.want {
			t.Errorf("numericAddr(%q, %d) = %s %v", c.in, c.family, got, ok)
		}
	}
	if !old("1") || old("no-such-interface-here") {
		t.Error("zoneOK: numbers are fine, unknown interfaces aren't")
	}
	for in, want := range map[string]bool{"1.2.3": true, "::1": true, "a%b": true, "h.example.com": false, "1.2.x": false} {
		if isAddrFast(in) != want {
			t.Errorf("isAddrFast(%q)", in)
		}
	}
}

func TestVis(t *testing.T) {
	for c, want := range map[byte]string{'~': "~", 'a': "a", '\\': `\\`, ' ': `\040`, 0xa0: `\240`, 1: `\^A`, '\t': `\^I`,
		0x7f: `\^?`, 0xe9: `\M-i`, 0x81: `\M^A`, 0xff: `\M^?`, 0: `\^@`, '!': "!"} {
		if got := vis(c); got != want {
			t.Errorf("vis(%#x) = %q, want %q", c, got, want)
		}
	}
}

func TestValidators(t *testing.T) {
	for in, want := range map[string]string{"Example.COM.": "example.com", "_srv.example.com": "_srv.example.com", "a-b": "a-b", "1a": "1a"} {
		if got, bad := validDomain(in, true); bad != "" || got != want {
			t.Errorf("validDomain(%q) = %q %q", in, got, bad)
		}
	}
	if got, _ := validDomain("Ab", false); got != "Ab" {
		t.Errorf("validDomain without lowering = %q", got)
	}
	for _, in := range []string{"", "-a", ".a", "a..b", "a b", "a!b", "a/b"} {
		if _, bad := validDomain(in, true); bad == "" {
			t.Errorf("validDomain(%q) passed", in)
		}
	}
	for in, want := range map[string]bool{"h.example.com": true, "-h": false, "a b": false, "a$b": false, "a,b": false,
		"a\x01": false, "a\x7f": false, "": true, "[::1]": true} {
		if validHostname(in) != want {
			t.Errorf("validHostname(%q)", in)
		}
	}
	for in, want := range map[string]bool{"a ": true, "me": true, "-me": false, "a;b": false, "a b": true, "a -b": false, `a\`: false,
		`a\b`: true, "a\x01": false, "a\x7f": false, "a$b": true, "a\t-": false} {
		if validRuser(in) != want {
			t.Errorf("validRuser(%q)", in)
		}
	}
	for in, want := range map[string]bool{"A_1": true, "": false, "a-b": false, "x": true} {
		if validEnvName(in) != want {
			t.Errorf("validEnvName(%q)", in)
		}
	}
}

func TestHostPortParsing(t *testing.T) {
	for _, c := range []struct {
		in, user, host string
		port           int
		ok             bool
	}{
		{"h", "", "h", -1, true}, {"me@h:22", "me", "h", 22, true}, {"[::1]:2", "", "::1", 2, true},
		{"a@b@h", "a@b", "h", -1, true}, {"@h", "", "", 0, false}, {"h:0", "", "", 0, false}, {"", "", "", 0, false},
		{"h/x", "", "", 0, false}, {"[::1", "", "", 0, false}, {"h:", "", "h", -1, true}, {"[::1]x", "", "", 0, false},
	} {
		u, h, p, ok := parseUserHostPort(c.in)
		if u != c.user || h != c.host || p != c.port || ok != c.ok {
			t.Errorf("parseUserHostPort(%q) = %q %q %d %v", c.in, u, h, p, ok)
		}
	}
	for _, c := range []struct {
		in, user, host string
		port, r        int
	}{
		{"h", "", "", 0, 1}, {"ssh://h", "", "h", -1, 0}, {"ssh://me@h:2", "me", "h", 2, 0},
		{"ssh://me;fp=x@h", "me", "h", -1, 0}, {"ssh://m%41+e@h", "mA e", "h", -1, 0}, {"ssh://@h", "", "", 0, -1},
		{"ssh://;x@h", "", "", 0, -1}, {"ssh://%zz@h", "", "", 0, -1}, {"ssh://%00@h", "", "", 0, -1}, {"ssh://%4@h", "", "", 0, -1},
		{"ssh://h/p", "", "", 0, -1}, {"ssh://h:2/p", "", "", 0, -1}, {"ssh://h:2/", "", "h", 2, 0}, {"ssh://h/", "", "h", -1, 0},
		{"ssh://h:x", "", "", 0, -1}, {"ssh://[::1]:3", "", "", 0, -1}, // an address isn't a valid_domain
		{"ssh://[h]:3", "", "h", 3, 0}, {"ssh://-h", "", "", 0, -1}, {"ssh://", "", "", 0, -1},
		{"ssh://h:", "", "h", -1, 0}, {"SSH://h", "", "", 0, 1}, {"ssh:/h", "", "", 0, 1},
	} {
		u, h, p, r := parseSSHURI(c.in)
		if u != c.user || h != c.host || p != c.port || r != c.r {
			t.Errorf("parseSSHURI(%q) = %q %q %d %d", c.in, u, h, p, r)
		}
	}
	for _, c := range []struct {
		in, field, rest string
		more            bool
		delim           byte
		ok              bool
	}{
		{"h:1", "h", "1", true, ':', true}, {"h/1", "h", "1", true, '/', true}, {"h", "h", "", false, 0, true},
		{"[a:b]:1", "[a:b]", "1", true, ':', true}, {"[a:b]", "[a:b]", "", false, 0, true}, {"[a:b", "", "", false, 0, false},
		{"[a]b", "", "", false, 0, false},
	} {
		f, r, m, d, ok := hpdelim2(c.in)
		if f != c.field || r != c.rest || m != c.more || d != c.delim || ok != c.ok {
			t.Errorf("hpdelim2(%q) = %q %q %v %q %v", c.in, f, r, m, d, ok)
		}
	}
	if f, _, _, ok := hpdelim("h/1"); ok || f != "" {
		t.Error("hpdelim refuses /")
	}
	for in, want := range map[string]string{"[h]": "h", "[h": "[h", "h]": "h]", "[]": "", "h": "h"} {
		if got := cleanhostname(in); got != want {
			t.Errorf("cleanhostname(%q) = %q", in, got)
		}
	}
}

func TestParseForward(t *testing.T) {
	t.Setenv("SSHCFG_TEST", "/sock")
	f := func(lh string, lset bool, lp int, lpath, ch string, cp int, cpath string) Forward {
		return Forward{lh, lset, lp, lpath, ch, cp, cpath}
	}
	for _, c := range []struct {
		spec            string
		dynamic, remote bool
		want            Forward
		ok              bool
	}{
		{"1:h:2", false, false, f("", false, 1, "", "h", 2, ""), true},
		{"a:1:h:2", false, false, f("a", true, 1, "", "h", 2, ""), true},
		{"[::1]:1:[::2]:2", false, false, f("::1", true, 1, "", "::2", 2, ""), true},
		{":1:h:2", false, false, f("", true, 1, "", "h", 2, ""), true},
		{"/l:h:2", false, false, f("", false, StreamLocal, "/l", "h", 2, ""), true},
		{"a:1:/c", false, false, f("a", true, 1, "", "", StreamLocal, "/c"), true},
		{"1:/c", false, false, f("", false, 1, "", "", StreamLocal, "/c"), true},
		{"/l:/c", false, false, f("", false, StreamLocal, "/l", "", StreamLocal, "/c"), true},
		{"${SSHCFG_TEST}:/c", false, false, f("", false, StreamLocal, "/sock", "", StreamLocal, "/c"), true},
		{"  1:h:2", false, false, f("", false, 1, "", "h", 2, ""), true},
		{`1:h\:x:2`, false, false, f("", false, 1, "", "h:x", 2, ""), true},
		{`1:h\/x:2`, false, false, f("", false, 1, "", "h/x", 2, ""), true},
		{"[/l]:h:2", false, false, f("", false, StreamLocal, "/l", "h", 2, ""), true},
		{"a:1:b:2:", false, false, f("a", true, 1, "", "b", 2, ""), true},
		{"1080", true, false, f("", false, 1080, "", "socks", 0, ""), true},
		{"a:1080", true, false, f("a", true, 1080, "", "socks", 0, ""), true},
		{"/s", true, false, f("", false, StreamLocal, "/s", "socks", 0, ""), true},
		{"0", true, true, f("", false, 0, "", "socks", 0, ""), true},
		{"0:h:2", false, true, f("", false, 0, "", "h", 2, ""), true},
		{"0:h:2", false, false, Forward{}, false},
		{"0", true, false, Forward{}, false},
		{"a:1:b", true, false, Forward{}, false},
		{"1:h", false, false, Forward{}, false},
		{"1:h:0", false, false, Forward{}, false},
		{"x:h:2", false, false, Forward{}, false},
		{"a:1:b:2:c", false, false, Forward{}, false},
		{"[a:1:h:2", false, false, Forward{}, false},
		{"[a]x:1:h:2", false, false, Forward{}, false},
		{`1:h:2\`, false, false, Forward{}, false},
		{"", false, false, Forward{}, false},
		{"${SSHCFG_NOPE}:h:2", false, false, Forward{}, false},
		{"1:" + strings.Repeat("h", 1025) + ":2", false, false, Forward{}, false},
		{strings.Repeat("h", 1025) + ":1:h:2", false, false, Forward{}, false},
		{"1:/" + strings.Repeat("p", 107), false, false, Forward{}, false},
		{"/" + strings.Repeat("p", 107) + ":h:2", false, false, Forward{}, false},
		{"1:h:[22]", false, false, f("", false, 1, "", "h", 22, ""), true},
		{"[1080]", true, false, f("", false, 1080, "", "socks", 0, ""), true},
		{"[1080]x", true, false, Forward{}, false},
		{"1:/" + strings.Repeat("p", 106), false, false, f("", false, 1, "", "", StreamLocal, "/"+strings.Repeat("p", 106)), true},
	} {
		got, ok := parseForward(c.spec, c.dynamic, c.remote)
		if ok != c.ok || got != c.want {
			t.Errorf("parseForward(%q, %v, %v) = %+v %v", c.spec, c.dynamic, c.remote, got, ok)
		}
	}
}

func TestParseHop(t *testing.T) {
	for _, c := range []struct {
		in     string
		strict bool
		want   Hop
		ok     bool
	}{
		{"h", true, Hop{"", "h", -1}, true}, {"me@h:2", true, Hop{"me", "h", 2}, true},
		{"ssh://me@h:3", true, Hop{"me", "h", 3}, true}, {"a;b", true, Hop{}, false}, {"a;b", false, Hop{"", "a;b", -1}, true},
		{"-me@h", true, Hop{}, false}, {"ssh://h/x", false, Hop{}, false}, {"", false, Hop{}, false},
	} {
		got, ok := parseHop(c.in, c.strict)
		if ok != c.ok || got != c.want {
			t.Errorf("parseHop(%q) = %+v %v", c.in, got, ok)
		}
	}
	o := newOptions()
	if !o.parseJump("a,b", false, false) || o.has("jumphost") {
		t.Error("an inactive ProxyJump is checked but not kept")
	}
	if o.parseJump("a,", false, true) {
		t.Error("an empty hop is an error")
	}
	if !o.parseJump("u@a,v@b:2 # c", false, true) || o.strs["jumphost"] != "b" || o.strs["jumpuser"] != "v" ||
		o.ints["jumpport"] != 2 || o.strs["jumpextra"] != "u@a" || o.strs["proxycommand"] != "none" {
		t.Errorf("parseJump kept %v %v", o.strs, o.ints)
	}
	if !o.parseJump("other", false, true) || o.strs["jumphost"] != "b" {
		t.Error("the first ProxyJump wins")
	}
	if !o.parseJump("none", false, true) || o.strs["jumphost"] != "b" {
		t.Error("none doesn't replace a jump host")
	}
	o = newOptions()
	if !o.parseJump("none", false, false) || o.has("jumphost") {
		t.Error("an inactive none is dropped")
	}
	if !o.parseJump("NONE", false, true) || o.strs["jumphost"] != "none" || o.ints["jumpport"] != 0 {
		t.Error("ProxyJump none")
	}
}

func TestPermOK(t *testing.T) {
	if !permOK("/") {
		t.Error("root's own directory is fine")
	}
	if permOK("/nonexistent/x") && runtime.GOOS != "windows" { // Windows': always true (OpenSSH there checks ACLs, not modes)
		t.Error("what can't be stat'ed isn't")
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("SSHCFG_TEST", "val")
	tok := map[byte]string{'h': "host", 'p': "22"}
	for _, c := range []struct {
		in       string
		env      bool
		want     string
		wantFail bool
	}{
		{"%h:%p", false, "host:22", false}, {"100%%", false, "100%", false}, {"%x", false, "", true}, {"end%", false, "", true},
		{"${SSHCFG_TEST}/%h", true, "val/host", false}, {"${SSHCFG_TEST}", false, "${SSHCFG_TEST}", false},
		{"${SSHCFG_NOPE}", true, "", true}, {"${}", true, "", true}, {"${open", true, "", true}, {"$x {", true, "$x {", false},
		{"${SSHCFG_NOPE}%x", true, "", true},
	} {
		got, err := Expand(c.in, tok, c.env)
		if got != c.want || (err != nil) != c.wantFail {
			t.Errorf("Expand(%q) = %q %v", c.in, got, err)
		}
	}
	if _, err := Expand("${SSHCFG_NOPE}", nil, true); !errors.Is(err, errMissingEnv) {
		t.Errorf("an unset variable is errMissingEnv: %v", err)
	}
	if s, err := expand("50% ${SSHCFG_TEST}", nil, true, false); err != nil || s != "50% val" {
		t.Errorf("dollar only: %q %v", s, err)
	}
}

func TestTilde(t *testing.T) {
	old := lookupUser
	lookupUser = func(n string) (string, error) {
		if n == "friend" {
			return "/home/friend", nil
		}
		return "", errors.New("no")
	}
	t.Cleanup(func() { lookupUser = old })
	for in, want := range map[string]string{"~": "/home/me/", "~/": "/home/me/", "~//x": "/home/me/x", "~/a/b": "/home/me/a/b",
		"~friend": "/home/friend/", "~friend/x": "/home/friend/x", "~friend//x": "/home/friend/x", "/abs": "/abs", "rel~": "rel~"} {
		if got, err := tilde(in, "/home/me"); err != nil || got != want {
			t.Errorf("tilde(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if got, _ := tilde("~/x", "/"); got != "/x" {
		t.Errorf("tilde from / = %q", got)
	}
	if _, err := tilde("~nobody/x", "/home/me"); err == nil {
		t.Error("an unknown user must fail")
	}
	if h, err := old("root"); err != nil || h == "" {
		t.Skipf("no root user here: %v", err)
	}
}

func TestAssemble(t *testing.T) {
	all := []string{"a", "b", "c", "d"}
	for _, c := range []struct {
		list, want string
		wantFail   bool
	}{
		{"", "a,b", false}, {"+c", "a,b,c", false}, {"+a,c,c", "a,b,c", false}, {"-a", "b", false}, {"-*", "", false},
		{"^c", "c,a,b", false}, {"^b,c", "b,c,a", false}, {"c,a", "c,a", false}, {"*", "a,b,c,d", false},
		{"d,*", "d,a,b,c", false}, {"!a", "", true}, {"x", "", true}, {"+x", "a,b", false}, {"c,c", "c", false},
	} {
		got, err := assemble(c.list, "a,b", all)
		if got != c.want || (err != nil) != c.wantFail {
			t.Errorf("assemble(%q) = %q %v", c.list, got, err)
		}
	}
	if got := namesCat("a,,b", "b,c,c"); got != "a,,b,b,c,c" {
		t.Errorf("namesCat past an empty name = %q", got)
	}
	if got := namesCat("a", "b,,c"); got != "a,b" {
		t.Errorf("namesCat stops at an empty name = %q", got)
	}
	if !namesValid("a,b", all) || namesValid("", all) || namesValid("a,x", all) || !namesValid("a,,x", all) {
		t.Error("namesValid")
	}
	if !keyNamesValid("ssh-ed25519,ssh-rsa*", false) || keyNamesValid("ssh-ed25519-cert-v01@openssh.com", true) ||
		keyNamesValid("", false) || keyNamesValid("SSH-RSA*", false) || filterList("a", "A", false) != "" || keyNamesValid("nomatch*", false) || !keyNamesValid("!ssh-rsa", false) || !keyNamesValid("ssh-rsa,,x", true) {
		t.Error("keyNamesValid")
	}
	if slices.ContainsFunc(plainKeys(), isCert) || len(plainKeys()) != 11 {
		t.Errorf("plainKeys %q", plainKeys())
	}
	if filterList("a,b,c", "!b,*", false) != "a,c" || filterList("a,b,c", "!b,*", true) != "b" || filterList("a,b,c", "b", true) != "a,c" || filterList("a,b,c", "b", false) != "b" {
		t.Error("filterList")
	}
}

func TestStrtolOctal(t *testing.T) {
	for in, want := range map[string]int{"0077": 0o77, "777": 0o777, " 7": 7, "+7": 7, "-7": -7, "0700x": 0o700, "0": 0, "1777": 0o1000, "77777777777777777777777": 0o1000} {
		if got, ok := strtolOctal(in); !ok || got != want {
			t.Errorf("strtolOctal(%q) = %d %v", in, got, ok)
		}
	}
	for _, in := range []string{"8", "", "+", "x7"} {
		if _, ok := strtolOctal(in); ok {
			t.Errorf("strtolOctal(%q) passed", in)
		}
	}
}

func TestStates(t *testing.T) {
	if msTunnel.name(1) != "point-to-point" || msTunnel.name(9) != "UNKNOWN" || msStrict.parse("ACCEPT-NEW") != 1 || msFlag.parse("maybe") != -1 {
		t.Error("states")
	}
	if len(msYesNoAsk) != 5 || len(msConfirm) != 6 || len(msFlag) != 4 {
		t.Error("derived tables share no storage with their base")
	}
}
