# Security policy

desk is a hobby project, built and kept up in spare time. There's no support promise and no
response time: reports are read and fixed as time allows, on a best-effort basis.

## Versions

Fixes go into the next release. Use the
[latest release](https://github.com/lfdominguez/desk/releases/latest) or the `nightly`
pre-release; older releases don't get fixes.

## Reporting a vulnerability

Please don't open a public issue. Report it privately on GitHub: the repository's **Security**
tab, then **Report a vulnerability**
([direct link](https://github.com/lfdominguez/desk/security/advisories/new)).

Say what an attacker can do, and how to reproduce it: the desk version (`desk --version`), the
system, and the steps or a proof of concept. A suggested fix or a pull request is welcome too.

desk handles secrets: vault passwords and keys (Bitwarden, Vaultwarden, Passbolt, HashiCorp),
provider tokens, SSH keys and connections, and the Claude Code sessions it starts. Reports about
any of them are especially welcome.
