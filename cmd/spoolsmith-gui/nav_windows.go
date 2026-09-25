//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

// The sidebar holds the app's one job and nothing else: This PC (take
// printers off it) and Add a printer (put them on). Everything used now and
// then -- Intune packaging, inspecting, the catalog, the log, saved setups --
// lives under More at the foot of the sidebar.
//
// It used to list seven places at equal weight, including a Review page that
// could be opened with nothing to review. Review is now the apply sheet,
// which opens only when there is something to apply.
type page int

const (
	pageThisPC page = iota
	pageAdd
	pageReview
	pageIntune
	pageInspect
	pageCatalog
	pageLog
	pageCount
)

// navPageCount is how many pages the sidebar lists; the rest are reached
// from More or, for the sheet, by opening something to apply.
const navPageCount = 2

// Titles double as the sidebar items' and More menu items' UI Automation
// names, which the desktop test suite navigates by.
var pageTitles = [pageCount]string{
	"This PC",
	"Add a printer",
	"Review",
	"Intune package",
	"Inspect",
	"Driver catalog",
	"Action log",
}

var (
	colorBrand       = walk.RGB(24, 76, 133)
	colorPage        = walk.RGB(250, 251, 253)
	colorSidebar     = walk.RGB(255, 255, 255)
	colorSurface     = walk.RGB(240, 244, 250)
	colorNavSelected = walk.RGB(229, 238, 250)
	colorNavText     = walk.RGB(44, 52, 64)
	colorDivider     = walk.RGB(222, 227, 234)
	colorHint        = walk.RGB(88, 98, 112) // 5.9:1 on colorPage
	colorWarning     = walk.RGB(138, 76, 0)  // 6.3:1 on colorPage
)

type navUI struct {
	navHost  *walk.Composite
	nav      *walk.ListBox
	moreBtn  *walk.PushButton
	offerBar *walk.Composite
	pages    [pageCount]*walk.Composite
	current  page
}

// buildNav creates the sidebar list once the main window exists.
//
// It is created here rather than declared because the declarative ListBox
// makes an owner-drawn list without LBS_HASSTRINGS, and then the list keeps no
// text for its items: screen readers and UI Automation would see unnamed
// entries. With LBS_HASSTRINGS the native list keeps each title, so keyboard,
// Narrator and automation all get "This PC" and "Add a printer".
func (a *app) buildNav() error {
	nav, err := walk.NewListBoxWithStyle(a.navHost, win.LBS_OWNERDRAWVARIABLE|win.LBS_HASSTRINGS)
	if err != nil {
		return err
	}
	// Drop the sunken border and the horizontal scroll bar the plain list
	// style always adds; the sidebar is edged by the content beside it.
	hwnd := nav.Handle()
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style&^(win.WS_BORDER|win.WS_HSCROLL)))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
	nav.Accessibility().SetName("navigation")
	styler := &navStyler{list: nav}
	if styler.bold, err = walk.NewFont("Segoe UI", 10, walk.FontBold); err != nil {
		return err
	}
	nav.SetItemStyler(styler)
	if err := nav.SetModel(pageTitles[:navPageCount]); err != nil {
		return err
	}
	styler.measure()
	a.nav = nav
	a.current = -1
	nav.CurrentIndexChanged().Attach(func() {
		// walk ignores the list's selection-only repaints (it draws only on
		// ODA_DRAWENTIRE), so repaint the whole list to move the highlight.
		nav.Invalidate()
		if i := nav.CurrentIndex(); i >= 0 {
			a.showPage(page(i))
		}
	})
	nav.FocusedChanged().Attach(func() { nav.Invalidate() })

	more, err := walk.NewPushButton(a.navHost)
	if err != nil {
		return err
	}
	more.SetText("More  ▾")
	more.Clicked().Attach(a.showMoreMenu)
	a.moreBtn = more
	return nil
}

// goTo navigates the way a click in the sidebar would.
func (a *app) goTo(p page) {
	if p < navPageCount {
		if a.nav.CurrentIndex() != int(p) {
			a.nav.SetCurrentIndex(int(p)) // publishes CurrentIndexChanged -> showPage
			return
		}
	} else if a.nav.CurrentIndex() != -1 {
		a.nav.SetCurrentIndex(-1)
		a.nav.Invalidate()
	}
	a.showPage(p)
}

