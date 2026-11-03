package links

// Nagios 3 has no statusjson.cgi: its status is only status.cgi's HTML
// table. ParseStatus3 reads that table's rows into statusjson's terms, so a
// Nagios 3 answers like a Core 4. ponytail: scraped HTML, the stock CGI's
// layout (a themed or patched status.cgi may need its own rules).

import (
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Status3Pages are status.cgi's pages under base (its cgi-bin/): every
// service (style=detail) and every host (style=hostdetail); problems adds
// the filters for services in warning or critical and hosts down or
// unreachable.
func Status3Pages(base string, problems bool) []string {
	services, hosts := base+"status.cgi?host=all&style=detail", base+"status.cgi?hostgroup=all&style=hostdetail"
	if problems {
		services += "&servicestatustypes=20"
		hosts += "&hoststatustypes=12"
	}
	return []string{services, hosts}
}

// Check3 is a host or service row of status.cgi, as statusjson would say it.
type Check3 struct {
	Host, Service        string // Service "": a host
	Status               int    // statusjson's bit: 1 pending; host 2 up, 4 down, 8 unreachable; service 2 ok, 4 warning, 8 unknown, 16 critical
	Output               string
	Changed, Checked     int64 // ms; 0 unknown
	Acked                bool
	Downtime             int // 1 in a scheduled downtime
	Attempt, MaxAttempts int // 0 when the page has no Attempt column (hosts)
}

var (
	states3 = map[string]int{"PENDING": 1, "UP": 2, "OK": 2, "DOWN": 4, "WARNING": 4, "UNREACHABLE": 8, "UNKNOWN": 8, "CRITICAL": 16}
	// "1d 2h 3m 4s", how long the state has lasted
	duration3 = regexp.MustCompile(`^(\d+)d\s*(\d+)h\s*(\d+)m\s*(\d+)s$`)
	attempt3  = regexp.MustCompile(`^(\d+)/(\d+)$`)
	// date_format's layouts: us, euro, iso8601, strict-iso8601
	dates3 = []string{"01-02-2006 15:04:05", "02-01-2006 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05"}
)

// ParseStatus3 reads status.cgi's page r: a row is one whose own cells
// hold a state word (the cell after the host's, or the service's); its host
// and service come from its extinfo.cgi links (a host's later services leave
// its cell empty: the host above), the times from Last Check and Duration
// (as of now), the attempt from Attempt, the output from the last cell, and
// acknowledged or in downtime from the row's icons.
func ParseStatus3(r io.Reader, now time.Time) ([]Check3, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []Check3
	host := ""
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			if c, ok := row3(n, now, host); ok {
				host = c.Host
				out = append(out, c)
				return
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(doc)
	return out, nil
}

func row3(tr *html.Node, now time.Time, above string) (Check3, bool) {
	var cells []*html.Node
	for k := tr.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == html.ElementNode && k.Data == "td" {
			cells = append(cells, k)
		}
	}
	st := -1
	for i, td := range cells {
		if _, ok := states3[text3(td)]; ok && i > 0 {
			st = i
			break
		}
	}
	if st < 0 {
		return Check3{}, false
	}
	c := Check3{Status: states3[text3(cells[st])], Host: above}
	var linkHost string
	visit(tr, func(n *html.Node) {
		switch n.Data {
		case "a":
			href, _, _ := strings.Cut(attr(n, "href"), "#") // the ack icon's link ends in #comments
			i := strings.Index(href, "extinfo.cgi?")
			if i < 0 {
				return
			}
			q, _ := url.ParseQuery(html.UnescapeString(href[i+len("extinfo.cgi?"):]))
			if h := q.Get("host"); h != "" {
				linkHost = h
			}
			if s := q.Get("service"); q.Get("type") == "2" && s != "" {
				c.Service = s
			}
		case "img":
			src := strings.ToLower(attr(n, "src") + " " + attr(n, "title") + " " + attr(n, "alt"))
			c.Acked = c.Acked || strings.Contains(src, "ack.") || strings.Contains(src, "acknowledged")
			if strings.Contains(src, "downtime") {
				c.Downtime = 1
			}
		}
	})
	if linkHost != "" {
		c.Host = linkHost
	}
	if c.Host == "" {
		return Check3{}, false
	}
	rest := cells[st+1:]
	for i, td := range rest {
		t := text3(td)
		if i == len(rest)-1 && i > 0 {
			c.Output = t
			break
		}
		if m := duration3.FindStringSubmatch(t); m != nil {
			d := time.Duration(num(m[1]))*24*time.Hour + time.Duration(num(m[2]))*time.Hour + time.Duration(num(m[3]))*time.Minute + time.Duration(num(m[4]))*time.Second
			c.Changed = now.Add(-d).UnixMilli()
		} else if m := attempt3.FindStringSubmatch(t); m != nil {
			c.Attempt, c.MaxAttempts = num(m[1]), num(m[2])
		} else {
			for _, l := range dates3 {
				if at, err := time.ParseInLocation(l, t, time.Local); err == nil {
					c.Checked = at.UnixMilli()
					break
				}
			}
		}
	}
	return c, true
}

// text3 is n's text, its spaces (and &nbsp;) folded.
func text3(n *html.Node) string {
	var b strings.Builder
	visit(n, func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(strings.ReplaceAll(b.String(), " ", " ")), " ")
}

// visit calls f on n and everything inside it.
func visit(n *html.Node, f func(*html.Node)) {
	f(n)
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		visit(k, f)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func num(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
