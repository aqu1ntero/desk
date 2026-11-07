# Changelog

What changed in each desk release. Releases are dated (`2026.11.05`, then `2026.11.05.1` the same
day). A release's section here is its notes on GitHub. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **macOS.** desk runs on macOS 12 and newer, as a universal `desk.app` (Apple Silicon and Intel)
  in each release's `desk-<version>-macos.zip`, and from the flake on Apple Silicon.
  - Image paste (<kbd>⌘</kbd>+<kbd>V</kbd>) reads the system pasteboard.
  - An organization's Claude profile runs with `CLAUDE_CONFIG_DIR` instead of bubblewrap.
  - Local shells use bash 4.4 or newer if it's on `PATH`, else zsh.
  - Opened from Finder or the Dock, desk takes `PATH` from your login shell, so `claude` is found.
  - Files live where they do on Linux (`~/.config/desk`, `~/.local/share/desk`).
  - The app is ad-hoc signed, not with an Apple Developer ID: Gatekeeper asks the first time.
  - The infra workspace's virtual camera stays Linux-only.
- **Windows.** desk runs on Windows 10 and 11 (x64): each release has an installer,
  `desk-<version>-windows-amd64.msi` (Program Files, `PATH`, Start Menu; a newer one replaces the
  old install), and a portable `desk-<version>-windows-amd64.zip`.
  - Terminals run on ConPTY; local shells are PowerShell 7 (`pwsh`), else Windows PowerShell.
  - An organization's Claude profile runs with `CLAUDE_CONFIG_DIR` instead of bubblewrap.
  - Files live where they do on Linux, under your user folder (`.config\desk`, `.local\share\desk`).
  - Neither file is code-signed: SmartScreen asks the first time (More info → Run anyway).
  - The infra workspace's virtual camera stays Linux-only.
- **A card's workspace:** its projects in a tree, an editor, and a read-only Claude that answers
  about the code.
- **desk's own SSH client.** Server panes, file transfers and port forwards no longer run the `ssh`
  program. desk reads the servers file as OpenSSH 10.5 does and connects with its own client, one
  shared connection per server.
- **Text size** for the terminals and the session panel, with zoom keys in every view.

### Changed

- Relative paths in a servers file (`IdentityFile`, `Include`…) resolve from the folder of the file
  that holds them.
- The Ásbrú import drops algorithms desk's SSH client doesn't support.
- Shortcut hints in messages show the keys as symbols (<kbd>⌃⇧K</kbd>, or <kbd>⌘K</kbd> on
  macOS).

### Fixed

- Chat code blocks draw tabs, and code blocks inside list items stay in the item.
- Closing a server pane while another pane of the same server was still connecting broke the
  second pane.
- A broken settings file is kept as it is, instead of being overwritten when you zoom.
- Demo mode: the servers' Files tab and the `changes` tool work.

## [2026.11.05.1] - 2026-11-05

### Added

- The documentation at [desk.domivega.com](https://desk.domivega.com).

### Changed

- **Improve** runs in the folder you picked, and the folder field filters as you type.

## [2026.11.05] - 2026-11-05

### Added

- A documentation site on GitHub Pages, with screenshots made from desk itself.

### Changed

- An organization's files move to `<Org>/.desk/`: `desk.toml`, `ssh_config`, and the infra folder
  (`.desk/infra`).
- Servers come only from the organization's `ssh_config`.

## [2026.11.04] - 2026-11-04

The first release: `.deb`, AppImage, and the AUR package `desk-kanban`.

[Unreleased]: https://github.com/lfdominguez/desk/compare/2026.11.05.1...HEAD
[2026.11.05.1]: https://github.com/lfdominguez/desk/compare/2026.11.05...2026.11.05.1
[2026.11.05]: https://github.com/lfdominguez/desk/compare/2026.11.04...2026.11.05
[2026.11.04]: https://github.com/lfdominguez/desk/releases/tag/2026.11.04
