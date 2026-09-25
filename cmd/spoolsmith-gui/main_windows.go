//go:build windows

package main

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

func pagePadding() VBox {
	return VBox{Margins: Margins{Left: 20, Top: 14, Right: 20, Bottom: 14}, Spacing: 8}
}
func row() HBox                 { return HBox{Spacing: 6, MarginsZero: true} }
func formGrid(columns int) Grid { return Grid{Columns: columns, Spacing: 6} }

// Set an MSAA name for captionless controls so keyboard and UI Automation
// clients can identify them independently of their current values.
func name(id string) Accessibility { return Accessibility{Name: id} }
func heading(text string) Label {
	return Label{Text: text, Font: Font{Family: "Segoe UI", PointSize: 15, Bold: true}, TextColor: colorBrand}
}

// contentPage is one screen of the main window. Only the current page is
// visible; the sidebar decides which.
func contentPage(a *app, p page, children ...Widget) Composite {
	// Only the first page starts visible: the window sizes itself to its
	// visible content when it is created, and seven stacked pages made it
	// several screens tall.
	return Composite{AssignTo: &a.pages[p], Visible: p == pageThisPC, Background: SolidColorBrush{Color: colorPage}, Layout: pagePadding(), Children: children}
}

func main() {
	request, requestErr := parseLaunchArgs(os.Args[1:])
	// A second double-click hands its file to the window already open. An
	// elevated relaunch never does: it is the window the operator asked for.
	if requestErr == nil && request.Review == nil && forwardToRunningInstance(request.Files) {
		return
	}
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
	tagline := "Printers, ready to carry."
	if isElevated() {
		tagline = "Running as administrator"
	}
	mainWindow := MainWindow{
		AssignTo: &a.mw, Title: "SpoolSmith",
		Background: SolidColorBrush{Color: colorPage},
		Font:       Font{Family: "Segoe UI", PointSize: 10},
		MinSize:    Size{Width: 960, Height: 640}, Size: Size{Width: 1060, Height: 720},
		Layout: VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			Composite{Background: SolidColorBrush{Color: colorBrand}, Layout: VBox{Margins: Margins{Left: 20, Top: 10, Right: 20, Bottom: 10}}, Children: []Widget{
				Composite{Layout: row(), Children: []Widget{
					Label{Text: "SpoolSmith", TextColor: walk.RGB(255, 255, 255), Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}},
					HSpacer{}, Label{Text: tagline, TextColor: walk.RGB(230, 240, 255)},
				}},
			}},
			Composite{AssignTo: &a.offerBar, Visible: false, Background: SolidColorBrush{Color: colorSurface},
				Layout: HBox{Margins: Margins{Left: 20, Top: 6, Right: 20, Bottom: 6}, Spacing: 8}, Children: []Widget{
					Label{Text: "Open printer files (.ssb) with SpoolSmith when you double-click them?"},
					HSpacer{},
					PushButton{Text: "Set up", OnClicked: a.toggleAssociation},
					PushButton{Text: "Not now", OnClicked: func() { rememberAssociationOffer(); a.hideAssociationOffer() }},
				}},
			Composite{Layout: HBox{MarginsZero: true, SpacingZero: true}, Children: []Widget{
				Composite{AssignTo: &a.navHost, Background: SolidColorBrush{Color: colorSidebar},
					MinSize: Size{Width: 190}, MaxSize: Size{Width: 190},
					Layout: VBox{Margins: Margins{Top: 10, Bottom: 10}, Spacing: 6}},
				Composite{Background: SolidColorBrush{Color: colorDivider}, MinSize: Size{Width: 1}, MaxSize: Size{Width: 1}},
				Composite{Background: SolidColorBrush{Color: colorPage}, Layout: VBox{MarginsZero: true, SpacingZero: true}, Children: []Widget{
					thisPCPage(a), addPage(a), sheetPage(a), intunePage(a), inspectPage(a), catalogPage(a), logPage(a),
				}},
			}},
		},
	}
	if err := mainWindow.Create(); err != nil {
		log.Fatal(err)
	}
	// Hide Walk's empty native toolbar.
	a.mw.ToolBar().SetVisible(false)
	if err := a.buildNav(); err != nil {
		log.Fatal(err)
	}
	a.bindMutationInputs()
	a.initializePrinters()
	a.initializeThisPC()
	a.initializeReview()
	applyStyle(a)
	a.enableFileDrop()
	a.listenForHandOver()
	a.goTo(pageThisPC)
	a.nav.SetFocus()
	a.startNetworkDiscovery()
	go cleanClipboardFolders()
	a.mw.Synchronize(func() {
		switch {
		case requestErr != nil:
			showErr(a.mw, "SpoolSmith", requestErr)
		case request.Review != nil:
			a.startOperation(*request.Review)
		case len(request.Files) > 0:
			a.openPrinterFiles(request.Files, "open")
		}
		a.offerAssociation()
	})
	guiApp.Run()
}

