# Patched dependencies

Copies of two charmbracelet modules with performance fixes, wired in by the
`replace` block in go.mod. Tests and testdata are stripped; licenses kept. Drop each copy once upstream releases the fix.

| Module | Version copied | Patch | Effect |
|---|---|---|---|
| github.com/charmbracelet/ultraviolet | v0.0.0-20261008173134-6b8d4baf91b4 | `Buffer.InsertLineArea` / `DeleteLineArea`: for full-width areas, rotate the row slices instead of copying every cell | scrolling no longer costs width × height cell copies per line |
| github.com/charmbracelet/x/vt | v0.1.0 | `Scrollback.Push`: drop the oldest line by reslicing instead of `slices.Delete` | a full scrollback no longer shifts 10k lines per pushed line |
| github.com/charmbracelet/x/vt | v0.1.0 | `Emulator.Resize`: a shorter main screen scrolls its top lines into the scrollback first | shrinking a pane keeps the prompt on screen instead of cutting off the bottom lines |

Measured with `seq 1 300000` (1.9 MB) through `vt.Emulator`: 80 columns
7.0s → 1.0s, 200 columns 15.2s → 1.8s. Both modules' own test suites pass
with the patches.
