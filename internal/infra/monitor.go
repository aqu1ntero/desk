package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/links"
)

func (o *orgConfig) source(list []SourceCfg, name, kind string) (SourceCfg, error) {
	for _, s := range list {
		if name == "" || s.Name == name {
			return s, nil
		}
	}
	return SourceCfg{}, fmt.Errorf("no %s source %q", kind, name)
}

// errNoPage is a source's 404 (a Nagios 3 has no statusjson.cgi).
var errNoPage = errors.New("no such page")

// getJSON fetches base+path with the source's credentials into v.
func (o *orgConfig) getJSON(ctx context.Context, s SourceCfg, path string, q url.Values, v any) error {
	body, err := o.get(ctx, s, strings.TrimRight(s.URL, "/")+"/"+path+"?"+q.Encode())
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// get fetches u with the source's credentials.
func (o *orgConfig) get(ctx context.Context, s SourceCfg, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	if err := o.authorize(ctx, s, req); err != nil {
		return nil, err
	}
	client := httpClient(s.Insecure)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// Debian/Ubuntu's nagios4 asks for digest login: answer the challenge once.
	if ch := res.Header.Get("WWW-Authenticate"); res.StatusCode == 401 && s.secret != nil && strings.HasPrefix(ch, "Digest ") {
		res.Body.Close()
		user, pass, err := o.login(ctx, s)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", links.DigestAuth(ch, "GET", req.URL.RequestURI(), user, pass, fmt.Sprintf("%016x", rand.Uint64())))
		if res, err = client.Do(req); err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if res.StatusCode == 404 {
		return nil, fmt.Errorf("%s: %s: %w", s.Name, req.URL.Path, errNoPage)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d: %.300s", s.Name, res.StatusCode, body)
	}
	return body, nil
}

// Nagios 4 status bitmasks (statusjson.cgi).
var (
	hostState = map[int]string{1: "PENDING", 2: "UP", 4: "DOWN", 8: "UNREACHABLE"}
	svcState  = map[int]string{1: "PENDING", 2: "OK", 4: "WARNING", 8: "UNKNOWN", 16: "CRITICAL"}
	severity  = map[string]int{"DOWN": 0, "UNREACHABLE": 1, "CRITICAL": 2, "UNKNOWN": 3, "WARNING": 4, "PENDING": 5, "UP": 6, "OK": 6}
)

type nagiosObj struct {
	Status         int    `json:"status"`
	Output         string `json:"plugin_output"`
	LastChange     int64  `json:"last_state_change"` // ms
	LastCheck      int64  `json:"last_check"`
	Acked          bool   `json:"problem_has_been_acknowledged"`
	Downtime       int    `json:"scheduled_downtime_depth"`
	CurrentAttempt int    `json:"current_attempt"`
	MaxAttempts    int    `json:"max_attempts"`
}

// nagios answers from statusjson.cgi: with no host, every host and service
// that is not OK; with host (a substring), everything about matching hosts,
// narrowed by service (a substring).
func (o *orgConfig) nagios(ctx context.Context, src, host, service string) (string, error) {
	hosts, svcs, err := o.nagiosStatus(ctx, src)
	if err != nil {
		return "", err
	}
	return formatNagios(hosts, svcs, host, service, time.Now()), nil
}

// nagiosStatus fetches every host and service of source src.
func (o *orgConfig) nagiosStatus(ctx context.Context, src string) (map[string]nagiosObj, map[string]map[string]nagiosObj, error) {
	s, err := o.source(o.Nagios, src, "nagios")
	if err != nil {
		return nil, nil, err
	}
	var hosts struct {
		Data struct {
			Hostlist map[string]nagiosObj `json:"hostlist"`
		} `json:"data"`
	}
	var svcs struct {
		Data struct {
			Servicelist map[string]map[string]nagiosObj `json:"servicelist"`
		} `json:"data"`
	}
	err = o.getJSON(ctx, s, "cgi-bin/statusjson.cgi", url.Values{"query": {"hostlist"}, "details": {"true"}}, &hosts)
	if errors.Is(err, errNoPage) { // a Nagios 3: its status pages instead
		return o.nagios3(ctx, s)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := o.getJSON(ctx, s, "cgi-bin/statusjson.cgi", url.Values{"query": {"servicelist"}, "details": {"true"}}, &svcs); err != nil {
		return nil, nil, err
	}
	return hosts.Data.Hostlist, svcs.Data.Servicelist, nil
}

// nagios3 is a Nagios 3's every host and service, read off status.cgi's
// pages (links.ParseStatus3).
func (o *orgConfig) nagios3(ctx context.Context, s SourceCfg) (map[string]nagiosObj, map[string]map[string]nagiosObj, error) {
	hosts, svcs := map[string]nagiosObj{}, map[string]map[string]nagiosObj{}
	for _, u := range links.Status3Pages(strings.TrimRight(s.URL, "/")+"/cgi-bin/", false) {
		body, err := o.get(ctx, s, u)
		if err != nil {
			return nil, nil, err
		}
		cs, err := links.ParseStatus3(bytes.NewReader(body), time.Now())
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		for _, c := range cs {
			ob := nagiosObj{Status: c.Status, Output: c.Output, LastChange: c.Changed, LastCheck: c.Checked, Acked: c.Acked, Downtime: c.Downtime, CurrentAttempt: c.Attempt, MaxAttempts: c.MaxAttempts}
			if c.Service == "" {
				hosts[c.Host] = ob
				continue
			}
			if svcs[c.Host] == nil {
				svcs[c.Host] = map[string]nagiosObj{}
			}
			svcs[c.Host][c.Service] = ob
		}
	}
	return hosts, svcs, nil
}

func formatNagios(hosts map[string]nagiosObj, svcs map[string]map[string]nagiosObj, host, service string, now time.Time) string {
	type row struct {
		sev  int
		line string
	}
	var rows []row
	problems := host == ""
	match := func(name, sub string) bool { return strings.Contains(strings.ToLower(name), strings.ToLower(sub)) }
	line := func(state, name string, o nagiosObj) row {
		flags := ""
		if o.Acked {
			flags += " [acked]"
		}
		if o.Downtime > 0 {
			flags += " [downtime]"
		}
		if o.CurrentAttempt < o.MaxAttempts && state != "OK" && state != "UP" {
			flags += fmt.Sprintf(" [soft %d/%d]", o.CurrentAttempt, o.MaxAttempts)
		}
		since := ""
		if o.LastChange > 0 {
			since = " for " + now.Sub(time.UnixMilli(o.LastChange)).Round(time.Minute).String()
		}
		return row{severity[state], fmt.Sprintf("%-11s %s%s%s — %s", state, name, since, flags, strings.TrimSpace(o.Output))}
	}
	nHosts, nSvcs := 0, 0
	for h, o := range hosts {
		if !problems && !match(h, host) {
			continue
		}
		nHosts++
		st := hostState[o.Status]
		if (problems && st != "UP") || (!problems && service == "") {
			rows = append(rows, line(st, h, o))
		}
	}
	for h, m := range svcs {
		if !problems && !match(h, host) {
			continue
		}
		for name, o := range m {
			if service != "" && !match(name, service) {
				continue
			}
			nSvcs++
			st := svcState[o.Status]
			if !problems || st != "OK" {
				rows = append(rows, line(st, h+" / "+name, o))
			}
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		if a.sev != b.sev {
			return a.sev - b.sev
		}
		return strings.Compare(a.line, b.line)
	})
	head := fmt.Sprintf("%d hosts, %d services checked", nHosts, nSvcs)
	if problems {
		head = fmt.Sprintf("problems among %d hosts and %d services:", nHosts, nSvcs)
		if len(rows) == 0 {
			return head + " none, everything is OK/UP"
		}
	} else if nHosts == 0 {
		var names []string
		for h := range hosts {
			names = append(names, h)
		}
		slices.Sort(names)
		return fmt.Sprintf("no host matches %q; hosts: %s", host, strings.Join(names, ", "))
	}
	out := []string{head}
	for i, r := range rows {
		if i == 300 {
			out = append(out, fmt.Sprintf("… %d more", len(rows)-300))
			break
		}
		out = append(out, r.line)
	}
	return strings.Join(out, "\n")
}

// promTime reads "now", "-1h" (relative), RFC 3339 or unix seconds.
func promTime(s string, now time.Time) (time.Time, error) {
	switch {
	case s == "" || s == "now":
		return now, nil
	case strings.HasPrefix(s, "-"):
		d, err := time.ParseDuration(s)
		return now.Add(d), err
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time %q: use now, -1h, RFC 3339 or unix seconds", s)
	}
	return time.Unix(0, int64(f*1e9)), nil
}

type promSeries struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
	Values [][2]any          `json:"values"`
}

// metrics runs PromQL on Prometheus or VictoriaMetrics (same HTTP API):
// instant at end without start, range otherwise. find lists metric names.
func (o *orgConfig) metrics(ctx context.Context, src, query, start, end, step, find string) (string, error) {
	s, err := o.source(o.Metrics, src, "metrics")
	if err != nil {
		return "", err
	}
	var res struct {
		Status   string          `json:"status"`
		Error    string          `json:"error"`
		Data     json.RawMessage `json:"data"`
		Warnings []string        `json:"warnings"`
	}
	if query == "" {
		if err := o.getJSON(ctx, s, "api/v1/label/__name__/values", url.Values{}, &res); err != nil {
			return "", err
		}
		var names []string
		json.Unmarshal(res.Data, &names)
		var hit []string
		for _, n := range names {
			if strings.Contains(n, find) {
				hit = append(hit, n)
			}
		}
		if len(hit) > 400 {
			return fmt.Sprintf("%d metrics match %q; narrow find. First ones:\n%s", len(hit), find, strings.Join(hit[:400], "\n")), nil
		}
		return fmt.Sprintf("%d metrics match %q:\n%s", len(hit), find, strings.Join(hit, "\n")), nil
	}
	now := time.Now()
	te, err := promTime(end, now)
	if err != nil {
		return "", err
	}
	q := url.Values{"query": {query}, "time": {strconv.FormatInt(te.Unix(), 10)}}
	path := "api/v1/query"
	if start != "" {
		ts, err := promTime(start, now)
		if err != nil {
			return "", err
		}
		if step == "" {
			step = strconv.Itoa(max(1, int(te.Sub(ts).Seconds())/120)) + "s"
		}
		q = url.Values{"query": {query}, "start": {strconv.FormatInt(ts.Unix(), 10)}, "end": {strconv.FormatInt(te.Unix(), 10)}, "step": {step}}
		path = "api/v1/query_range"
	}
	if err := o.getJSON(ctx, s, path, q, &res); err != nil {
		return "", err
	}
	if res.Status != "success" {
		return "", fmt.Errorf("%s: %s", s.Name, res.Error)
	}
	var data struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	json.Unmarshal(res.Data, &data)
	out := formatProm(data.ResultType, data.Result)
	if len(res.Warnings) > 0 {
		out += "\nwarnings: " + strings.Join(res.Warnings, "; ")
	}
	return out, nil
}

func labels(m map[string]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	var ps []string
	name := m["__name__"]
	for _, k := range ks {
		if k != "__name__" {
			ps = append(ps, k+"="+strconv.Quote(m[k]))
		}
	}
	return name + "{" + strings.Join(ps, ", ") + "}"
}

func num(v any) float64 {
	s, _ := v.(string)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// formatProm prints vectors one series per line and ranges as min/avg/max/last
// plus a sample of points, so long ranges stay short.
func formatProm(typ string, raw json.RawMessage) string {
	switch typ {
	case "scalar", "string":
		var v [2]any
		json.Unmarshal(raw, &v)
		return fmt.Sprintf("%s: %v", typ, v[1])
	}
	var ss []promSeries
	json.Unmarshal(raw, &ss)
	if len(ss) == 0 {
		return "no data (empty result)"
	}
	out := []string{fmt.Sprintf("%s, %d series", typ, len(ss))}
	for i, s := range ss {
		if i == 200 {
			out = append(out, fmt.Sprintf("… %d more series", len(ss)-200))
			break
		}
		if typ == "vector" {
			out = append(out, fmt.Sprintf("%s %v", labels(s.Metric), s.Value[1]))
			continue
		}
		mn, mx, sum := math.Inf(1), math.Inf(-1), 0.0
		for _, p := range s.Values {
			f := num(p[1])
			mn, mx, sum = math.Min(mn, f), math.Max(mx, f), sum+f
		}
		n := len(s.Values)
		if n == 0 {
			continue
		}
		last := num(s.Values[n-1][1])
		out = append(out, fmt.Sprintf("%s  points=%d min=%g avg=%g max=%g last=%g", labels(s.Metric), n, mn, sum/float64(n), mx, last))
		var pts []string
		every := max(1, n/20)
		for j := 0; j < n; j += every {
			t, _ := s.Values[j][0].(float64)
			pts = append(pts, time.Unix(int64(t), 0).UTC().Format("15:04")+"="+fmt.Sprint(s.Values[j][1]))
		}
		out = append(out, "  "+strings.Join(pts, " "))
	}
	return strings.Join(out, "\n")
}
