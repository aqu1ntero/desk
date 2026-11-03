package links

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

// Alert is a host down or unreachable, or a service in warning or
// critical, on one of an organization's Nagios.
type Alert struct {
	Monitor string // the Nagios's name
	Host    string
	Service string // "" a host alert
	State   string // "critical", "warning", "down", "unreachable"
	Output  string // the check's plugin output
	Since   time.Time
	Acked   bool // acknowledged, or in a scheduled downtime
	URL     string
}

// Critical is an error, not a warning: a service critical, a host down or unreachable.
func (a Alert) Critical() bool { return a.State != "warning" }

// statusjson's state bits (Nagios Core 4 cgi/statusjson.c).
var (
	hostStates    = map[int]string{4: "down", 8: "unreachable"}
	serviceStates = map[int]string{4: "warning", 16: "critical"}
)

// nagiosCheck is a host's or a service's status, as statusjson says it.
type nagiosCheck struct {
	Status   int    `json:"status"`
	Output   string `json:"plugin_output"`
	Changed  int64  `json:"last_state_change"` // ms
	Acked    bool   `json:"problem_has_been_acknowledged"`
	Downtime int    `json:"scheduled_downtime_depth"`
}

// Alerts asks every Nagios of cfg for its problems (statusjson.cgi, which
// Core 4 and XI serve; a Nagios 3's status.cgi pages, nagios3.go), errors first, then the newest; one that fails leaves
// the others' alerts, and its error joined.
func Alerts(ctx context.Context, cfg orgs.Config) ([]Alert, error) {
	var out []Alert
	var errs []error
	for _, n := range cfg.Nagios {
		as, err := nagiosAlerts(ctx, n, cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("Nagios %s: %w", cmp.Or(n.Name, hostOf(n.URL)), err))
		}
		out = append(out, as...)
	}
	slices.SortStableFunc(out, func(a, b Alert) int {
		if a.Critical() != b.Critical() {
			return map[bool]int{true: -1, false: 1}[a.Critical()]
		}
		return b.Since.Compare(a.Since)
	})
	return out, errors.Join(errs...)
}

// NagiosLogin is n's user and password (or bearer token, without a user):
// the user desk.toml names, else its vault item's username.
func NagiosLogin(ctx context.Context, cfg orgs.Config, n orgs.Nagios) (user, pass string, err error) {
	if pass, err = secret(ctx, cfg, n.Secret); err != nil {
		return "", "", err
	}
	user = n.User
	if s := n.Secret; user == "" && s.Vault != "" && s.Field == "" { // the item is the whole login
		s.Field = "username"
		if user, err = secret(ctx, cfg, s); err != nil {
			return "", "", err
		}
	}
	return user, pass, nil
}

// errNoPage is a Nagios's 404: a Nagios 3 has no statusjson.cgi.
var errNoPage = errors.New("no such page")

