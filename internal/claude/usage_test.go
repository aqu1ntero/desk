package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestLimits: the profile's login asks, and limits[] names the 5-hour,
// weekly and per-model limits.
func TestLimits(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "Secure", "Claudes", "Globex")
	os.MkdirAll(p, 0o700)
	os.WriteFile(filepath.Join(p, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"tok"}}`), 0o600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"limits":[{"kind":"session","percent":84,"resets_at":"2026-10-28T07:39:59.8+00:00","scope":null},
			{"kind":"weekly_all","percent":24,"resets_at":"2026-11-03T16:59:59+00:00"},
			{"kind":"weekly_scoped","percent":17,"resets_at":null,"scope":{"model":{"display_name":"Fable"}}}]}`))
	}))
	defer srv.Close()
	usageURL = srv.URL
	l, err := Limits(context.Background(), home, "Globex")
	if err != nil || len(l) != 3 || l[0].Name != "5h" || l[0].Percent != 84 || l[1].Name != "7d" || l[2].Name != "7d Fable" || l[0].Resets.IsZero() {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := Limits(context.Background(), home, "Initech"); err == nil {
		t.Fatal("no login should be an error")
	}
}
