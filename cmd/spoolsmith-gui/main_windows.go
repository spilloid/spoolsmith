//go:build windows

package main

import (
	"log"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

const (
	tabDiscover = iota
	tabSetup
	tabProfiles
	tabReview
	tabTools
)

func pagePadding() VBox {
	return VBox{Margins: Margins{Left: 16, Top: 14, Right: 16, Bottom: 14}, Spacing: 10}
}
func row() HBox                 { return HBox{Spacing: 8, MarginsZero: true} }
func formGrid(columns int) Grid { return Grid{Columns: columns, Spacing: 8} }

// Set an MSAA name for captionless controls so keyboard and UI Automation
// clients can identify them independently of their current values.
func name(id string) Accessibility { return Accessibility{Name: id} }
func heading(text string) Label {
	return Label{Text: text, Font: Font{Family: "Segoe UI", PointSize: 14, Bold: true}}
}

func main() {
	guiApp, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}
	a := newApp()
	defer a.logger.Close()
	a.familyLabels = []string{"Automatic identification"}
	a.familyIDs = []string{""}
	for _, family := range catalog.Families() {
		a.familyLabels = append(a.familyLabels, family.ID+" ("+family.Manufacturer+")")
		a.familyIDs = append(a.familyIDs, family.ID)
	}
	mainWindow := MainWindow{
		AssignTo: &a.mw, Title: "SpoolSmith",
		Font:    Font{Family: "Segoe UI", PointSize: 10},
		MinSize: Size{Width: 820, Height: 620}, Size: Size{Width: 980, Height: 740},
		Layout: VBox{MarginsZero: true, Spacing: 0},
		Children: []Widget{
			Composite{Layout: pagePadding(), Children: []Widget{
				heading("SpoolSmith"),
				Label{Text: "Find your printer. Choose its settings. Review and install."},
			}},
			TabWidget{AssignTo: &a.tabs, Pages: []TabPage{
				discoverPage(a), setupPage(a), profilesPage(a), mutatePage(a), toolsPage(a),
			}},
			Composite{Layout: HBox{Margins: Margins{Left: 16, Top: 6, Right: 16, Bottom: 8}}, Children: []Widget{
				Label{AssignTo: &a.accessStatus, Text: "You can find printers and save settings without changing Windows."},
			}},
		},
	}
	if err := mainWindow.Create(); err != nil {
		log.Fatal(err)
	}
	// Hide Walk's empty native toolbar and attach the completed control tree
	// so every nested tab descendant receives its own native control ID.
	a.mw.ToolBar().SetVisible(false)
	parent := a.tabs.Parent()
	if err := a.tabs.SetParent(nil); err != nil {
		log.Fatal(err)
	}
	if err := a.tabs.SetParent(parent); err != nil {
		log.Fatal(err)
	}
	a.tabs.SetVisible(true)
	a.setReviewMode("install")
	a.useProfileCheck.SetChecked(true)
	a.bindMutationInputs()
	a.initializePrinters()
	a.initializeReview()
	applyStyle(a)
	a.startNetworkDiscovery()
	guiApp.Run()
}

func applyStyle(a *app) {
	monoFont, err := walk.NewFont("Consolas", 10, 0)
	if err != nil {
		return
	}
	for _, out := range []*walk.TextEdit{a.inspectOut, a.catalogOut, a.logOut} {
		out.SetFont(monoFont)
	}
}

func (a *app) showDetails(title, text string) {
	var dialog *walk.Dialog
	var closeButton *walk.PushButton
	err := (Dialog{
		AssignTo: &dialog, Title: title,
		MinSize: Size{Width: 600, Height: 400}, Size: Size{Width: 800, Height: 560}, Layout: pagePadding(),
		DefaultButton: &closeButton, CancelButton: &closeButton,
		Children: []Widget{
			TextEdit{Text: text, ReadOnly: true, VScroll: true, HScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{}, PushButton{AssignTo: &closeButton, Text: "Close", OnClicked: func() { dialog.Accept() }},
			}},
		},
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, title, err)
		return
	}
	dialog.Run()
}

func (a *app) onPlanDetails() {
	if a.previewJSON != "" {
		a.showDetails("Full plan and preflight", a.previewJSON)
	}
}

