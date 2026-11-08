package ui

import (
	"fmt"
	"os"
	"strings"

	"gioui.org/layout"

	"github.com/lfdominguez/desk/internal/infra"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// asbruFile is Ásbrú Connection Manager's config; tests point it elsewhere.
var asbruFile = infra.AsbruPath

// asbruCurrent says when Ásbrú's file can't be trusted yet: Ásbrú is open
// (a var tests replace).
var asbruCurrent = infra.AsbruCurrent

// cfgServers is an organization page's Servers part, under its accounts:
// where its servers file is, and Import from Ásbrú, which lists Ásbrú's
// top-level groups to take one, or all of them (d.asbru while it is open).
func (u *ui) cfgServers(gtx layout.Context) layout.Dimensions {
	k, d := u.k, u.cfg
	org := d.org
	if d.srvFile == "" { // read once (its desk.toml, the file): not every frame
		file := infra.ServersFile(orgs.Dir(u.root, org, ""))
		d.srvFile = strings.Replace(file, u.home, "~", 1)
		if _, err := os.Stat(file); err != nil {
			d.srvFile = "no servers file yet: an import makes " + d.srvFile
		}
	}
	state := d.srvFile
	btn := func(id, label string, run func()) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			dim, ok := k.RimButton(gtx, id, kit.CPanelCard, kit.CHover, kit.CBtnRim, kit.RCtl, layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10},
				k.Lbl(label, 12, kit.CText, kit.Semi(kit.Sans)))
			if ok {
				d.act = run
			}
			return dim
		}
	}
	var row []layout.Widget
	if d.asbru == nil {
		row = append(row, btn("cfgAsbru", "Import from Ásbrú", func() { u.openAsbru() }))
	} else {
		row = append(row, btn("cfgAsbruAll", "All of Ásbrú", func() { u.importAsbru(org, "") }))
		for _, g := range d.asbru {
			row = append(row, btn("cfgAsbru:"+g, g, func() { u.importAsbru(org, g) }))
		}
		row = append(row, btn("cfgAsbruNo", "Cancel", func() { d.asbru = nil }))
	}
	hint := d.asbruHint()
	login := func(gtx layout.Context) layout.Dimensions { // clear_login = false
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				dim, ok := k.Switch(gtx, "cfgKeepLogin", d.opts.on["keep_login"])
				if ok {
					d.act = func() { d.opts.on["keep_login"] = !d.opts.on["keep_login"] }
				}
				return dim
			}),
			layout.Rigid(kit.HGap(10)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(k.Lbl("Show the server's login message", 13, kit.CText, kit.Sans)),
					layout.Rigid(k.Lbl("Its banner and message of the day stay above a pane's first prompt, as plain text; off, the screen is cleared once desk's shell integration is in.", 11, kit.CText3, kit.Sans)))
			}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(kit.VGap(18)),
		layout.Rigid(k.Caption("Servers")),
		layout.Rigid(kit.VGap(8)),
		layout.Rigid(login),
		layout.Rigid(kit.VGap(12)),
		layout.Rigid(kit.VGap(4)),
		layout.Rigid(k.Lbl(state, 12, kit.CText2, kit.Mono)),
		layout.Rigid(kit.VGap(4)),
		layout.Rigid(k.Lbl(hint, 12, kit.CText3, kit.Sans)),
		layout.Rigid(kit.VGap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return kit.Wrap(gtx, 6, row) }))
}

// asbruHint is the line under the Servers part: what an import brings, or,
// with the picker open, what to pick.
func (d *cfgDialog) asbruHint() string {
	if d.asbru != nil {
		return "Which of Ásbrú's groups are " + d.org + "'s? Its hosts are added; those already in the file are kept."
	}
	return "SSH connections from Ásbrú Connection Manager: its groups, users, ports, keys and jump hosts (no passwords)."
}

// openAsbru reads Ásbrú's groups for the page's picker.
func (u *ui) openAsbru() {
	if err := asbruCurrent(asbruFile()); err != nil {
		u.showToast(err.Error(), true)
		return
	}
	data, err := os.ReadFile(asbruFile())
	if err != nil {
		u.showToast("No Ásbrú connections: "+err.Error(), true)
		return
	}
	gs, err := infra.AsbruGroups(data)
	if err != nil {
		u.showToast(err.Error(), true)
		return
	}
	u.cfg.asbru = append([]string{}, gs...) // not nil: the picker is open, even with no groups
}

// importAsbru adds Ásbrú's SSH hosts (group's, "" all) to org's servers
// file, then has the infra workspace read it again.
func (u *ui) importAsbru(org, group string) {
	u.cfg.asbru = nil
	file, added, skipped, err := infra.ImportAsbru(orgs.Dir(u.root, org, ""), asbruFile(), group)
	switch {
	case err != nil:
		u.showToast("Ásbrú import: "+err.Error(), true)
		return
	case len(added) == 0:
		u.showToast(fmt.Sprintf("Nothing to import from Ásbrú (%d skipped)", len(skipped)), true)
		return
	}
	u.cfg.srvFile = "" // it may exist now
	if u.infra != nil {
		u.infra.Reload()
	}
	msg := fmt.Sprintf("Added %d servers to %s", len(added), strings.Replace(file, u.home, "~", 1))
	if len(skipped) > 0 {
		msg += fmt.Sprintf(" (%d skipped: %s)", len(skipped), strings.Join(skipped[:min(3, len(skipped))], "; "))
	}
	u.showToast(msg, false)
}
