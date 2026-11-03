package links

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

func TestDigestAuth(t *testing.T) {
	// RFC 2617 section 3.5 example.
	got := DigestAuth(`Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c403ebaf9f0171e9517f40e41"`,
		"GET", "/dir/index.html", "Mufasa", "Circle Of Life", "0a4f113b")
	if !strings.Contains(got, `response="6629fae49393a05397450978507c4ef1"`) || !strings.Contains(got, `opaque="5ccc069c403ebaf9f0171e9517f40e41"`) {
		t.Error(got)
	}
}

// TestAlertsDigest: a nagios4 that asks for digest login gets it.
func TestAlertsDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); !strings.HasPrefix(a, `Digest username="me"`) || !strings.Contains(a, `uri="`+r.URL.RequestURI()+`"`) {
			w.Header().Set("WWW-Authenticate", `Digest realm="Nagios4", qop="auth", nonce="abc"`)
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"data":{"hostlist":{"db1":{"status":4,"plugin_output":"down","last_state_change":1}}}}`))
	}))
	defer srv.Close()
	cfg := orgs.Config{Nagios: []orgs.Nagios{{Name: "n1", URL: srv.URL + "/nagios4", User: "me", Secret: orgs.Secret{Token: "pw"}}}}
	as, err := Alerts(context.Background(), cfg)
	if err != nil || len(as) != 1 || as[0].Host != "db1" {
		t.Fatalf("alerts %+v, %v", as, err)
	}
}

// TestParseStatus3: Nagios 3's status.cgi rows in statusjson's terms: the
// host carried down to its later services, icons as acknowledged and
// downtime, the state's start from Duration, the attempt, the output.
func TestParseStatus3(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 40, 0, 0, time.Local)
	f, _ := os.Open("testdata/status3-services.html")
	defer f.Close()
	cs, err := ParseStatus3(f, now)
	if err != nil || len(cs) != 3 {
		t.Fatalf("services %+v %v", cs, err)
	}
	if c := cs[0]; c.Host != "db1" || c.Service != "MySQL Replication" || c.Status != 16 || !c.Acked || c.Attempt != 3 || c.MaxAttempts != 3 ||
		c.Output != "CRITICAL - Slave_IO_Running: No" || c.Changed != now.Add(-(2*time.Hour+15*time.Minute+4*time.Second)).UnixMilli() ||
		c.Checked != time.Date(2026, 10, 29, 21, 38, 12, 0, time.Local).UnixMilli() {
		t.Fatalf("critical %+v", c)
	}
	if c := cs[1]; c.Host != "db1" || c.Service != "Disk /var" || c.Status != 4 || c.Acked || c.Downtime != 0 {
		t.Fatalf("the host's next service %+v", c)
	}
	if c := cs[2]; c.Host != "web1" || c.Status != 2 || c.Downtime != 1 {
		t.Fatalf("in downtime %+v", c)
	}
	h, _ := os.Open("testdata/status3-hosts.html")
	defer h.Close()
	hs, err := ParseStatus3(h, now)
	if err != nil || len(hs) != 2 || hs[0].Host != "db1" || hs[0].Service != "" || hs[0].Status != 4 || hs[0].Output != "CRITICAL - Host Unreachable (10.0.0.5)" || hs[1].Status != 2 {
		t.Fatalf("hosts %+v %v", hs, err)
	}
}

// TestAlertsNagios3: a Nagios without statusjson.cgi (404) is read from
// its status.cgi pages instead, asked only for the problems.
func TestAlertsNagios3(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/statusjson.cgi"):
			w.WriteHeader(404)
		case r.URL.Query().Get("style") == "detail":
			asked = append(asked, r.URL.Query().Get("servicestatustypes"))
			http.ServeFile(w, r, "testdata/status3-services.html")
		case r.URL.Query().Get("style") == "hostdetail":
			asked = append(asked, r.URL.Query().Get("hoststatustypes"))
			http.ServeFile(w, r, "testdata/status3-hosts.html")
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cfg := orgs.Config{Nagios: []orgs.Nagios{{Name: "mc", URL: srv.URL + "/nagios3/"}}}
	as, err := Alerts(context.Background(), cfg)
	if err != nil || strings.Join(asked, " ") != "20 12" {
		t.Fatalf("alerts %v, asked %q", err, asked)
	}
	var got []string
	for _, a := range as {
		got = append(got, fmt.Sprintf("%s/%s %s %v", a.Host, a.Service, a.State, a.Acked))
	}
	slices.Sort(got)
	if want := "db1/ down false|db1/Disk /var warning false|db1/MySQL Replication critical true"; strings.Join(got, "|") != want {
		t.Fatalf("alerts %q", got)
	}
}

// TestAlerts: a Nagios's down hosts and warning or critical services,
// errors first, through basic auth; one that fails leaves the others'.
func TestAlerts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me" || p != "pw" || r.URL.Path != "/nagios/cgi-bin/statusjson.cgi" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Query().Get("query") {
		case "hostlist":
			w.Write([]byte(`{"data":{"hostlist":{"db1":{"status":4,"plugin_output":"PING CRITICAL - Packet loss = 100%","last_state_change":1700000000000}}}}`))
		case "servicelist":
			w.Write([]byte(`{"data":{"servicelist":{"web1":{
				"Disk /":{"status":4,"plugin_output":"DISK WARNING - 12% free","last_state_change":1700000500000},
				"HTTP":{"status":16,"plugin_output":"Connection refused","last_state_change":1700000900000,"problem_has_been_acknowledged":true},
				"Load":{"status":2,"plugin_output":"OK"}}}}}`))
		}
	}))
	defer srv.Close()
	cfg := orgs.Config{Nagios: []orgs.Nagios{
		{Name: "prod", URL: srv.URL + "/nagios/", User: "me", Secret: orgs.Secret{Token: "pw"}},
		{Name: "lab", URL: srv.URL + "/nagios", User: "me", Secret: orgs.Secret{Token: "wrong"}},
	}}
	as, err := Alerts(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the failing one's error: %v", err)
	}
	if len(as) != 3 {
		t.Fatalf("alerts %+v", as)
	}
	if a := as[0]; a.Service != "HTTP" || a.State != "critical" || !a.Acked || a.Monitor != "prod" || !strings.Contains(a.URL, "extinfo.cgi?host=web1&service=HTTP&type=2") {
		t.Fatalf("first, the newest error: %+v", a)
	}
	if a := as[1]; a.Host != "db1" || a.Service != "" || a.State != "down" || !a.Critical() {
		t.Fatalf("then the host down: %+v", a)
	}
	if a := as[2]; a.Service != "Disk /" || a.State != "warning" || a.Critical() || a.Output != "DISK WARNING - 12% free" {
		t.Fatalf("last, the warning: %+v", a)
	}
}
