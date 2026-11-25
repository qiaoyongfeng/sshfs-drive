package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The debug panel's buttons sit on the label's line, not above it.
func TestDebugPanelLayout(t *testing.T) {
	a := testApp(sample("Home", "host", "ada", "/", "M:", statusConnected))
	s := a.store.Settings()
	s.ShowDebugPanel = true
	a.store.settings = s
	a.logs.Add("service sshfs has been started")

	tt := ui.NewTester(a.view, 940, 580)
	label, ok := tt.Find("DEBUG OUTPUT")
	if !ok {
		t.Fatal("no debug label")
	}
	for _, name := range []string{"Copy debug output", "Clear debug output", "Hide debug output"} {
		button, ok := tt.Find(name)
		if !ok {
			t.Fatalf("%s is missing", name)
		}
		labelMid := label.Y + label.H/2
		buttonMid := button.Y + button.H/2
		if diff := labelMid - buttonMid; diff > 3 || diff < -3 {
			t.Errorf("%s is %.1f DIP off the label's line", name, diff)
		}
	}
	snapshot(t, tt)
	if os.Getenv("SNAPSHOT") != "" {
		t.Log("snapshot written")
	}
}
