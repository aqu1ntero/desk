## What and why

<!-- What changes for users, and why. Link the issue it closes: "Closes #12". -->

## Checklist

- [ ] Tests cover the change, and `go run ./cmd/qa cover -base origin/main` passes
- [ ] `go run ./cmd/qa mutate -file <file> ./internal/<pkg>` on the files the tests exercise: no survivors, or each explained below
- [ ] Docs (`site/src/content/docs/`) and README updated where they describe what changed
- [ ] User-visible changes under `## [Unreleased]` in `CHANGELOG.md`
- [ ] Examples stay generic (Acme, Globex, `*.example.com`): nothing real or private
