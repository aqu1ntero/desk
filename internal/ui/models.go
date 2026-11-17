package ui

import (
	"context"
	"errors"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/vault"
)

// providerEnv is the environment org's Claude for task runs with and the
// provider's name (claude.ProviderEnv), from any goroutine. A desk.toml that
// doesn't read leaves it on the Claude subscription, as before providers.
func (u *ui) providerEnv(org, task string) ([]string, string, error) {
	cfg, err := orgs.Load(u.root, org)
	if err != nil {
		return nil, "", nil
	}
	return claude.ProviderEnv(context.Background(), cfg, task)
}

// modelEnv is providerEnv on the UI goroutine: a provider's token in a
// locked vault asks to unlock it, as a link's does.
func (u *ui) modelEnv(org, task string) ([]string, string, error) {
	env, name, err := u.providerEnv(org, task)
	if le := (*vault.LockedError)(nil); errors.As(err, &le) {
		u.vaultLocked(le)
	}
	return env, name, err
}

// goEnv is providerEnv for another goroutine: the unlock is asked on the UI's.
func (u *ui) goEnv(org, task string) ([]string, error) {
	env, _, err := u.providerEnv(org, task)
	if le := (*vault.LockedError)(nil); errors.As(err, &le) {
		u.later(func() { u.vaultLocked(le) })
	}
	return env, err
}
