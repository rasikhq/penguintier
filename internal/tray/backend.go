package tray

import "fyne.io/systray"

// Backend is the part of the tray icon library the controller uses.
// fyneBackend is the real one; the tests use a fake that records every
// call, to check that the controller changes the menu as little as
// possible.
//
// The methods mirror fyne.io/systray's functions of the same names.
type Backend interface {
	SetIcon(png []byte)
	SetTooltip(text string)
	// ResetMenu removes every menu entry. The library closes each removed
	// entry's Clicked channel.
	ResetMenu()
	// AddItem adds an entry at the end of the top-level menu.
	AddItem(label, tooltip string, checkbox, checked bool) Item
	// AddSeparator adds a separator at the end of the top-level menu.
	AddSeparator()
}

// Item is one menu entry that a Backend created.
type Item interface {
	// AddSubItem adds an entry at the end of this entry's submenu. The
	// first one turns this entry into a submenu.
	AddSubItem(label, tooltip string, checkbox, checked bool) Item
	// AddSeparator adds a separator at the end of this entry's submenu.
	AddSeparator()
	SetTitle(label string)
	SetTooltip(text string)
	Check()
	Uncheck()
	Enable()
	Disable()
	// Clicked receives a value each time the entry is clicked. It is
	// closed when the entry is removed.
	Clicked() <-chan struct{}
}

// fyneBackend implements Backend with fyne.io/systray. The struct is empty:
// the library keeps its state in package-level variables. An empty struct
// takes no memory; it only gives the methods something to hang on.
type fyneBackend struct{}

func (fyneBackend) SetIcon(png []byte)     { systray.SetIcon(png) }
func (fyneBackend) SetTooltip(text string) { systray.SetTooltip(text) }
func (fyneBackend) ResetMenu()             { systray.ResetMenu() }
func (fyneBackend) AddSeparator()          { systray.AddSeparator() }

func (fyneBackend) AddItem(label, tooltip string, checkbox, checked bool) Item {
	// On Linux an entry only gets a check mark when it is created as a
	// checkbox; AddMenuItem makes one without.
	if checkbox {
		return fyneItem{systray.AddMenuItemCheckbox(label, tooltip, checked)}
	}
	return fyneItem{systray.AddMenuItem(label, tooltip)}
}

// fyneItem implements Item. It embeds *systray.MenuItem, so the library's
// SetTitle, SetTooltip, Check, Uncheck, Enable, Disable and AddSeparator
// methods become fyneItem's methods too; only the two below are new.
type fyneItem struct {
	*systray.MenuItem
}

func (i fyneItem) AddSubItem(label, tooltip string, checkbox, checked bool) Item {
	if checkbox {
		return fyneItem{i.AddSubMenuItemCheckbox(label, tooltip, checked)}
	}
	return fyneItem{i.AddSubMenuItem(label, tooltip)}
}

// Clicked returns the library's ClickedCh field. Its type is a plain
// "chan struct{}"; returning it as "<-chan struct{}" lets callers only
// receive from it.
func (i fyneItem) Clicked() <-chan struct{} {
	return i.ClickedCh
}
