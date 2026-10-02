//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// onChooseUSBQueue offers Windows' current USB printer queues when the saved
// queue name does not match the one Windows created on this PC.
func (a *app) onChooseUSBQueue() {
	if a.mutationExecuting || a.sheetManifest == nil || a.sheetManifest.Profile.PortType != "usb" {
		return
	}
	a.setHint("Reading this PC's USB printer queues...")
	go func() {
		queues, err := install.ListUSBPrinters(context.Background(), a.env)
		a.mw.Synchronize(func() {
			if a.current != pageReview || a.sheetDone {
				return
			}
			if err != nil {
				showErr(a.mw, "Choose USB queue", err)
				return
			}
			a.showUSBQueueChooser(queues)
		})
	}()
}

func (a *app) showUSBQueueChooser(queues []install.InstalledQueue) {
	var dialog *walk.Dialog
	var list *walk.ListBox
	var chooseButton, cancelButton *walk.PushButton
	labels := []string{"Use the saved queue name automatically"}
	for _, queue := range queues {
		labels = append(labels, fmt.Sprintf("%s  ·  %s  ·  current driver: %s", queue.PrinterName, queue.PortName, queue.DriverName))
	}
	selected := ""
	accepted := false
	choose := func() {
		index := list.CurrentIndex()
		if index < 0 || index >= len(labels) {
			return
		}
		if index > 0 {
			selected = queues[index-1].PrinterName
		}
		accepted = true
		dialog.Accept()
	}
	hintText := "Select the existing Windows USB printer to receive the saved driver. Its port and current driver are shown again in the plan before any change."
	if len(queues) == 0 {
		hintText = "No Windows USB printer queue is connected yet. Keep the automatic choice to prepare the driver now."
	}
	err := (Dialog{
		AssignTo: &dialog, Title: "Choose Windows USB printer",
		MinSize: Size{Width: 650, Height: 350}, Size: Size{Width: 730, Height: 420}, Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &chooseButton, CancelButton: &cancelButton,
		Children: dialogFrame("Choose Windows USB printer",
			Label{Text: hintText},
			ListBox{AssignTo: &list, Model: labels, MinSize: Size{Height: 160}, Accessibility: name("usb-queue-list"), OnItemActivated: choose},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &cancelButton, Text: "Cancel", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &chooseButton, Text: "Use this queue", OnClicked: choose},
			}},
		),
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Choose USB queue", err)
		return
	}
	initial := 0
	for index, queue := range queues {
		if strings.EqualFold(queue.PrinterName, a.pending.USBQueue) {
			initial = index + 1
			break
		}
	}
	list.SetCurrentIndex(initial)
	a.runDialog(dialog)
	if !accepted {
		return
	}
	a.settingOptions = true
	a.offlineCheck.SetChecked(false)
	a.settingOptions = false
	a.pending.USBQueue = selected
	a.pending.Offline = false
	a.invalidateReview()
}
