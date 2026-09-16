//go:build windows

package main

import (
	"fmt"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"log"
	"net/netip"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// Four tabs, in the order the work actually happens: look at what this PC
// has, add something to it, review the change, and everything else.
//
// The previous five tabs numbered themselves "1." "2." "3." as though they
// were a wizard, while an unnumbered fourth sat between two of them and the
// tab strip let you start anywhere. Finding a printer and choosing its
// settings were two tabs for one continuous task, so the app changed tabs
// under the operator mid-thought.
const (
	tabThisPC = iota
	tabAdd
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
	return Label{Text: text, Font: Font{Family: "Segoe UI", PointSize: 15, Bold: true}, TextColor: walk.RGB(24, 76, 133)}
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
		Background: SolidColorBrush{Color: walk.RGB(245, 247, 251)},
		Font:       Font{Family: "Segoe UI", PointSize: 10},
		MinSize:    Size{Width: 820, Height: 620}, Size: Size{Width: 980, Height: 740},
		Layout: VBox{MarginsZero: true, Spacing: 0},
		Children: []Widget{
			Composite{Background: SolidColorBrush{Color: walk.RGB(24, 76, 133)}, Layout: pagePadding(), Children: []Widget{
				Composite{Layout: row(), Children: []Widget{
					Label{Text: "SpoolSmith", TextColor: walk.RGB(255, 255, 255), Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}},
					HSpacer{}, Label{Text: "Printers, ready to carry.", TextColor: walk.RGB(230, 240, 255)},
				}},
			}},
			TabWidget{AssignTo: &a.tabs, Pages: []TabPage{
				thisPCPage(a), addPage(a), mutatePage(a), toolsPage(a),
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
	tabIndex := parent.Children().Index(a.tabs)
	if err := a.tabs.SetParent(nil); err != nil {
		log.Fatal(err)
	}
	if err := parent.Children().Insert(tabIndex, a.tabs); err != nil {
		log.Fatal(err)
	}
	a.tabs.SetVisible(true)
	a.bindMutationInputs()
	a.initializePrinters()
	a.initializeThisPC()
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
	a.runDialog(dialog)
}

func (a *app) onPlanDetails() {
	if a.previewJSON != "" {
		a.showDetails("Full plan and preflight", a.previewJSON)
	}
}

// addPage merges what used to be "Find a printer" and "Add printer", and adds
// the two file-based ways in.
//
// Discovery and choosing settings were always one task: nobody scans a network
// and then decides not to set anything up. Splitting them across tabs meant the
// app navigated for you at the moment you were concentrating, and the settings
// tab was reachable while empty. Here the settings appear underneath the
// printer you picked, and the page is honest when nothing is picked yet.
func addPage(a *app) TabPage {
	return TabPage{Title: "Add a printer", Background: SolidColorBrush{Color: walk.RGB(250, 251, 253)}, Layout: pagePadding(), Children: []Widget{
		heading("Add a printer to this PC"),
		Label{Text: "Find it on the network, or open a printer file or saved setup."},
		Composite{AssignTo: &a.searchGroup, Layout: VBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
			Composite{Layout: row(), Children: []Widget{
				Label{Text: "Network or IP:"},
				LineEdit{AssignTo: &a.discoverCIDR, CueBanner: "192.168.1.0/24 or 192.168.1.50", Accessibility: name("discover-cidr")},
				PushButton{AssignTo: &a.discoverBtn, Text: "Scan", OnClicked: a.onDiscover},
				PushButton{Text: "Use IP directly", OnClicked: func() {
					target := strings.TrimSpace(a.discoverCIDR.Text())
					ip, err := netip.ParseAddr(target)
					if err != nil || ip.Zone() != "" {
						showErr(a.mw, "Printer address", fmt.Errorf("enter a single printer IP address to continue"))
						return
					}
					if !a.reviewSavedPrinter(ip.String()) {
						a.openPrinterSetup(evidence.Evidence{IP: ip.String()})
					}
				}},
				PushButton{AssignTo: &a.discoverCancelBtn, Text: "Cancel scan", Enabled: false, OnClicked: a.onCancelDiscovery},
			}},
			Label{AssignTo: &a.networkStatus, Text: "Looking for your Wi-Fi or Ethernet network..."},
			ListBox{AssignTo: &a.discoverList, MinSize: Size{Height: 96}, Accessibility: name("discover-results"), OnItemActivated: a.onUseDiscovered},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &a.discoverUseBtn, Text: "Use this printer", Enabled: false, OnClicked: a.onUseDiscovered},
				PushButton{AssignTo: &a.discoverDetailsBtn, Text: "Scan details", Enabled: false, OnClicked: a.onDiscoveryDetails},
				HSpacer{},
				PushButton{Text: "Open a printer file...", OnClicked: a.onOpenBundle},
				PushButton{Text: "Open a saved setup...", OnClicked: a.onOpenSavedSetup},
			}},
			TextEdit{AssignTo: &a.discoverOut, Text: "Preparing discovery...", ReadOnly: true, VScroll: true,
				MinSize: Size{Height: 48}, MaxSize: Size{Height: 64}, Accessibility: name("discover-output")},
		}},
		GroupBox{AssignTo: &a.setupGroup, Title: "Printer settings", Visible: false, Layout: VBox{Spacing: 8}, Children: []Widget{
			Composite{Layout: formGrid(2), Children: []Widget{
				Label{Text: "IP address:"}, LineEdit{AssignTo: &a.captureTarget, CueBanner: "192.168.1.50", Accessibility: name("capture-target")},
				Label{Text: "Printer name:"}, LineEdit{AssignTo: &a.captureName, CueBanner: "Office printer", Accessibility: name("capture-name")},
				Label{Text: "Windows driver:"}, ComboBox{AssignTo: &a.captureDriver, Editable: true, Accessibility: name("capture-driver")},
				Label{Text: "Save settings to:"}, LineEdit{AssignTo: &a.captureFile, Accessibility: name("capture-file")},
			}},
			Composite{Layout: row(), Children: []Widget{
				Label{AssignTo: &a.driverStatus, Text: "Drivers will load when you choose a printer."},
				HSpacer{},
				PushButton{AssignTo: &a.refreshDrivers, Text: "Refresh drivers", OnClicked: a.onDrivers},
				PushButton{AssignTo: &a.captureBrowseBtn, Text: "Browse...", OnClicked: a.onBrowseCapture},
			}},
			Label{AssignTo: &a.captureStatus, Text: "Saving checks the printer and keeps its settings for next time."},
			Composite{Layout: row(), Children: []Widget{
				PushButton{Text: "Back to discovery", OnClicked: func() { a.setupOpen = false; a.setupGroup.SetVisible(false); a.searchGroup.SetVisible(true) }},
				PushButton{Text: "Review catalog setup", OnClicked: func() {
					a.startOperation(operation{Kind: opInstall, Target: strings.TrimSpace(a.captureTarget.Text())})
				}},
				HSpacer{}, PushButton{AssignTo: &a.captureBtn, Text: "Save and review", OnClicked: a.onCaptureProfile},
			}},
		}},
		VSpacer{},
	}}
}

