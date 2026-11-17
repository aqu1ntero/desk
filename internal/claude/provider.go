package claude

import (
	"context"
	"fmt"

	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/vault"
)

// ProviderEnv is what a desk Claude of an organization runs with for task
// (orgs.Tasks): nothing on the Claude subscription, else its provider's
// endpoint and token, which Claude Code takes over its login, and the models
// standing in for Opus, Sonnet and Haiku, so every alias desk and the roles
// use (--model haiku, model: opus) lands on one of them. name is the
// provider's, "" none. A locked vault comes back as its *vault.LockedError.
// ponytail: a token_cmd runs on every start (vault items are cached); cache
// it too if a slow one shows.
func ProviderEnv(ctx context.Context, cfg orgs.Config, task string) (env []string, name string, err error) {
	p, err := cfg.ProviderFor(task)
	if p == nil || err != nil {
		return nil, "", err
	}
	tok, err := vault.Resolve(ctx, cfg.Vaults, p.Secret)
	if err != nil {
		return nil, p.Name, fmt.Errorf("%s's token: %w", p.Name, err)
	}
	env = []string{"ANTHROPIC_BASE_URL=" + p.BaseURL, "ANTHROPIC_AUTH_TOKEN=" + tok, "ANTHROPIC_API_KEY="}
	for _, m := range [][2]string{{"OPUS", p.Opus}, {"SONNET", p.Sonnet}, {"HAIKU", p.Haiku}} {
		if m[1] != "" {
			env = append(env, "ANTHROPIC_DEFAULT_"+m[0]+"_MODEL="+m[1])
		}
	}
	return env, p.Name, nil
}
