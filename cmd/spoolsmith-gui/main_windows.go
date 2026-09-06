//go:build windows

package main

import (
	"log"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/spilloid/spoolsmith/internal/catalog"
)

// pagePadding and row are shared layout presets so every tab gets the same
// breathing room instead of controls sitting flush against the window edge
// and each other (the walk/declarative zero-value VBox/HBox has none).
func pagePadding() VBox { return VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 10} }
func row() HBox         { return HBox{Spacing: 8} }
func formGrid(columns int) Grid {
	return Grid{Columns: columns, Spacing: 8, MarginsZero: true}
}

func main() {
	guiApp, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	a := newApp()
	defer a.logger.Close()

	a.familyLabels = []string{"(automatic)"}
	a.familyIDs = []string{""}
	for _, family := range catalog.Families() {
		a.familyLabels = append(a.familyLabels, family.ID+" ("+family.Manufacturer+")")
		a.familyIDs = append(a.familyIDs, family.ID)
	}

	mainWindow := MainWindow{
		AssignTo: &a.mw,
		Title:    "SpoolSmith",
		MinSize:  Size{Width: 760, Height: 560},
		Size:     Size{Width: 860, Height: 640},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
					discoverPage(a),
					inspectPage(a),
					catalogPage(a),
					profilesPage(a),
					mutatePage(a),
					logPage(a),
				},
			},
		},
	}
	// Create() builds the whole tree (and every AssignTo pointer) without
	// entering the message loop, so defaults that declarative.RadioButton
	// doesn't expose (it has no Checked field) can be set here before Run().
	if err := mainWindow.Create(); err != nil {
		log.Fatal(err)
	}
	a.modeInstall.SetChecked(true)
	applyStyle(a)
	guiApp.Run()
}

// applyStyle replaces the toolkit's default 8pt "MS Shell Dlg 2" (the classic
// dated Win32 look) with Segoe UI everywhere, then gives the read-only JSON/
// plan/log output panes a monospace font for legibility. SetFont on the
// MainWindow cascades to every descendant automatically; the per-pane
// overrides below run after that cascade so they aren't clobbered by it.
func applyStyle(a *app) {
	if uiFont, err := walk.NewFont("Segoe UI", 9, 0); err == nil {
		a.mw.SetFont(uiFont)
	}
	monoFont, err := walk.NewFont("Consolas", 9, 0)
	if err != nil {
		return
	}
	for _, out := range []*walk.TextEdit{a.discoverOut, a.inspectOut, a.catalogOut, a.profileOut, a.planOut, a.logOut} {
		out.SetFont(monoFont)
	}
}

func discoverPage(a *app) TabPage {
	return TabPage{
		Title:  "Discover",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					Label{Text: "IPv4 CIDR (/24 through /32):"},
					LineEdit{AssignTo: &a.discoverCIDR, CueBanner: "192.168.1.0/24"},
					PushButton{AssignTo: &a.discoverBtn, Text: "Scan", OnClicked: a.onDiscover},
				},
			},
			TextEdit{AssignTo: &a.discoverOut, ReadOnly: true, VScroll: true, HScroll: true},
		},
	}
}

func inspectPage(a *app) TabPage {
	return TabPage{
		Title:  "Inspect",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					Label{Text: "Target (IP or fixture file):"},
					LineEdit{AssignTo: &a.inspectTarget},
					PushButton{AssignTo: &a.inspectBtn, Text: "Inspect", OnClicked: a.onInspect},
				},
			},
			TextEdit{AssignTo: &a.inspectOut, ReadOnly: true, VScroll: true, HScroll: true},
		},
	}
}

func catalogPage(a *app) TabPage {
	return TabPage{
		Title:  "Catalog",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					PushButton{AssignTo: &a.familiesBtn, Text: "List families", OnClicked: a.onFamilies},
					Label{Text: "Probe IP:"},
					LineEdit{AssignTo: &a.probeTarget},
					PushButton{AssignTo: &a.probeBtn, Text: "Probe", OnClicked: a.onProbe},
				},
			},
			TextEdit{AssignTo: &a.catalogOut, ReadOnly: true, VScroll: true, HScroll: true},
		},
	}
}