func discoverPage(a *app) TabPage {
	return TabPage{Title: "Find a printer", Layout: pagePadding(), Children: []Widget{
		heading("1. Find your printer"),
		Label{Text: "Your current network is scanned on launch. Select a printer to continue."},
		Composite{Layout: row(), Children: []Widget{
			Label{Text: "Network or IP:"},
			LineEdit{AssignTo: &a.discoverCIDR, CueBanner: "192.168.1.0/24 or 192.168.1.50", Accessibility: name("discover-cidr")},
			PushButton{AssignTo: &a.discoverBtn, Text: "Scan", OnClicked: a.onDiscover},
			PushButton{AssignTo: &a.discoverCancelBtn, Text: "Cancel scan", Enabled: false, OnClicked: a.onCancelDiscovery},
		}},
		Label{AssignTo: &a.networkStatus, Text: "Looking for your Wi-Fi or Ethernet network..."},
		TextEdit{AssignTo: &a.discoverOut, Text: "Preparing discovery...", ReadOnly: true, VScroll: true,
			MinSize: Size{Height: 62}, MaxSize: Size{Height: 82}, Accessibility: name("discover-output")},
		ListBox{AssignTo: &a.discoverList, MinSize: Size{Height: 100}, Accessibility: name("discover-results"), OnItemActivated: a.onUseDiscovered},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.discoverUseBtn, Text: "Review selected printer", Enabled: false, OnClicked: a.onUseDiscovered},
			PushButton{AssignTo: &a.discoverCustomizeBtn, Text: "Use different settings", Enabled: false, OnClicked: a.onCustomizeDiscovered},
			HSpacer{},
			PushButton{AssignTo: &a.discoverDetailsBtn, Text: "Scan details", Enabled: false, OnClicked: a.onDiscoveryDetails},
		}},
		GroupBox{Title: "Already know the printer's IP address?", Layout: row(), Children: []Widget{
			LineEdit{AssignTo: &a.knownTarget, CueBanner: "Printer IP, for example 192.168.1.50", Accessibility: name("known-ip"),
				OnKeyDown: func(key walk.Key) {
					if key == walk.KeyReturn {
						a.onKnownIP()
					}
				}},
			PushButton{Text: "Continue with IP", OnClicked: a.onKnownIP},
		}},
	}}
}

func setupPage(a *app) TabPage {
	return TabPage{Title: "Add printer", Layout: pagePadding(), Children: []Widget{
		heading("2. Choose printer settings"),
		Label{Text: "Give the printer a name and choose a compatible driver installed on this computer."},
		GroupBox{Title: "Printer", Layout: formGrid(2), Children: []Widget{
			Label{Text: "IP address:"}, LineEdit{AssignTo: &a.captureTarget, CueBanner: "192.168.1.50", Accessibility: name("capture-target")},
			Label{Text: "Printer name:"}, LineEdit{AssignTo: &a.captureName, CueBanner: "Office printer", Accessibility: name("capture-name")},
			Label{Text: "Windows driver:"}, ComboBox{AssignTo: &a.captureDriver, Editable: true, Accessibility: name("capture-driver")},
		}},
		Label{AssignTo: &a.driverStatus, Text: "Drivers will load when you open this screen."},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.refreshDrivers, Text: "Refresh drivers", OnClicked: a.onDrivers}, HSpacer{},
		}},
		GroupBox{Title: "Save settings for next time", Layout: VBox{Spacing: 8}, Children: []Widget{
			Label{Text: "A profile saves this printer's address, name and driver so you can reuse the setup."},
			Composite{Layout: row(), Children: []Widget{
				Label{Text: "Profile file:"},
				LineEdit{AssignTo: &a.captureFile, Accessibility: name("capture-file")},
				PushButton{AssignTo: &a.captureBrowseBtn, Text: "Browse...", OnClicked: a.onBrowseCapture},
			}},
		}},
		Label{AssignTo: &a.captureStatus, Text: "Save and review checks the printer and saves its settings. Installation comes after review."},
		VSpacer{},
		Composite{Layout: row(), Children: []Widget{
			PushButton{Text: "Back to printers", OnClicked: func() { a.tabs.SetCurrentIndex(tabDiscover) }},
			HSpacer{}, PushButton{AssignTo: &a.captureBtn, Text: "Save and review", OnClicked: a.onCaptureProfile},
		}},
	}}
}