func (a *app) showPage(p page) {
	if p == a.current {
		return
	}
	a.current = p
	// walk's SetVisible skips the call when IsWindowVisible already agrees, and
	// before the main window is first shown every page reports invisible, so
	// the first switch would hide nothing and all pages would stack.
	for i, content := range a.pages {
		setShown(content, page(i) == p)
	}
	// A button that navigated (the sheet's Back) may now be hidden while
	// still holding focus, where Enter would press it unseen.
	if focus := win.GetFocus(); focus != 0 && !win.IsWindowVisible(focus) {
		a.nav.SetFocus()
	}
	a.pageShown(p)
}

// setShown shows or hides a whole panel regardless of what walk believes its
// visibility already is, then lays the window out again.
func setShown(w walk.Widget, shown bool) {
	cmd := int32(win.SW_HIDE)
	if shown {
		cmd = win.SW_SHOWNA
	}
	win.ShowWindow(w.Handle(), cmd)
	// Lay out the whole form, not just the parent: which panel is showing
	// changes how much space the panels above it want. Relaying out only the
	// parent left the old distribution in place, as grey gutters around the
	// sidebar that shifted from page to page.
	if form := w.Form(); form != nil {
		form.AsFormBase().RequestLayout()
	}
}

// pageShown re-applies each optional panel's state once its page is visible.
//
// walk's SetVisible compares against IsWindowVisible, which includes every
// ancestor. While a page is hidden its panels already report themselves
// invisible, so hiding one there is a no-op and it would reappear, empty, the
// first time the page is opened. Only after the page is shown does applying
// the state take effect.
func (a *app) pageShown(p page) {
	switch p {
	case pageThisPC:
		a.updateQueueActions()
	case pageAdd:
		a.setupGroup.SetVisible(a.setupOpen)
		a.searchGroup.SetVisible(!a.setupOpen)
		setShown(a.otherNetwork, a.otherNetworkShown)
		a.updateDiscoveryActions()
		if a.setupOpen && !a.driversLoaded {
			a.onDrivers()
		}
	case pageReview:
		a.advancedPanel.SetVisible(a.advancedCheck.Checked())
		a.detailsPanel.SetVisible(a.detailsCheck.Checked())
		a.renderSheet()
	}
}

// More menu command IDs.
const (
	moreIntune = iota + 1
	moreInspect
	moreCatalog
	moreLog
	moreSavedSetups
	moreAssociation
	moreAbout
)

// showMoreMenu drops the occasional destinations down from the More button.
// It is a native popup menu, so keyboard, Narrator and UI Automation read it
// like any other Windows menu.
func (a *app) showMoreMenu() {
	menu := win.CreatePopupMenu()
	if menu == 0 {
		return
	}
	defer win.DestroyMenu(menu)
	exe, _ := os.Executable()
	associated := exe != "" && currentAssociation(exe) == associationThis
	position := uint32(0)
	add := func(id int, text string, checked bool) {
		info := win.MENUITEMINFO{FMask: win.MIIM_ID | win.MIIM_STRING | win.MIIM_FTYPE | win.MIIM_STATE, FType: win.MFT_STRING, WID: uint32(id)}
		if id == 0 {
			info.FMask, info.FType = win.MIIM_FTYPE, win.MFT_SEPARATOR
		} else {
			text16, _ := windows.UTF16FromString(text)
			info.DwTypeData = &text16[0]
			info.Cch = uint32(len(text16) - 1)
		}
		if checked {
			info.FState = win.MFS_CHECKED
		}
		info.CbSize = uint32(unsafe.Sizeof(info))
		win.InsertMenuItem(menu, position, true, &info)
		position++
	}
	add(moreIntune, pageTitles[pageIntune], false)
	add(moreInspect, pageTitles[pageInspect], false)
	add(moreCatalog, pageTitles[pageCatalog], false)
	add(moreLog, pageTitles[pageLog], false)
	add(0, "", false)
	add(moreSavedSetups, "Saved setups...", false)
	add(0, "", false)
	add(moreAssociation, "Open .ssb files with SpoolSmith", associated)
	add(0, "", false)
	add(moreAbout, "About SpoolSmith", false)

	bounds := a.moreBtn.BoundsPixels()
	pt := win.POINT{X: int32(bounds.X), Y: int32(bounds.Y + bounds.Height)}
	win.ClientToScreen(win.GetParent(a.moreBtn.Handle()), &pt)
	choice := win.TrackPopupMenuEx(menu, win.TPM_RETURNCMD|win.TPM_LEFTALIGN|win.TPM_TOPALIGN, pt.X, pt.Y, a.mw.Handle(), nil)
	switch int(choice) {
	case moreIntune:
		a.goTo(pageIntune)
	case moreInspect:
		a.goTo(pageInspect)
	case moreCatalog:
		a.goTo(pageCatalog)
	case moreLog:
		a.goTo(pageLog)
		a.onRefreshLog()
	case moreSavedSetups:
		a.onOpenSavedSetup()
	case moreAssociation:
		a.toggleAssociation()
	case moreAbout:
		walk.MsgBox(a.mw, "About SpoolSmith", fmt.Sprintf("SpoolSmith %s\r\n\r\nCopy network printers between Windows PCs. Every change is shown and confirmed before it happens.", versionString()), walk.MsgBoxIconInformation)
	}
}

