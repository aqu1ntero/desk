package infra

import "github.com/lfdominguez/desk/internal/fzf"

// The workspace's searches match as all of desk's do (internal/fzf).
var (
	fzfScore   = fzf.Match
	matchRunes = fzf.Runes
)