func profilesPage(a *app) TabPage {
	return TabPage{
		Title:  "Profiles",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					Label{Text: "Profiles directory:"},
					LineEdit{AssignTo: &a.profileDir, Text: "profiles"},
					PushButton{AssignTo: &a.refreshBtn, Text: "Refresh", OnClicked: a.onRefreshProfiles},
					PushButton{AssignTo: &a.viewBtn, Text: "View selected", OnClicked: a.onViewProfile},
				},
			},
			HSplitter{
				Children: []Widget{
					ListBox{AssignTo: &a.profileList, MinSize: Size{Width: 200, Height: 0}},
					TextEdit{AssignTo: &a.profileOut, ReadOnly: true, VScroll: true, HScroll: true},
				},
			},
			GroupBox{
				Title:  "Capture new profile",
				Layout: formGrid(4),
				Children: []Widget{
					Label{Text: "Target IP:"}, LineEdit{AssignTo: &a.captureTarget},
					Label{Text: "Queue name:"}, LineEdit{AssignTo: &a.captureName},
					Label{Text: "Save to file:"}, LineEdit{AssignTo: &a.captureFile, Text: "profiles/printer.json"},
					Label{Text: "Installed driver name:"}, LineEdit{AssignTo: &a.captureDriver},
					PushButton{AssignTo: &a.captureBtn, Text: "Capture", OnClicked: a.onCaptureProfile, ColumnSpan: 4},
				},
			},
			GroupBox{
				Title:  "Edit selected profile",
				Layout: formGrid(4),
				Children: []Widget{
					PushButton{AssignTo: &a.loadEditBtn, Text: "Load selected", OnClicked: a.onLoadEdit, ColumnSpan: 4},
					Label{Text: "Queue name:"}, LineEdit{AssignTo: &a.editName},
					Label{Text: "Driver name:"}, LineEdit{AssignTo: &a.editDriver},
					Label{Text: "Target IP:"}, LineEdit{AssignTo: &a.editTarget},
					PushButton{AssignTo: &a.saveEditBtn, Text: "Save changes", OnClicked: a.onSaveEdit, ColumnSpan: 4},
				},
			},
		},
	}
}

func mutatePage(a *app) TabPage {
	return TabPage{
		Title:  "Install / Uninstall",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					RadioButton{AssignTo: &a.modeInstall, Text: "Install / configure"},
					RadioButton{AssignTo: &a.modeUninstall, Text: "Uninstall / remove"},
				},
			},
			Composite{
				Layout: row(),
				Children: []Widget{
					CheckBox{AssignTo: &a.useProfileCheck, Text: "Use profile file (instead of a target/name)"},
				},
			},
			Composite{
				Layout: row(),
				Children: []Widget{
					Label{Text: "Target IP / printer name / profile path:"},
					LineEdit{AssignTo: &a.targetField},
				},
			},
			Composite{
				Layout: row(),
				Children: []Widget{
					Label{Text: "Force family (install only):"},
					ComboBox{AssignTo: &a.forceFamilyCombo, Model: a.familyLabels, CurrentIndex: 0},
					CheckBox{AssignTo: &a.purgeDriverCheck, Text: "Purge driver on uninstall"},
					CheckBox{AssignTo: &a.dryRunOnlyCheck, Text: "Dry run only (never execute)"},
				},
			},
			Composite{
				Layout: row(),
				Children: []Widget{
					PushButton{AssignTo: &a.previewBtn, Text: "Preview plan", OnClicked: a.onPreview},
					PushButton{AssignTo: &a.executeBtn, Text: "Execute (mutate this machine)", Enabled: false, OnClicked: a.onExecute},
				},
			},
			TextEdit{AssignTo: &a.planOut, ReadOnly: true, VScroll: true, HScroll: true},
		},
	}
}

func logPage(a *app) TabPage {
	return TabPage{
		Title:  "Action log",
		Layout: pagePadding(),
		Children: []Widget{
			Composite{
				Layout: row(),
				Children: []Widget{
					PushButton{AssignTo: &a.refreshLog, Text: "Refresh", OnClicked: a.onRefreshLog},
					PushButton{AssignTo: &a.openLogPath, Text: "Show log file path", OnClicked: a.onOpenLogPath},
				},
			},
			TextEdit{AssignTo: &a.logOut, ReadOnly: true, VScroll: true, HScroll: true},
		},
	}
}