func profilesPage(a *app) TabPage {
	return TabPage{Title: "Saved printers", Layout: pagePadding(), Children: []Widget{
		heading("Your saved printers"),
		Label{Text: "Reuse a setup on this computer, update its settings, or remove its Windows printer."},
		Composite{Layout: row(), Children: []Widget{
			Label{Text: "Profile folder:"}, LineEdit{AssignTo: &a.profileDir, Text: "profiles", Accessibility: name("profiles-dir")},
			PushButton{AssignTo: &a.profileBrowseBtn, Text: "Browse...", OnClicked: a.onBrowseProfiles},
			PushButton{AssignTo: &a.refreshBtn, Text: "Refresh", OnClicked: a.onRefreshProfiles},
		}},
		HSplitter{Children: []Widget{
			ListBox{AssignTo: &a.profileList, MinSize: Size{Width: 230, Height: 100}, Accessibility: name("profiles-list"), OnItemActivated: func() { a.useSelectedProfile("install") }},
			TextEdit{AssignTo: &a.profileOut, ReadOnly: true, VScroll: true, MinSize: Size{Width: 280, Height: 100}, Accessibility: name("profiles-output")},
		}},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.profileSetupBtn, Text: "Set up printer", Enabled: false, OnClicked: func() { a.useSelectedProfile("install") }},
			PushButton{AssignTo: &a.profileConfigureBtn, Text: "Update printer", Enabled: false, OnClicked: func() { a.useSelectedProfile("configure") }},
			PushButton{AssignTo: &a.profileRemoveBtn, Text: "Remove printer", Enabled: false, OnClicked: func() { a.useSelectedProfile("remove") }},
			HSpacer{},
			PushButton{AssignTo: &a.loadEditBtn, Text: "Edit settings", Enabled: false, OnClicked: a.onLoadEdit},
			PushButton{AssignTo: &a.profileDetailsBtn, Text: "Profile details", Enabled: false, OnClicked: a.onProfileDetails},
		}},
		GroupBox{AssignTo: &a.profileEditor, Title: "Edit saved settings", Visible: false, Layout: formGrid(4), Children: []Widget{
			Label{AssignTo: &a.editPathLabel, Text: "", ColumnSpan: 4},
			Label{Text: "Printer name:"}, LineEdit{AssignTo: &a.editName, Accessibility: name("edit-name")},
			Label{Text: "Driver name:"}, LineEdit{AssignTo: &a.editDriver, Accessibility: name("edit-driver")},
			Label{Text: "IP address:"}, LineEdit{AssignTo: &a.editTarget, Accessibility: name("edit-target")},
			Label{Text: "Package ID (optional):"}, LineEdit{AssignTo: &a.editPackage, Accessibility: name("edit-package")},
			Label{Text: "Local archive:"}, LineEdit{AssignTo: &a.editArchive, Accessibility: name("edit-archive"), ColumnSpan: 3},
			Label{Text: "Leave both package fields empty to use an installed driver.", ColumnSpan: 4},
			Label{AssignTo: &a.editStatus, Text: "A backup is kept whenever you save changes.", ColumnSpan: 4},
			Composite{ColumnSpan: 4, Layout: row(), Children: []Widget{
				HSpacer{}, PushButton{AssignTo: &a.cancelEditBtn, Text: "Cancel editing", OnClicked: a.onCancelEdit},
				PushButton{AssignTo: &a.saveEditBtn, Text: "Save changes", OnClicked: a.onSaveEdit},
			}},
		}},
	}}
}

