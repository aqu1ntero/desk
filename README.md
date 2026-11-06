<div align="center">

<img src="packaging/desk.svg" width="96" alt="desk logo">

# desk

<img src="docs/desk.gif" width="820" alt="desk: a card typed in plain words becomes a prompt, its Claude session works, and the infra Claude fixes a server">

**A Kanban board where every card is a Claude Code session.**<br>
Say what to do; Claude works on it in the right organization, under its own account, and asks you when it needs you.<br>
Pull requests, tickets, CI, alerts and the org's servers sit next to the work. `desk demo` shows it all with nothing to set up.

▶ [The 85-second demo, narrated](docs/desk-demo.mp4) · 📖 [**Documentation**](https://desk.domivega.com/)

[![Build](https://github.com/lfdominguez/desk/actions/workflows/release.yml/badge.svg)](https://github.com/lfdominguez/desk/actions/workflows/release.yml)
[![Release](https://img.shields.io/github/v/release/lfdominguez/desk?sort=date)](https://github.com/lfdominguez/desk/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/github/go-mod/go-version/lfdominguez/desk)
![Platform](https://img.shields.io/badge/platform-Linux%20%C2%B7%20Wayland-informational)
[![Nix](https://img.shields.io/badge/nix-run-5277C3?logo=nixos&logoColor=white)](#nix)
[![Docs](https://img.shields.io/badge/docs-desk.domivega.com-0a84ff)](https://desk.domivega.com/)

[Features](#features) · [Install](#install) · [Getting started](#getting-started) ·
[Guide](#guide) · [Keys](#keys) · [Development](#development)

</div>

---

## Features

| | |
|---|---|
| 🗂️ **Board of sessions** | Backlog, To do, In progress, Blocked, Done. Sessions move their own card: a message sends it to In progress, Claude reporting blocked to Blocked, done to Done. |
| 🏢 **Organizations** | Each org is a folder with its own `desk.toml`: its accounts, its Claude profile (sandboxed with bwrap), its skills, its agents. |
| ✨ **Prompt optimization** | A new card's request is worked out by Haiku in the org's folder into a real brief: context, steps, acceptance criteria, labels, links and the skills to use. |
| 🔗 **Live links** | GitHub, GitLab and Bitbucket PRs/MRs and Jira issues on each card, with their state and CI. Tickets follow their card's column. |
| 🎫 **My tickets, CI, alerts** | Per org: tickets assigned to you, failing pipelines, and Nagios alerts. One click turns any of them into a card. |
| 🖥️ **Infra workspace** | Live SSH panes of the org's servers, from a plain OpenSSH config (or imported from Ásbrú), shared with an infrastructure Claude that asks before every change. |
| 💬 **Rich conversation** | Markdown with highlighted code, diffs of every edit, explained shell commands, subagents and background shells, permission prompts and interviews inline. |
| 🧑‍💻 **Full TUI too** | Any card opens in the real Claude Code TUI, inside desk, on the same session. |
| 🔐 **Vaults** | Tokens from Bitwarden/Vaultwarden, Passbolt, env vars or a command. Unlocked keys stay in memory only. |
| 🪶 **No CLIs needed** | Every provider and vault is spoken to in Go. No `gh`, `glab`, `bw` or `wl-paste`. |

## Screenshots

Taken from `desk demo`: the organizations, tickets and servers are made up.

| | |
|---|---|
| ![The board: every card a Claude Code session, grouped by organization in the sidebar](docs/screenshots/board.png) | ![A new card's request turned into a full prompt by Improve with Claude](docs/screenshots/improve.png) |
| **The board.** Every card is a session; Agent Sessions groups them by organization. | **Improve with Claude.** A one-line request becomes a brief: goal, context, steps, done when. |
| ![A session at work: explained commands, a subagent and the edit as a diff](docs/screenshots/session.png) | ![Claude asking to run git push, with Allow once and Deny](docs/screenshots/permission.png) |
| **The session.** Commands in plain words, subagents, every edit as a diff. | **Your turn.** Permission prompts and answers right in the conversation. |
| ![An organization's Nagios alerts: web-02 critical, db-01 warning](docs/screenshots/alerts.png) | ![The infra workspace: an SSH pane on web-02 shared with the infra Claude, which found the full disk and fixed it after approval](docs/screenshots/servers.png) |
| **Alerts.** Each organization's Nagios alerts, next to the work. | **Servers.** SSH panes shared with an infra Claude that asks before every change. |

## Install

desk runs on **Linux under Wayland** (Hyprland, Sway, KDE, GNOME…); X11 and macOS are on the
[roadmap](#roadmap). It needs
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude`) on `PATH`. Optional:
`bwrap` ([bubblewrap](https://github.com/containers/bubblewrap)) for per-organization Claude
profiles, and `ssh` for the infra workspace.

### Nix

```sh
nix run github:lfdominguez/desk
```

Or add it to a flake:

```nix
inputs.desk.url = "github:lfdominguez/desk";
# …
environment.systemPackages = [ inputs.desk.packages.${pkgs.system}.default ];
```

### AppImage

Download `desk-<version>-x86_64.AppImage` from the [latest release](https://github.com/lfdominguez/desk/releases/latest):

```sh
chmod +x desk-*-x86_64.AppImage
./desk-*-x86_64.AppImage
```

### Debian / Ubuntu

```sh
sudo apt install ./desk_<version>_amd64.deb
```

### Arch Linux (AUR)

```sh
yay -S desk-kanban   # or paru, or: git clone https://aur.archlinux.org/desk-kanban.git && cd desk-kanban && makepkg -si
```

It builds the tagged release from source and installs `desk`. The AUR's `desk` is another program
(a shell workspace manager), so the two conflict. Claude Code itself is the AUR's `claude-code`.

### From source

Go (the version in `go.mod`), cgo, and the Wayland, xkbcommon, EGL and PipeWire headers:

```sh
CGO_CFLAGS_ALLOW=-fno-strict-overflow go build -tags nox11 -o desk ./cmd/desk
```

## Getting started

1. **Lay out your work as organizations.** By default desk looks in `$DESK_ROOT` (`~/Work`), two
   levels down: every subfolder is an organization (`~/Work/Acme`, `~/Work/Globex`…). Change the
   folders and depth in **Settings → General**, along with the languages cards are written in and
   Claude speaks with you in (English unless you change them; each new card can pick its own).
2. **Optionally give an org a Claude account.** A profile in `~/Secure/Claudes/<Org>/` makes that
   org's sessions run under it, sandboxed with bwrap. Others use your own login.
3. **Optionally connect its accounts** in **Settings** (or by hand in `<Org>/.desk/desk.toml`, see
   [Links and organizations](#links-and-organizations)).
4. **Ctrl+N** for a new card: say what to do, pick the organization, **Improve with Claude**,
   **Create card**. The card starts working right away.

Just looking? `desk demo` opens a made-up board (Acme, Globex, Initech) whose Claude sessions,
tickets, CI, alerts and Globex's servers (with their infra Claude) are scripted: no accounts, no
SSH, nothing of yours read or written.

Everything is saved as you go: the board in `~/.local/share/desk/board.json`, conversations in
`~/.local/share/desk/chats/<card>.jsonl`, settings in `~/.config/desk/settings.toml`.

## Guide

### The board

- **Sidebar views**: all cards; sessions by status (working, your turn, blocked, failed); each
  organization and label in use; links (waiting on review, ready to merge, changes requested, open
  tickets); due (overdue, this week); the Done archive. Each shows its count.
- **Header**: the view in words, e.g. "2 working · 4 blocked · 1 overdue".
- **Cards**: key (`DESK-12`), status dot, organization, age; title; the status line with its
  reason ("Blocked by agent: waiting for PR #609"); link chips with their live state ("PR #609 ·
  approved — merge", "ABC-1317 · In Review"); the last message; labels, due date, and the
  session's turns, tokens and cost.
- **Done**: no view shows the Done column; Done cards show in **Done archive**, above the
  archived ones. The box in the toolbar archives every Done card; archived cards stay searchable
  and can be restored from their panel.
- **My tickets**: under each organization with a Jira, GitHub or GitLab account, **Tickets** lists
  the open issues assigned to you there, by project. Click one to open its card, or to make one
  linked to it.
- **CI**: under each organization whose cards link pull or merge requests or commits, **CI** lists
  their GitHub Actions runs and GitLab pipelines; failed and running ones show their jobs. A PR or
  MR chip says its CI too ("approved — merge · CI failed" turns red).
- **Alerts**: under each organization with one or more `[[nagios]]`, **Alerts** lists hosts down
  or unreachable and services in warning or critical, errors first. Its count is the ones nobody
  acknowledged. desk reads Nagios Core's JSON CGI (`statusjson.cgi`, which XI serves too) with
  basic or digest auth, and falls back to a Nagios 3's `status.cgi` pages. An alert's
  **Investigate** makes it a card whose Claude has the infra Claude check the servers;
  **Snooze** (1 hour … 1 week, or your own `2h30m`) hides it until then.
- **Card tools**: every card's Claude has desk's read-only tools (`mcp__desk__*`): `alerts`,
  `tickets`, `link`, `ci` and `ci_log`, through the org's accounts. They add to the org's own
  workflow tools; Claude falls back to them when one of the workflow's fails.
- **Right-click a card** for its menu: open its session or full window, its terminal, edit it,
  copy its key, move it to another column, archive it.
- **Desktop notifications** when a card turns to your turn, blocked, failed or done, unless you're
  looking at it.

### Prompt optimization

The new-card dialog asks only **What to do** (as it comes, over as many lines as you like), the
organization, and **Improve with Claude** (Ctrl+J). The latest Haiku runs as Claude Code in the organization's
folder under its account, reading its CLAUDE.md files and code and changing nothing, and fills the
dialog as it streams: a title, the request worked out into the session's first message (context
found, steps, acceptance criteria, open questions), labels (the board's reused), priority, folder,
links and due date. It sees the skills, plugins, subagents and MCP tools that account's sessions
have, and has the session start with the ones that fit (a ticket → `/team-tools:work-ticket
ABC-510`). Tickets and PRs the request names are read first. The brief it follows is
[`internal/claude/optimizer.md`](internal/claude/optimizer.md).

Look it over, edit anything, then **Create**; **Ask again…** reruns it on what the dialog
holds now. What you wrote is kept: **Use mine** or **Discard suggestions** goes back to it.

- **Screenshots**: Ctrl+V of an image saves it beside the board and puts `![image](path)` in the
  notes; the session reads it. desk reads images from the compositor itself (the Wayland
  data-control protocol: Hyprland, Sway, other wlroots compositors, KDE), nothing to install.
- **Tickets**: when the organization has somewhere to create them (its first `[[jira]]` project,
  else a `[[github]]` account's `repo`), the **Ticket** switch has the card's Claude create its
  ticket before any work, assigned to you, with the Markdown description converted to Atlassian's
  format by [go-adf](https://github.com/nyactl/go-adf).

### Sessions

Click a card (or Enter) to open its panel: the conversation, its links, and a box to message
Claude. Claude runs in `<Org>/<folder>` with `--permission-mode auto`, the organization's own
folder added with `--add-dir` so its `.claude/skills` load. Every Claude view names the account it
runs under.

- **Status lines**: Claude sets the card's state by ending a reply with `DESK-STATUS: blocked —
  <reason>` or `DESK-STATUS: done`, and attaches links with `DESK-LINK: <url>` (desk hides those
  lines).
- **Questions and permissions**: Claude's interviews (AskUserQuestion) and tool permissions show
  as a box at the end of the conversation; the card goes to "your turn" meanwhile.
- **Shell commands**: a message starting with `!` runs it yourself with `bash` in the card's
  folder, no Claude, no approval. Its output streams into the conversation, and when it ends
  Claude gets the command, output, exit code and time and carries on by itself.
- **Agent Sessions**: every card with a session is listed in the sidebar. Its full window shows
  the context meter and the account's plan limits (the 5-hour session, the week, a model's week).
- **Subagents and background shells**: a bar under the header says what runs; **Show details**
  opens a rail with each one (model, task, tools, tokens, time) and their history. A held subagent
  says why: **Waiting on \<its shell\>**, **Running \<tool\>** over 30 s, **No activity** after 2
  minutes.
- **The team view**: a card that runs as an agent shows its session as a team: the lead in the
  middle, the agents it handed work to on a ring around it, and the briefs and results in order.
- **File changes**: every Edit shows as a diff (unified, split or word diff, picked per block),
  over the code's highlighting. A Write shows its size.
- **Shell commands, explained**: Claude describes every command it runs in plain words;
  **Explain** has Haiku take it apart flag by flag.
- **Terminals**: **T** opens the card's session in the full Claude Code TUI inside desk (desk
  lets go of the chat's process first). It keeps running while you look at the board.
- **Artifacts** the session publishes are listed under the card's links.
- **Selecting text**: drag or double-click; Claude's replies select with formatting kept, and
  **Copy** takes a whole reply as Markdown.

### Links and organizations

Add links in the card's sheet (E): GitHub PRs and issues, Bitbucket PRs, GitLab MRs, Jira issues,
or bare Jira keys. Their state refreshes every 3 minutes through the organization's own accounts in
`<Org>/.desk/desk.toml`: any number of each kind. Edit them in **Settings** or by hand (the dialog keeps
keys it doesn't show, but not the file's comments). **Reload** looks for organizations again and
rechecks every link. desk keeps everything of its own in an org's `.desk/` (Claude Code's
`.claude/` and `CLAUDE.md` stay where they are); a `desk.toml` or `ssh_config` still at the org's
root from an older desk moves there by itself, and relative paths in them still start from the
org's folder.

A folder inside an organization can be one of its own: give it a `desk.toml` (e.g.
`~/Work/Acme/billing`). It is named after the folder, or by a top-level `org = "Billing"`, and its
sessions run under the outer org's Claude account.

**Tickets follow their card**: when a card moves column, its tickets you're assigned move to
match. A Jira issue takes the transition to the column's status category (Blocked: a status named
blocked), a GitHub issue closes in Done and reopens out of it. Pull and merge requests are left
alone. At start and every 3 minutes, tickets that drifted are brought back in line.

desk only asks a provider through an account: a link whose organization has no account (with a
token) for its host and owner shows **no GitHub account** (or GitLab, Bitbucket) and is never
fetched, and its CI stays out of the CI view. Anonymous API calls would see no private repos and
run out fast (GitHub allows 60 an hour per address).

A token comes from a password vault item (`vault` + `item`, optionally `field`), an environment
variable (`token_env`) or a command (`token_cmd`). Vaults are Bitwarden, Vaultwarden and
Passbolt; desk asks for the master password the first time a link needs it and keeps it in memory
until it closes.

<details>
<summary><b>A full <code>desk.toml</code></b></summary>

```toml
agent = "planner"               # optional: the Claude Code agent new cards' sessions run as
agents = "meta/.claude/agents"  # where its agent files are, from this folder (default .claude/agents)

[[vault]]
name = "me"
kind = "bitwarden"              # Bitwarden or Vaultwarden
url = "https://vault.example.com"   # default https://vault.bitwarden.com
email = "me@example.com"
client_id = "user.1b2c…"        # optional: the account's API key, which skips 2FA
client_secret = { token_env = "BW_CLIENT_SECRET" }

[[vault]]
name = "globex"
kind = "passbolt"
config_file = "~/.config/passbolt/globex.toml"  # go-passbolt-cli's: server, key, passphrase, TOTP token

[[vault]]                       # or without a CLI config
name = "acme"
kind = "passbolt"
url = "https://passbolt.acme.com"
key_file = "~/.config/passbolt/acme.asc"   # your armored private key
# password = { token_env = "PASSBOLT_PASSPHRASE" }  # else desk asks

[[github]]                      # REST API; needs a token, like every account here
owners = ["globex-org"]
# repo = "globex-org/api"       # where cards create their issues (Ticket switch)
vault = "globex"
item = "GitHub token"

[[jira]]
url = "https://globex.atlassian.net"
email = "me@globex.com"
projects = ["ABC"]              # bare keys like ABC-12 link here; cards create their tickets in the first
# issue_type = "Story"          # what they're created as; default Task, else the first non-subtask
# sync = false                  # cards' column moves don't transition these issues
token_env = "JIRA_GLOBEX_TOKEN"
[jira.status]                   # optional: the status a column moves issues to, by name
Done = "In Review"              # the project's owner closes them after a last review

[[bitbucket]]
workspace = "team"
user = "me"                     # app password; without user, token is a bearer token
token_cmd = "pass bitbucket/team"

[[gitlab]]
url = "https://gitlab.com"
groups = ["globex"]
token_env = "GITLAB_TOKEN"

[[nagios]]                      # as many as the org has: each one's alerts in its Alerts view
name = "prod"                   # what its alerts say they come from (default: its host)
url = "https://mon.globex.com/nagios"   # its web root, where cgi-bin/ is
user = "me"                     # the CGIs' login; the password is the token
vault = "globex"
item = "Nagios prod"

[servers]                       # optional: the org's servers file (below), from the org's folder
file = "infra/ssh_config"       # default .desk/ssh_config
```

</details>

### Servers

Under each organization with a servers file, **Servers** opens its infra workspace: session tabs,
live SSH panes with numbered command blocks, and the org's infrastructure Claude beside them,
which runs commands in those panes and waits for your approve / always / edit / deny on every
change (`[approvals]` in `desk.toml`). **Ctrl+Shift+K** is its palette: runbooks, files, forwards,
notes, reports, the health check, the audit log, recordings, sharing, broadcast.

A card of such an org hands server work to it with `desk peer`; the card shows **⇄ \<org\> infra**,
and the infra Claude's questions and results come into the card's conversation. Each card gets an
infra session of its own (tab **\<org\> · DESK-12**, its own Claude), so two tickets' server work
never mixes; its panes are the servers that one ticket needs. It can also write
to a card unasked or put follow-up work in To do. Claude Code users outside desk get the same
through the bundled plugin ([`plugin/`](plugin)).

An organization's servers are one real OpenSSH config, `<Org>/.desk/ssh_config` (or `[servers] file`).
desk runs `ssh -F <that file> <alias>`, so the same command works from any terminal. desk's own
data are comment lines OpenSSH ignores, `#@ key value`:

<details>
<summary><b>An annotated <code>ssh_config</code></b></summary>

```sshconfig
# Before any "#@ group" or Host: the file's defaults.
#@ vault acme
#@ password "{user}@{host}"

# A section of the tree until the next "#@ group" ("/" nests); its #@ lines
# are the group's defaults (eu/web also takes eu's).
#@ group eu/web
#@ tags web, nginx
Host web1
    HostName 10.0.0.1
    User deploy
    # this host's own: host > group > parent groups > file
    #@ password "web1 root" password
    #@ wol 52:54:00:12:34:56 10.0.0.255:9
    #@ desc the public proxy
    # its name in Nagios (default: the alias and HostName)
    #@ nagios web1.example.com
    #@ pin
Host bastion
    # usable (ProxyJump), not listed
    #@ hidden
Host lab-pi
    # the password itself, no vault: your call (keep the file chmod 600)
    #@ password-raw "s3cret"

# Your own defaults last: ssh keeps the first value it finds.
Match all
    Include ~/.ssh/config
```

- Comments go on their own lines (`#@ desc a # b` keeps `a # b`).
- `password ITEM [FIELD]` fills `{host}`, `{user}` and `{group}` in ITEM and takes it from
  `vault`; `password-raw` is the password itself. The nearest level with either one wins.
- **A changed host key** (a reinstalled server, rotated keys) stops ssh. The pane's box then shows
  the key the server sent next to the one in `known_hosts`, by fingerprint. **Accept new key** (A)
  drops that line and reconnects, and ssh saves the new key; it does nothing if the line no longer
  holds that key for that host.
- **Includes** resolving inside the org folder count; ones outside it (like `~/.ssh/config`) are
  ssh's alone, so your personal hosts never list as the org's servers.
- **Dynamic inventory**: `#@ hosts-cmd PATH` runs at start and on reload and keeps what it prints
  (ssh_config text or `name user@host:port` lines) in `<Org>/.desk/servers-dynamic.conf`.
- Every tab of the org works in `<Org>/.desk/infra/`: its Claude's cwd, with `runbooks/` and its
  `CLAUDE.md` (an older `<Org>/Infra-Claude/` moves there once). The servers file is the only
  source of servers: the old `Infra-Claude/<group>/` folders are no longer read.

</details>

#### Import from Ásbrú

Coming from [Ásbrú Connection Manager](https://www.asbru-cm.net/)? An organization's settings page
has **Import from Ásbrú** under its accounts: pick one of Ásbrú's top-level groups (the one that is
this organization's), or all of it, and its SSH connections are added to the org's servers file,
which is made if it has none. From a shell, the same is:

```sh
desk servers import-asbru -org ~/Work/Acme -group Prod   # add Prod's hosts to Acme's servers file
desk servers import-asbru > ssh_config                   # or just print the file
```

It reads `~/.config/asbru/asbru.yml` (or `$ASBRU_CFG`; give another file, such as an export, as the
last argument). Ásbrú writes that file when it closes cleanly, so close it before importing.

- Groups become `#@ group` paths, relative to the group you picked.
- For each host it brings the address, user (the key's user for a key login), port and key.
- Jump hosts become hidden `Host` blocks behind `ProxyJump`, and a SOCKS proxy a `ProxyCommand`.
- The descriptions become `#@ desc`.
- Its ssh options (`-X`, `-A`, `-C`, `-o Key=Value`, `-L`/`-R`/`-D` forwards, `-p`, `-i`, `-l`, `-J`)
  become their config keywords. Anything else stays as a comment in the host's block.
- Algorithms your `ssh` doesn't support (`ssh -Q`; OpenSSH 10 has no `ssh-dss`) are left out of
  `HostKeyAlgorithms`, `KexAlgorithms`, `Ciphers`… with a comment, since one unknown name makes ssh
  refuse the whole file.
- Passwords stay behind: an ssh_config can't hold them. Point `#@ password` at your vault instead.
- Connections that aren't SSH (RDP, VNC, Telnet…) are skipped, and so are hosts written with Ásbrú's
  variables (`<GV:…>`, `<ENV:…>`). The toast and the shell say which ones.
- A host the file already has (by name) is left as it is, so importing again only adds the new ones.

## Keys

| Do | How |
|---|---|
| Add a card | **Ctrl+N**, **New card**, or a column's **+** |
| Open a card's session | click it, or **Enter** on the selected card |
| Edit its fields | **E** |
| Its terminal | **T** |
| Move a card | drag it, or **Ctrl+arrows** |
| Select | **arrows** |
| Search | **/** or **Ctrl+F**: key, title, notes, org, links, `#labels` (fzf syntax) |
| Improve the request | **Ctrl+J** in the new-card dialog |
| Undo an archive | **Ctrl+Z** |
| Back out | **Esc**: the dialog, the search, a field, the panel, then the selection |

Board keys work on the board only, not while you type in a field or in a dialog. A terminal takes
every key, Esc included, for Claude Code.

## Development

```sh
nix develop                     # Go, cgo and the Wayland headers; or install them yourself
go test ./...                   # the dev shell sets -tags nox11
go run ./cmd/qa cover           # every package's coverage against its floor
go run ./cmd/qa mutate -file internal/ui/smart.go ./internal/ui   # mutation testing
```

**Releases** are dated, not semver (desk is an app, not a library): `2026.11.03`, and another
the same day `2026.11.03.1`, `.2`… Pushing such a tag builds the `.deb` and the AppImage and
publishes them; `desk --version` and the Settings say which one runs (a build between releases says
`dev-<commit>`). Every push to `main` also replaces the
[`nightly` pre-release](https://github.com/lfdominguez/desk/releases/tag/nightly) with that
commit's `.deb` and AppImage.

```sh
git tag 2026.11.03 && git push origin 2026.11.03
```

Point `XDG_DATA_HOME` and `DESK_ROOT` at scratch directories when running checks, never at your
real board. For screenshots and videos, `desk demo` runs in a fresh temp folder (board, settings,
organizations and `HOME` all inside it) with every provider faked, in a window titled `desk demo`. [CLAUDE.md](CLAUDE.md) has the package map and the conventions (it's written for
Claude Code, which builds most of desk, but reads fine for people).

## Vendored dependencies

desk's terminals (card sessions and SSH panes) run on charmbracelet's
[`x/vt`](https://github.com/charmbracelet/x/tree/main/vt) emulator and
[`ultraviolet`](https://github.com/charmbracelet/ultraviolet) cell buffer. Their released versions
copy every cell of the screen on each scrolled line, and drop the bottom of the screen when a pane
shrinks. A busy pane (a build log, `journalctl -f`) then costs seconds of CPU, and resizing a split
hides the prompt. Until upstream releases fixes, [`third_party/`](third_party) holds both modules
as published, with only the changes below, wired in by the `replace` block in `go.mod`. Their MIT
licenses are kept.

| Module | Function | Before | After | Measured | Upstream |
|---|---|---|---|---|---|
| ultraviolet | `Buffer.InsertLineArea` / `DeleteLineArea` | every cell of every line below is copied: O(width × height) per scrolled line | full-width areas rotate the row slices: O(height) pointer moves | one line on a 200×1000 buffer: 2.0 ms → 3 µs; `seq 1 300000` through a pane: 7.0 s → 1.0 s (80 cols), 15.2 s → 1.8 s (200 cols) | not fixed: [issue #214](https://github.com/charmbracelet/ultraviolet/issues/214), open [PR #215](https://github.com/charmbracelet/ultraviolet/pull/215) |
| x/vt | `Scrollback.Push` | a full scrollback drops its oldest line with `slices.Delete`, shifting all 10,000 lines | drops it by reslicing | one line into a full scrollback: 5.3 µs → 1.9 µs | not fixed: open [PR #888](https://github.com/charmbracelet/x/pull/888) (a ring buffer) |
| x/vt | `Emulator.Resize` | a shorter main screen cuts its bottom rows, where the prompt is | the top rows scroll into the scrollback first, as xterm does | behaviour fix | not fixed: [issue #1013](https://github.com/charmbracelet/x/issues/1013), open [PR #1019](https://github.com/charmbracelet/x/pull/1019) |

Each copy goes away once upstream releases its fix. [`third_party/README.md`](third_party/README.md)
has the versions copied.

## Roadmap

Planned, not there yet:

- **X11.** Gio, the UI toolkit, can draw on X11, but desk builds with `-tags nox11`, and pasting
  images reads the Wayland clipboard itself (`internal/clip`).
- **macOS.** Gio runs there too. What's Linux-only today: bubblewrap (per-organization Claude
  profiles), PipeWire (the infra workspace's virtual camera), the Wayland clipboard, and the
  packaging (no `.app` or Homebrew yet).

## Acknowledgements

- [Gio](https://gioui.org) for the GUI, [charmbracelet](https://github.com/charmbracelet)'s
  `x/vt` and `ultraviolet` for the terminals (patched copies in [`third_party/`](third_party)),
  [goldmark](https://github.com/yuin/goldmark) and [chroma](https://github.com/alecthomas/chroma)
  for the conversation, [fzf](https://github.com/junegunn/fzf)'s matching algorithm for every
  search box, [go-adf](https://github.com/nyactl/go-adf) and
  [go-passbolt](https://github.com/passbolt/go-passbolt).
- Fonts: [Manrope](https://github.com/sharanda/manrope) and
  [Geist Mono](https://github.com/vercel/geist-font), under the SIL Open Font License.

## License

[MIT](LICENSE) © Luis Felipe Domínguez Vega
