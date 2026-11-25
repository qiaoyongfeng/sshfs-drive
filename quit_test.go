package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// A window that prevents its close cancels the quit, so the tray's Quit would
// leave the app running in the tray for ever. Closing the window has to hide
// it when the user clicked the X, and go through when a quit is what closed it.
func TestWindowCloseHidesUnlessTheAppIsQuitting(t *testing.T) {
	a := testApp()

	e := &mygo.CloseEvent{}
	a.onWindowClose(e)
	if !e.DefaultPrevented() {
		t.Error("closing the window did not hide it into the tray")
	}

	a.quitting = true
	e = &mygo.CloseEvent{}
	a.onWindowClose(e)
	if e.DefaultPrevented() {
		t.Error("a quit kept the window open, which cancels the quit")
	}
}