func mutatePage(a *app) TabPage {
	return TabPage{Title: "Review and apply", Layout: pagePadding(), Children: []Widget{
		heading("3. Review your changes"),
		Label{AssignTo: &a.reviewHint, Text: "Choose a saved printer or browse for a profile, then preview its setup."},
		Composite{Layout: row(), Children: []Widget{
			RadioButton{AssignTo: &a.modeInstall, Text: "Add printer"},
			RadioButton{AssignTo: &a.modeConfigure, Text: "Update settings"},
			RadioButton{AssignTo: &a.modeUninstall, Text: "Remove printer"},
		}},
		Composite{Layout: row(), Children: []Widget{
			Label{AssignTo: &a.targetLabel, Text: "Profile file:"},
			LineEdit{AssignTo: &a.targetField, CueBanner: "Choose a saved printer or browse for a profile", Accessibility: name("mutate-target")},
			PushButton{AssignTo: &a.reviewBrowseBtn, Text: "Choose profile...", OnClicked: a.onBrowseReview},
		}},
		Composite{Layout: row(), Children: []Widget{
			CheckBox{AssignTo: &a.advancedCheck, Text: "Advanced options", OnCheckedChanged: a.onToggleAdvanced}, HSpacer{},
			PushButton{AssignTo: &a.previewBtn, Text: "Preview changes", OnClicked: a.onPreview},
		}},
		Composite{AssignTo: &a.advancedPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			CheckBox{AssignTo: &a.useProfileCheck, Text: "Use a saved profile"},
			Composite{Layout: row(), Children: []Widget{
				Label{Text: "Printer family:"},
				ComboBox{AssignTo: &a.forceFamilyCombo, Model: a.familyLabels, CurrentIndex: 0, Accessibility: name("mutate-force-family")},
				CheckBox{AssignTo: &a.purgeDriverCheck, Text: "Also remove unused driver"},
			}},
			CheckBox{AssignTo: &a.dryRunOnlyCheck, Text: "Preview only (disable installation)"},
		}},
		TextEdit{AssignTo: &a.planOut, Text: "Your preview will appear here. No changes are made until you review and confirm them.", ReadOnly: true, VScroll: true, Accessibility: name("mutate-output")},
		Composite{Layout: row(), Children: []Widget{
			PushButton{Text: "Saved printers", OnClicked: func() { a.tabs.SetCurrentIndex(tabProfiles) }},
			PushButton{AssignTo: &a.planDetailsBtn, Text: "Full plan / JSON", Enabled: false, OnClicked: a.onPlanDetails},
			HSpacer{}, PushButton{AssignTo: &a.executeBtn, Text: "Add printer...", Enabled: false, OnClicked: a.onExecute},
		}},
	}}
}

func toolsPage(a *app) TabPage {
	return TabPage{Title: "Tools", Layout: pagePadding(), Children: []Widget{
		heading("Printer diagnostics"),
		Label{Text: "Inspect device evidence, explore the driver catalog, or review recent activity."},
		TabWidget{Pages: []TabPage{inspectPage(a), catalogPage(a), logPage(a)}},
	}}
}
func inspectPage(a *app) TabPage {
	return TabPage{Title: "Inspect", Layout: pagePadding(), Children: []Widget{
		Composite{Layout: row(), Children: []Widget{
			Label{Text: "IP or fixture file:"}, LineEdit{AssignTo: &a.inspectTarget, Accessibility: name("inspect-target")},
			PushButton{AssignTo: &a.inspectBtn, Text: "Inspect", OnClicked: a.onInspect},
		}},
		TextEdit{AssignTo: &a.inspectOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("inspect-output")},
	}}
}
func catalogPage(a *app) TabPage {
	return TabPage{Title: "Catalog", Layout: pagePadding(), Children: []Widget{
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.familiesBtn, Text: "List families", OnClicked: a.onFamilies},
			Label{Text: "Printer IP:"}, LineEdit{AssignTo: &a.probeTarget, Accessibility: name("catalog-probe-target")},
			PushButton{AssignTo: &a.probeBtn, Text: "Probe", OnClicked: a.onProbe},
		}},
		TextEdit{AssignTo: &a.catalogOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("catalog-output")},
	}}
}
func logPage(a *app) TabPage {
	return TabPage{Title: "Action log", Layout: pagePadding(), Children: []Widget{
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.refreshLog, Text: "Refresh", OnClicked: a.onRefreshLog},
			PushButton{AssignTo: &a.openLogPath, Text: "Show log file path", OnClicked: a.onOpenLogPath},
		}},
		TextEdit{AssignTo: &a.logOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("log-output")},
	}}
}
