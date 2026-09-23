//go:build windows

package main

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

// The sidebar replaces the tab strip that used to run across the top of the
// window, and the second tab strip nested inside its Tools tab.
//
// Tabs read as "pick a mode first", and the nested strip hid three whole
// screens behind a fourth. A sidebar lists every place the app can go at once,
// in the order the work happens, with the diagnostic screens set apart below
// a divider instead of folded away.
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

// firstToolPage starts the lower, less frequent group. The sidebar draws a
// divider and caption in the extra space above it.
const firstToolPage = pageIntune

// Sidebar titles double as the list items' UI Automation names, which the
// desktop test suite navigates by.
var pageTitles = [pageCount]string{
	"This PC",
	"Add a printer",
	"Review and apply",
	"Intune package",
	"Inspect",
	"Driver catalog",
	"Action log",
}

var (
	colorBrand       = walk.RGB(24, 76, 133)
	colorPage        = walk.RGB(250, 251, 253)
	colorSidebar     = walk.RGB(255, 255, 255)
	colorNavSelected = walk.RGB(229, 238, 250)
	colorNavText     = walk.RGB(44, 52, 64)
	colorNavCaption  = walk.RGB(112, 122, 136)
	colorDivider     = walk.RGB(222, 227, 234)
	colorHint        = walk.RGB(88, 98, 112) // 5.9:1 on colorPage
)

type navUI struct {
	navHost *walk.Composite
	nav     *walk.ListBox
	pages   [pageCount]*walk.Composite
	current page
}

// buildNav creates the sidebar list once the main window exists.
//
// It is created here rather than declared because the declarative ListBox
// makes an owner-drawn list without LBS_HASSTRINGS, and then the list keeps no
// text for its items: screen readers and UI Automation would see seven
// unnamed entries. With LBS_HASSTRINGS the native list keeps each title, so
// keyboard, Narrator and automation all get "This PC", "Add a printer"...
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
	if styler.caption, err = walk.NewFont("Segoe UI", 8, walk.FontBold); err != nil {
		return err
	}
	nav.SetItemStyler(styler)
	if err := nav.SetModel(pageTitles[:]); err != nil {
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
	return nil
}

// goTo navigates the way a click in the sidebar would.
func (a *app) goTo(p page) {
	if a.nav.CurrentIndex() != int(p) {
		a.nav.SetCurrentIndex(int(p)) // publishes CurrentIndexChanged -> showPage
		return
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
	// the first switch would hide nothing and all seven pages would stack.
	for i, content := range a.pages {
		setShown(content, page(i) == p)
	}
	// A button that navigated (Review's "This PC") may now be hidden while
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
	case pageAdd:
		a.setupGroup.SetVisible(a.setupOpen)
		a.searchGroup.SetVisible(!a.setupOpen)
		if a.setupOpen && !a.driversLoaded {
			a.onDrivers()
		}
	case pageReview:
		a.advancedPanel.SetVisible(a.advancedCheck.Checked())
		a.updateReviewControls()
	}
}

// navStyler draws the sidebar: a quiet list with the current page tinted and
// marked by a brand-colored bar, and a caption over the tools group.
type navStyler struct {
	list          *walk.ListBox
	bold, caption *walk.Font
	measuredDPI   int
}

// measure sets every row's height for the list's current DPI. walk asks a
// styler for per-item heights only when they depend on width, and never again
// after a DPI change, so the sidebar does it itself. Win32 caps an
// owner-drawn row at 255 pixels.
func (s *navStyler) measure() {
	s.measuredDPI = s.list.DPI()
	for i := 0; i < int(pageCount); i++ {
		s.list.SendMessage(win.LB_SETITEMHEIGHT, uintptr(i), uintptr(min(s.ItemHeight(i, 0), 255)))
	}
	s.list.Invalidate()
}

func (s *navStyler) px(v int) int { return v * s.list.DPI() / 96 }

func (s *navStyler) ItemHeightDependsOnWidth() bool { return false }
func (s *navStyler) DefaultItemHeight() int         { return s.px(38) }
func (s *navStyler) ItemHeight(index, _ int) int {
	if page(index) == firstToolPage {
		return s.px(38 + 34)
	}
	return s.px(38)
}

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

	row := bounds
	if page(index) == firstToolPage {
		head := bounds
		head.Height = min(s.px(34), bounds.Height/2)
		row.Y += head.Height
		row.Height -= head.Height
		headBackground, divider, caption := colorSidebar, colorDivider, colorNavCaption
		if highContrast {
			headBackground, divider, caption = sysColor(win.COLOR_WINDOW), sysColor(win.COLOR_WINDOWTEXT), sysColor(win.COLOR_WINDOWTEXT)
		}
		fill(canvas, headBackground, head)
		fill(canvas, divider, walk.Rectangle{X: head.X + s.px(16), Y: head.Y + s.px(8), Width: head.Width - s.px(32), Height: s.px(1)})
		canvas.DrawTextPixels("TOOLS", s.caption, caption,
			walk.Rectangle{X: head.X + s.px(18), Y: head.Y + s.px(12), Width: head.Width - s.px(24), Height: head.Height - s.px(12)},
			walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
	}
	if selected {
		fill(canvas, bar, walk.Rectangle{X: row.X, Y: row.Y + s.px(6), Width: s.px(4), Height: row.Height - s.px(12)})
	}
	font := style.Font
	if selected {
		font = s.bold
	}
	textBounds := walk.Rectangle{X: row.X + s.px(18), Y: row.Y, Width: row.Width - s.px(24), Height: row.Height}
	canvas.DrawTextPixels(pageTitles[index], font, text, textBounds, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
	if selected && s.list.Focused() {
		rc := win.RECT{Left: int32(row.X + s.px(8)), Top: int32(row.Y + s.px(2)), Right: int32(row.X + row.Width - s.px(4)), Bottom: int32(row.Y + row.Height - s.px(2))}
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
