//go:build windows

package main

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

// dialogFrame gives every dialog the main window's look: a brand band naming
// what the dialog is for, then its content on the page background. Dialogs
// used to be plain grey system forms, which read as a different application
// once the main window had its sidebar.
func dialogFrame(title string, body ...Widget) []Widget {
	band, text, page := colorBrand, walk.RGB(255, 255, 255), colorPage
	if walk.IsHighContrastEnabled() {
		band, text, page = sysColor(win.COLOR_HIGHLIGHT), sysColor(win.COLOR_HIGHLIGHTTEXT), sysColor(win.COLOR_WINDOW)
	}
	return []Widget{
		Composite{Background: SolidColorBrush{Color: band}, Layout: HBox{Margins: Margins{Left: 16, Top: 10, Right: 16, Bottom: 10}}, Children: []Widget{
			Label{Text: title, TextColor: text, Font: Font{Family: "Segoe UI", PointSize: 13, Bold: true}},
			HSpacer{},
		}},
		Composite{Background: SolidColorBrush{Color: page}, Layout: dialogBody(), Children: body},
	}
}

func dialogLayout() VBox { return VBox{MarginsZero: true, SpacingZero: true} }

// dialogBody keeps checkboxes and buttons at the left edge instead of centered;
// fields and lists still stretch to the dialog's width.
func dialogBody() VBox {
	return VBox{Margins: Margins{Left: 16, Top: 12, Right: 16, Bottom: 12}, Spacing: 8, Alignment: AlignHNearVNear}
}

// runDialog attaches each completed subtree before entering the modal loop.
// Walk assigns native command IDs when a subtree is attached to its form;
// declarative nested composites can otherwise leave descendant buttons without
// a routed command.
func (a *app) runDialog(dialog *walk.Dialog) {
	children := make([]walk.Widget, dialog.Children().Len())
	for i := range children {
		children[i] = dialog.Children().At(i)
	}
	for _, child := range children {
		parent := child.Parent()
		index := parent.Children().Index(child)
		if err := child.SetParent(nil); err != nil {
			showErr(a.mw, "Open dialog", err)
			dialog.Dispose()
			return
		}
		if err := parent.Children().Insert(index, child); err != nil {
			showErr(a.mw, "Open dialog", err)
			dialog.Dispose()
			return
		}
		child.SetVisible(true)
	}
	dialog.Run()
}