// nagiosGet GETs u's JSON as user (nagiosFetch).
func nagiosGet(ctx context.Context, u, user, pass string, v any) error {
	b, err := nagiosFetch(ctx, u, user, pass)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// nagiosFetch GETs u as user: basic auth, or the digest Debian's nagios4
// asks for (its challenge answered once); no user, pass is a bearer token.
func nagiosFetch(ctx context.Context, u, user, pass string) ([]byte, error) {
	get := func(authz string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		switch {
		case authz != "":
			req.Header.Set("Authorization", authz)
		case user != "":
			req.SetBasicAuth(user, pass)
		case pass != "":
			req.Header.Set("Authorization", "Bearer "+pass)
		}
		return client.Do(req)
	}
	res, err := get("")
	if err != nil {
		return nil, err
	}
	if ch := res.Header.Get("WWW-Authenticate"); res.StatusCode == http.StatusUnauthorized && user != "" && strings.HasPrefix(ch, "Digest ") {
		res.Body.Close()
		uri := res.Request.URL.RequestURI()
		if res, err = get(DigestAuth(ch, "GET", uri, user, pass, fmt.Sprintf("%016x", rand.Uint64()))); err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %s: %w", res.Request.URL.Host, res.Request.URL.Path, errNoPage)
	}
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return nil, fmt.Errorf("%s: %s %s", res.Request.URL.Host, res.Status, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}

var digestParam = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^\s,]+))`)

// DigestAuth answers an HTTP digest challenge (RFC 2617, MD5, qop=auth or
// none) for method on uri.
func DigestAuth(challenge, method, uri, user, pass, cnonce string) string {
	p := map[string]string{}
	for _, m := range digestParam.FindAllStringSubmatch(challenge, -1) {
		p[m[1]] = m[2] + m[3]
	}
	h := func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }
	ha1, ha2 := h(user+":"+p["realm"]+":"+pass), h(method+":"+uri)
	out := fmt.Sprintf(`Digest username=%q, realm=%q, nonce=%q, uri=%q, algorithm=MD5`, user, p["realm"], p["nonce"], uri)
	if p["opaque"] != "" {
		out += fmt.Sprintf(", opaque=%q", p["opaque"])
	}
	if !slices.Contains(strings.Split(p["qop"], ","), "auth") {
		return out + fmt.Sprintf(", response=%q", h(ha1+":"+p["nonce"]+":"+ha2))
	}
	return out + fmt.Sprintf(`, qop=auth, nc=00000001, cnonce=%q, response=%q`, cnonce, h(ha1+":"+p["nonce"]+":00000001:"+cnonce+":auth:"+ha2))
}

func nagiosAlerts(ctx context.Context, n orgs.Nagios, cfg orgs.Config) ([]Alert, error) {
	if n.URL == "" {
		return nil, nil
	}
	user, pass, err := NagiosLogin(ctx, cfg, n)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(n.URL, "/") + "/cgi-bin/"
	name := cmp.Or(n.Name, hostOf(n.URL))
	var hs struct {
		Data struct {
			List map[string]nagiosCheck `json:"hostlist"`
		} `json:"data"`
	}
	var ss struct {
		Data struct {
			List map[string]map[string]nagiosCheck `json:"servicelist"`
		} `json:"data"`
	}
	err = nagiosGet(ctx, base+"statusjson.cgi?query=hostlist&details=true&hoststatus=down+unreachable", user, pass, &hs)
	switch {
	case errors.Is(err, errNoPage): // a Nagios 3: its status pages instead
		if hs.Data.List, ss.Data.List, err = nagios3(ctx, base, user, pass); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := nagiosGet(ctx, base+"statusjson.cgi?query=servicelist&details=true&servicestatus=warning+critical", user, pass, &ss); err != nil {
			return nil, err
		}
	}
	alert := func(host, svc string, c nagiosCheck, state string) Alert {
		q := url.Values{"type": {"1"}, "host": {host}}
		if svc != "" {
			q = url.Values{"type": {"2"}, "host": {host}, "service": {svc}}
		}
		return Alert{name, host, svc, state, c.Output, time.UnixMilli(c.Changed), c.Acked || c.Downtime > 0, base + "extinfo.cgi?" + q.Encode()}
	}
	var out []Alert
	for h, c := range hs.Data.List {
		if s, ok := hostStates[c.Status]; ok {
			out = append(out, alert(h, "", c, s))
		}
	}
	for h, svcs := range ss.Data.List {
		for s, c := range svcs {
			if st, ok := serviceStates[c.Status]; ok {
				out = append(out, alert(h, s, c, st))
			}
		}
	}
	return out, nil
}

// nagios3 is a Nagios 3's problems (status.cgi's pages filtered to them),
// as statusjson's lists.
func nagios3(ctx context.Context, base, user, pass string) (map[string]nagiosCheck, map[string]map[string]nagiosCheck, error) {
	hosts, svcs := map[string]nagiosCheck{}, map[string]map[string]nagiosCheck{}
	for _, u := range Status3Pages(base, true) {
		b, err := nagiosFetch(ctx, u, user, pass)
		if err != nil {
			return nil, nil, err
		}
		cs, err := ParseStatus3(bytes.NewReader(b), time.Now())
		if err != nil {
			return nil, nil, err
		}
		for _, c := range cs {
			nc := nagiosCheck{Status: c.Status, Output: c.Output, Changed: c.Changed, Acked: c.Acked, Downtime: c.Downtime}
			if c.Service == "" {
				hosts[c.Host] = nc
				continue
			}
			if svcs[c.Host] == nil {
				svcs[c.Host] = map[string]nagiosCheck{}
			}
			svcs[c.Host][c.Service] = nc
		}
	}
	return hosts, svcs, nil
}

func hostOf(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Hostname()
	}
	return s
}
