package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Limit is one of an account's plan limits: the 5-hour session, the week,
// a model's week.
type Limit struct {
	Name    string // "5h", "7d", "7d Fable"
	Percent float64
	Resets  time.Time
}

// usageURL is Claude Code's own plan usage endpoint (what /usage shows);
// tests point it at a fake.
var usageURL = "https://api.anthropic.com/api/oauth/usage"

// Limits is account's plan usage, read with the OAuth login Claude Code
// keeps in the profile (~/Secure/Claudes/<account>, else ~/.claude). An
// expired token is an error until Claude next runs there and refreshes it.
func Limits(ctx context.Context, home, account string) ([]Limit, error) {
	dir := filepath.Join(home, ".claude")
	if p := filepath.Join(home, "Secure", "Claudes", account); account != "" {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			dir = p
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return nil, err
	}
	var cr struct {
		OAuth struct {
			Token string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &cr) != nil || cr.OAuth.Token == "" {
		return nil, errors.New("no Claude login in " + dir)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+cr.OAuth.Token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage: %s", resp.Status)
	}
	var u struct {
		Limits []struct {
			Kind    string    `json:"kind"`
			Percent float64   `json:"percent"`
			Resets  time.Time `json:"resets_at"`
			Scope   *struct {
				Model *struct {
					Name string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	var out []Limit
	for _, l := range u.Limits {
		name := map[string]string{"session": "5h", "weekly_all": "7d"}[l.Kind]
		if l.Kind == "weekly_scoped" && l.Scope != nil && l.Scope.Model != nil {
			name = "7d " + l.Scope.Model.Name
		}
		if name != "" {
			out = append(out, Limit{Name: name, Percent: l.Percent, Resets: l.Resets})
		}
	}
	return out, nil
}
