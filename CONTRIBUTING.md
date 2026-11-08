# Contributing to desk

Thanks for wanting to help. Bug reports, ideas and pull requests are all welcome.

desk is a hobby project, built in spare time. There's no support promise: issues and pull requests
are looked at as time allows, and not every idea will land.

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/lfdominguez/desk/issues/new/choose) and pick its form. For a
bug, say which desk (`desk --version`) and which system it runs on, what you did, what you
expected and what happened instead. A screenshot or the error text helps.

Security problems don't go in issues: see [SECURITY.md](SECURITY.md).

## Building and testing

desk is Go with [Gio](https://gioui.org) for the window. The
[Development](https://desk.domivega.com/reference/development/) page has the whole setup. In short:

```sh
nix develop                     # Go, cgo and the Wayland headers; or install them yourself
go build -o desk ./cmd/desk
go test ./...                   # the dev shell sets -tags nox11
go run ./cmd/qa cover -base origin/main   # what CI checks on a pull request
```

Point `XDG_DATA_HOME` and `DESK_ROOT` at scratch folders when you run desk to try a change, never
at your real board, or use `desk demo`, which runs in a fresh temporary world with every provider
faked.

[CLAUDE.md](CLAUDE.md) has the package map and the conventions. It's written for Claude Code,
which builds most of desk, but reads fine for people.

## Pull requests

- Branch from `main` and keep a pull request to one change.
- **Tests.** Add or change tests with the code: plain `testing`, next to the package they test.
  CI fails when under 98% of the lines the pull request changes run in a test, or when a package
  drops under its coverage floor. Its report lands on the pull request as a comment.
- **Mutation testing.** Run `go run ./cmd/qa mutate -file <file> ./internal/<pkg>` on the files
  your tests exercise. Each change it lists is behavior no test pins down: add a test that fails
  on it, or say in the pull request why it behaves the same.
- **Docs.** When the change shows in the docs (`site/src/content/docs/`) or the README (behavior,
  UI text, keys, config), update them in the same pull request.
- **Changelog.** Add user-visible changes under `## [Unreleased]` in
  [CHANGELOG.md](CHANGELOG.md): `### Added`, `### Changed` or `### Fixed`, written for users.
- **Keep examples generic.** The repo is public: no real company, host, email or ticket key in
  code, tests or docs. Use the made-up ones CLAUDE.md lists (Acme, Globex, `*.example.com`,
  192.0.2.0/24, `ABC-123`).
- Providers (GitHub, Jira, GitLab…) are reached from Go with `net/http` or a Go SDK, never through
  an installed CLI.

By contributing, you agree that your work is licensed under the project's [MIT license](LICENSE),
and to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