// offerAssociation asks once whether .ssb files should open here. Never in an
// elevated session: the question can wait for an ordinary launch.
func (a *app) offerAssociation() {
	if isElevated() || associationOfferAnswered() || os.Getenv("SPOOLSMITH_GUI_NO_OFFER") == "1" {
		return
	}
	if exe, err := os.Executable(); err == nil && currentAssociation(exe) == associationThis {
		return
	}
	setShown(a.offerBar, true)
}

func (a *app) hideAssociationOffer() {
	if a.offerBar != nil {
		setShown(a.offerBar, false)
	}
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
	var closeButton, copyButton *walk.PushButton
	err := (Dialog{
		AssignTo: &dialog, Title: title,
		MinSize: Size{Width: 600, Height: 400}, Size: Size{Width: 800, Height: 560}, Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &closeButton, CancelButton: &closeButton,
		Children: dialogFrame(title,
			TextEdit{Text: text, ReadOnly: true, VScroll: true, HScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &copyButton, Text: "Copy", OnClicked: func() {
					if err := walk.Clipboard().SetText(text); err != nil {
						showErr(dialog, "Copy details", err)
					} else {
						copyButton.SetText("Copied")
					}
				}},
				HSpacer{}, PushButton{AssignTo: &closeButton, Text: "Close", OnClicked: func() { dialog.Accept() }},
			}},
		),
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, title, err)
		return
	}
	a.runDialog(dialog)
}

func (a *app) onPlanDetails() {
	if a.previewJSON != "" {
		a.showDetails("Full plan and result", a.previewJSON)
	}
}

// addPage is the other half of the app's job: putting a printer on this PC.
//
// The network is scanned as soon as the app starts, so this page usually
// opens on a list of printers rather than an empty address box. Typing a
// network or one address is the fallback, behind "Scan a different network
// or IP". Printer files and saved setups open from here too, though a file is
// just as easily dropped or pasted anywhere in the window.
func addPage(a *app) Composite {
	a.discoverModel = &discoveryTableModel{}
	return contentPage(a, pageAdd,
		heading("Add a printer to this PC"),
		hint("Choose a printer found on your network, or open a printer file. Printer files can also be dropped or pasted anywhere in this window."),
		Composite{AssignTo: &a.searchGroup, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			Composite{Layout: row(), Children: []Widget{
				Label{AssignTo: &a.networkStatus, Text: "Finding your network..."},
				HSpacer{},
				PushButton{AssignTo: &a.discoverCancelBtn, Text: "Stop scan", Visible: false, OnClicked: a.onCancelDiscovery},
				PushButton{AssignTo: &a.otherNetworkBtn, Text: "Scan a different network or IP...", OnClicked: func() { a.showOtherNetwork(!a.otherNetworkShown) }},
			}},
			Composite{AssignTo: &a.otherNetwork, Visible: false, Layout: row(), Children: []Widget{
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
			}},
			TableView{
				AssignTo: &a.discoverTable, Model: a.discoverModel, LastColumnStretched: true, NotSortableByHeaderClick: true,
				MinSize: Size{Height: 120}, Accessibility: name("discover-results"),
				Columns: []TableViewColumn{
					{Title: "Printer", Width: 300},
					{Title: "Address", Width: 130},
					{Title: "Saved setup", Width: 120},
				},
				OnSelectedIndexesChanged: func() { a.updateDiscoveryActions() },
				OnItemActivated:          a.onUseDiscovered,
			},
			Label{AssignTo: &a.discoverOut, Text: " ", TextColor: colorHint, EllipsisMode: EllipsisEnd},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &a.discoverUseBtn, Text: "Use this printer", Visible: false, OnClicked: a.onUseDiscovered},
				PushButton{AssignTo: &a.discoverDetailsBtn, Text: "Scan details", Visible: false, OnClicked: a.onDiscoveryDetails},
				HSpacer{},
				PushButton{Text: "Open a printer file...", OnClicked: a.onOpenBundle},
				PushButton{Text: "Open a saved setup...", OnClicked: a.onOpenSavedSetup},
			}},
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
				PushButton{Text: "Use catalog identification instead...", OnClicked: func() {
					a.startOperation(operation{Kind: opInstall, Target: strings.TrimSpace(a.captureTarget.Text())})
				}},
				HSpacer{}, PushButton{AssignTo: &a.captureBtn, Text: "Save and review", OnClicked: a.onCaptureProfile},
			}},
		}},
		VSpacer{},
	)
}

