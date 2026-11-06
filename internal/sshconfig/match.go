package sshconfig

import (
	"net/netip"
	"strings"
)

// matchPattern is match.c's match_pattern: * and ? wildcards over bytes.
func matchPattern(s, pat string) bool {
	// Classic greedy matching with one backtrack point: the same answers as
	// OpenSSH's NFA.
	si, pi, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case pi < len(pat) && (pat[pi] == '?' || pat[pi] == s[si]):
			si++
			pi++
		case pi < len(pat) && pat[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			mark++
			si, pi = mark, star+1
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

// matchPatternList is match.c's match_pattern_list: 1 when a comma-separated
// pattern matches, -1 when a !negated one does, else 0.
func matchPatternList(s, list string, dolower bool) int {
	got := 0
	for i := 0; i < len(list); {
		neg := list[i] == '!'
		if neg {
			i++
		}
		j := strings.IndexByte(list[i:], ',')
		if j < 0 {
			j = len(list) - i
		}
		sub := list[i : i+j]
		i += j
		if i < len(list) {
			i++
		}
		if len(sub) >= 1023 {
			return 0
		}
		if dolower {
			sub = lower(sub)
		}
		if matchPattern(s, sub) {
			if neg {
				return -1
			}
			got = 1
		}
	}
	return got
}

// matchHostname is match.c's match_hostname: the host lowercased against a lowercased list.
func matchHostname(host, list string) int {
	return matchPatternList(lower(host), list, true)
}

// matchCIDRList is addrmatch.c's addr_match_cidr_list: 1 when addr (nil to
// only check the list) is in one of the comma-separated networks, 0 when
// not, -1 for a bad list.
func matchCIDRList(addr *netip.Addr, list string) int {
	ret := 0
	for _, e := range strings.Split(list, ",") {
		if e == "" || len(e) > 46+3 {
			return -1
		}
		if strings.Trim(e, "0123456789abcdefABCDEF.:/") != "" {
			ret = -1
		}
		ip, bits, ok := parseCIDR(e)
		if !ok {
			return -1
		}
		if addr != nil && addr.Is4() == ip.Is4() {
			if p, _ := addr.WithZone("").Prefix(bits); p.Addr() == ip {
				ret = 1
			}
		}
	}
	return ret
}

// parseCIDR is addr.c's addr_pton_cidr; a set host bit is an error too.
func parseCIDR(s string) (netip.Addr, int, bool) {
	a, m, slash := strings.Cut(s, "/")
	ip, ok := numericAddr(a, afUnspec)
	if !ok {
		return ip, 0, false
	}
	ip = ip.WithZone("")
	bits := ip.BitLen()
	if slash {
		v, e := strtonum(m, 0, int64(bits))
		if e != "" {
			return ip, 0, false
		}
		bits = int(v)
	}
	if p, _ := ip.Prefix(bits); p.Addr() != ip {
		return ip, 0, false
	}
	return ip, bits, true
}