// mutatePage states the one thing about to happen, instead of asking the
// operator to reselect it.
//
// It used to carry three mode radios, a "use a saved profile" checkbox, and one
// text field that meant a profile path, a queue name or an IP address depending
// on which radio was active. All of that restated a decision already made by
// whichever button opened this screen.
func mutatePage(a *app) TabPage {
	return TabPage{Title: "Review and apply", Background: SolidColorBrush{Color: walk.RGB(250, 251, 253)}, Layout: pagePadding(), Children: []Widget{
		heading("Review your change"),
		Label{AssignTo: &a.summaryLabel, Text: "Choose a printer from This PC, or add one, to see its changes here.",
			Font: Font{Family: "Segoe UI", PointSize: 11}, Accessibility: name("review-summary")},
		Label{AssignTo: &a.reviewHint, Text: "Nothing has changed yet."},
		Composite{Layout: row(), Children: []Widget{
			CheckBox{AssignTo: &a.advancedCheck, Text: "More options", OnCheckedChanged: a.onToggleAdvanced},
			HSpacer{},
			PushButton{AssignTo: &a.previewBtn, Text: "Preview changes", OnClicked: a.onPreview},
		}},
		Composite{AssignTo: &a.advancedPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			CheckBox{AssignTo: &a.updateCheck, Text: "Update an existing queue to match this printer file"},
			CheckBox{AssignTo: &a.offlineCheck, Text: "Do not contact the printer (its identity will not be checked)"},
			Composite{AssignTo: &a.familyRow, Layout: row(), Children: []Widget{
				Label{Text: "Printer family:"},
				ComboBox{AssignTo: &a.forceFamilyCombo, Model: a.familyLabels, CurrentIndex: 0, Accessibility: name("mutate-force-family")},
			}},
			Composite{AssignTo: &a.purgeRow, Layout: row(), Children: []Widget{
				CheckBox{AssignTo: &a.purgeDriverCheck, Text: "Also remove the driver, if nothing else uses it"},
				HSpacer{},
			}},
			CheckBox{AssignTo: &a.dryRunOnlyCheck, Text: "Preview only (never apply)"},
		}},
		TextEdit{AssignTo: &a.planOut, Text: "Your preview will appear here. No changes are made until you review and confirm them.", ReadOnly: true, VScroll: true, Accessibility: name("mutate-output")},
		Composite{Layout: row(), Children: []Widget{
			PushButton{Text: "This PC", OnClicked: func() { a.tabs.SetCurrentIndex(tabThisPC) }},
			PushButton{AssignTo: &a.planDetailsBtn, Text: "Full plan / JSON", Enabled: false, OnClicked: a.onPlanDetails},
			HSpacer{}, PushButton{AssignTo: &a.executeBtn, Text: "Apply...", Enabled: false, OnClicked: a.onExecute},
		}},
	}}
}

func toolsPage(a *app) TabPage {
	return TabPage{Title: "Tools", Layout: pagePadding(), Children: []Widget{
		heading("Printer diagnostics"),
		Label{Text: "Inspect device evidence, explore the driver catalog, or review recent activity."},
		TabWidget{Pages: []TabPage{inspectPage(a), catalogPage(a), logPage(a)}},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.intuneBtn, Text: "Build an Intune printer app...", Enabled: false, OnClicked: a.onIntuneWizard},
			Label{Text: "Coming soon — not yet tested against a real Intune tenant."},
			HSpacer{},
		}},
	}}
}
func inspectPage(a *app) TabPage {
	return TabPage{Title: "Inspect", Layout: pagePadding(), Children: []Widget{
		Composite{Layout: row(), Children: []Widget{
			Label{Text: "IP, fixture or .ssb file:"}, LineEdit{AssignTo: &a.inspectTarget, Accessibility: name("inspect-target")},
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
