//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/profileset"
	"github.com/tailscale/walk"
)

func (a *app) exportSetups(owner walk.Form) {
	picker := walk.FileDialog{Title: "Export all saved setups", Filter: "JSON collections (*.json)|*.json", FilePath: "printer-setups.json"}
	ok, err := picker.ShowSave(owner)
	if err != nil {
		showErr(owner, "Export setups", err)
		return
	}
	if !ok {
		return
	}
	count, err := profileset.Export(a.profilesDirectory(), picker.FilePath)
	a.log("gui", "profile export-all", []string{a.profilesDirectory(), picker.FilePath}, statusOf(err), err, time.Now())
	if err != nil {
		showErr(owner, "Export setups", err)
		return
	}
	walk.MsgBox(owner, "Setups exported", fmt.Sprintf("Saved %d setups to %s.\r\n\r\nIncludes all JSON settings and captured evidence. Driver archives are separate files; copy those too, keeping their paths relative to the setup folder.", count, picker.FilePath), walk.MsgBoxIconInformation)
}

func (a *app) importSetups(owner walk.Form) bool {
	picker := walk.FileDialog{Title: "Import saved setups", Filter: "JSON collections (*.json)|*.json"}
	ok, err := picker.ShowOpen(owner)
	if err != nil {
		showErr(owner, "Import setups", err)
		return false
	}
	if !ok {
		return false
	}
	collection, err := profileset.Load(picker.FilePath)
	if err != nil {
		showErr(owner, "Import setups", err)
		return false
	}
	if walk.MsgBox(owner, "Import saved setups", fmt.Sprintf("Import %d setups into %s?\r\n\r\nExisting files will not be replaced. This saves JSON files only; adding printers to Windows still requires a separate review and confirmation. Driver archives must be copied separately.", len(collection.Profiles), a.profilesDirectory()), walk.MsgBoxYesNo|walk.MsgBoxDefButton2|walk.MsgBoxIconQuestion) != 6 {
		return false
	}
	count, err := profileset.Import(picker.FilePath, a.profilesDirectory())
	a.log("gui", "profile import-all", []string{picker.FilePath, a.profilesDirectory()}, statusOf(err), err, time.Now())
	if err != nil {
		showErr(owner, "Import setups", err)
		return false
	}
	walk.MsgBox(owner, "Setups imported", fmt.Sprintf("Imported %d saved setups. Choose one to review and set up its printer.", count), walk.MsgBoxIconInformation)
	return true
}

func (a *app) checkSavedStatus(owner walk.Form, path string) {
	p, err := install.LoadProfile(path)
	if err != nil {
		showErr(owner, "Check local status", err)
		return
	}
	// A status query does not probe the printer or mutate Windows.
	status, err := install.CheckStatus(context.Background(), a.env, p)
	if err != nil {
		showErr(owner, "Check local status", err)
		return
	}
	text := "This PC matches the saved queue, driver and RAW TCP 9100 settings."
	if !status.Compliant {
		text = "This PC differs from the saved setup:\r\n\r\n" + strings.Join(status.Mismatches, "\r\n")
	}
	walk.MsgBox(owner, "Local status: "+p.PrinterName, text+"\r\n\r\nThis checks local configuration only, not reachability or physical printing.", walk.MsgBoxIconInformation)
}