// showOtherNetwork reveals or hides the manual network/IP row.
func (a *app) showOtherNetwork(shown bool) {
	a.otherNetworkShown = shown
	setShown(a.otherNetwork, shown)
	if shown {
		a.otherNetworkBtn.SetText("Hide network or IP")
		a.discoverCIDR.SetFocus()
	} else {
		a.otherNetworkBtn.SetText("Scan a different network or IP...")
	}
}

// intunePage keeps Intune packaging one menu away: it's a job for the
// occasional deployment, not for every visit.
func intunePage(a *app) Composite {
	return contentPage(a, pageIntune,
		heading("Package a printer for Intune"),
		Label{Text: "Turn a saved printer (.ssb) into a Windows app (Win32) for Intune, deployed as Required or offered in Company Portal."},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.intuneBtn, Text: "Build an Intune printer app...", OnClicked: a.onIntuneWizard},
			HSpacer{},
		}},
		// Like every other page, the page ends in a text area that fills it; a
		// page of labels alone is not wide enough to span the window.
		TextEdit{ReadOnly: true, Accessibility: name("intune-about"), Text: lines(intuneAbout)},
	)
}

func inspectPage(a *app) Composite {
	return contentPage(a, pageInspect,
		heading("Inspect a printer or printer file"),
		Label{Text: "See what a printer reports about itself, or what a printer file or set contains. Nothing is installed."},
		Composite{Layout: row(), Children: []Widget{
			Label{Text: "IP, .ssb or .zip:"}, LineEdit{AssignTo: &a.inspectTarget, Accessibility: name("inspect-target")},
			PushButton{AssignTo: &a.inspectBtn, Text: "Inspect", OnClicked: a.onInspect},
		}},
		TextEdit{AssignTo: &a.inspectOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("inspect-output")},
	)
}

func catalogPage(a *app) Composite {
	return contentPage(a, pageCatalog,
		heading("Driver catalog"),
		Label{Text: "List the printer families SpoolSmith can identify, or probe one address to see how it matches."},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.familiesBtn, Text: "List families", OnClicked: a.onFamilies},
			Label{Text: "Printer IP:"}, LineEdit{AssignTo: &a.probeTarget, Accessibility: name("catalog-probe-target")},
			PushButton{AssignTo: &a.probeBtn, Text: "Probe", OnClicked: a.onProbe},
		}},
		TextEdit{AssignTo: &a.catalogOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("catalog-output")},
	)
}

func logPage(a *app) Composite {
	return contentPage(a, pageLog,
		heading("Action log"),
		Label{Text: "Everything SpoolSmith has done on this PC, newest last."},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.refreshLog, Text: "Refresh", OnClicked: a.onRefreshLog},
			PushButton{AssignTo: &a.openLogPath, Text: "Show log file path", OnClicked: a.onOpenLogPath},
		}},
		TextEdit{AssignTo: &a.logOut, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("log-output")},
	)
}

const intuneAbout = `What the package contains
  - install, uninstall and detection scripts, and a README with the exact commands
  - the printer's .ssb and the SpoolSmith CLI you approve, pinned by hash
  - optionally, the .intunewin, made with Microsoft's Win32 Content Prep Tool

What it never does
  - sign in to a tenant, upload, or assign; those stay in the Intune admin center
  - change this PC's printers

The same packaging as the CLI's intune build and intune wizard.`
