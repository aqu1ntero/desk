desk: a Kanban board where every card is a Claude Code session.

Portable build: desk.exe runs from any folder, nothing to install.

- Claude Code must be installed: the `claude` command on your PATH.
- Put this folder on your PATH too, so the Claude sessions desk starts can run
  `desk peer` (the MSI installer does this for you).
- desk keeps its files in %USERPROFILE%\.config\desk and %USERPROFILE%\.local\share\desk.
- The build is not code-signed: the first time, SmartScreen says "Windows protected
  your PC". Click "More info", then "Run anyway".

Documentation: https://desk.domivega.com/
Source and releases: https://github.com/lfdominguez/desk
License: MIT (LICENSE.txt)
