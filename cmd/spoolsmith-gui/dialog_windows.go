//go:build windows

package main

import "github.com/tailscale/walk"

// runDialog attaches each completed subtree before entering the modal loop.
// Walk assigns native command IDs when a subtree is attached to its form;
// declarative nested composites can otherwise leave descendant buttons without
// a routed command. The main window does the same for its tab subtree.
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