// navStyler draws the sidebar: a quiet list with the current page tinted and
// marked by a brand-colored bar.
type navStyler struct {
	list        *walk.ListBox
	bold        *walk.Font
	measuredDPI int
}

// measure sets every row's height for the list's current DPI. walk asks a
// styler for per-item heights only when they depend on width, and never again
// after a DPI change, so the sidebar does it itself. Win32 caps an
// owner-drawn row at 255 pixels.
func (s *navStyler) measure() {
	s.measuredDPI = s.list.DPI()
	for i := 0; i < navPageCount; i++ {
		s.list.SendMessage(win.LB_SETITEMHEIGHT, uintptr(i), uintptr(min(s.ItemHeight(i, 0), 255)))
	}
	s.list.Invalidate()
}

func (s *navStyler) px(v int) int { return v * s.list.DPI() / 96 }

func (s *navStyler) ItemHeightDependsOnWidth() bool { return false }
func (s *navStyler) DefaultItemHeight() int         { return s.px(40) }
func (s *navStyler) ItemHeight(int, int) int        { return s.px(40) }

func (s *navStyler) StyleItem(style *walk.ListItemStyle) {
	canvas := style.Canvas()
	if canvas == nil {
		return
	}
	if s.list.DPI() != s.measuredDPI {
		// Moved to a monitor with a different scale: re-measure after this paint.
		s.list.Synchronize(s.measure)
	}
	index := style.Index()
	bounds := style.BoundsPixels()
	selected := index == s.list.CurrentIndex()
	highContrast := walk.IsHighContrastEnabled()

	background, text, bar := colorSidebar, colorNavText, colorBrand
	if highContrast {
		background, text = sysColor(win.COLOR_WINDOW), sysColor(win.COLOR_WINDOWTEXT)
		if selected {
			background, text = sysColor(win.COLOR_HIGHLIGHT), sysColor(win.COLOR_HIGHLIGHTTEXT)
		}
		bar = text
	} else if selected {
		background, text = colorNavSelected, colorBrand
	}
	fill(canvas, background, bounds)
	if selected {
		fill(canvas, bar, walk.Rectangle{X: bounds.X, Y: bounds.Y + s.px(6), Width: s.px(4), Height: bounds.Height - s.px(12)})
	}
	font := style.Font
	if selected {
		font = s.bold
	}
	textBounds := walk.Rectangle{X: bounds.X + s.px(20), Y: bounds.Y, Width: bounds.Width - s.px(26), Height: bounds.Height}
	canvas.DrawTextPixels(pageTitles[index], font, text, textBounds, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
	if selected && s.list.Focused() {
		rc := win.RECT{Left: int32(bounds.X + s.px(8)), Top: int32(bounds.Y + s.px(2)), Right: int32(bounds.X + bounds.Width - s.px(4)), Bottom: int32(bounds.Y + bounds.Height - s.px(2))}
		win.DrawFocusRect(canvas.HDC(), &rc)
	}
}

func sysColor(index int) walk.Color { return walk.Color(win.GetSysColor(index)) }

func fill(canvas *walk.Canvas, color walk.Color, bounds walk.Rectangle) {
	brush, err := walk.NewSolidColorBrush(color)
	if err != nil {
		return
	}
	defer brush.Dispose()
	canvas.FillRectanglePixels(brush, bounds)
}
