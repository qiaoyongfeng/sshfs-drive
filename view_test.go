package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// testApp is an app with a store that writes nowhere, so tests stay in memory.
func testApp(conns ...*Connection) *app {
	a := newApp()
	a.store = &Store{settings: defaultSettings()}
	for _, c := range conns {
		a.store.conns = append(a.store.conns, c)
	}
	a.settingsDraft = a.store.Settings()
	return a
}

// sample builds a connection that looks like one a user made.
func sample(name, host, user, folder, drive string, status Status) *Connection {
	c := NewConnection()
	c.Name = name
	c.Host = host
	c.User = user
	c.Folder = folder
	c.MountPoint = "auto"
	c.MountedDrive = drive
	c.Status = status
	return c
}

// snapshot writes the tester's last frame when SNAPSHOT is set, so the view
// can be looked at without opening a window.
func snapshot(t *testing.T, tt *ui.Tester) {
	t.Helper()
	path := os.Getenv("SNAPSHOT")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowShowsConnections(t *testing.T) {
	home := sample("Home server", "192.168.1.20", "ada", "/home/ada", "M:", statusConnected)
	work := sample("Build box", "build.example.com", "ci", "/srv/build", "", statusDisconnected)
	nas := sample("NAS", "nas.local", "media", "/volume1", "", statusDisconnected)
	a := testApp(home, work, nas)

	tt := ui.NewTester(a.view, 940, 580)
	for _, text := range []string{"Connections", "Home server", "Build box", "NAS", "M:"} {
		if !tt.HasText(text) {
			t.Fatalf("%q is missing, texts %q", text, tt.Texts())
		}
	}
	if !tt.HasText("1 of 3 mounted") {
		t.Errorf("the sidebar count is missing: %q", tt.Texts())
	}
	snapshot(t, tt)
}

func TestEmptyState(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.view, 940, 580)
	if !tt.HasText("No connections yet") {
		t.Fatalf("no empty state: %q", tt.Texts())
	}
	if err := tt.Click("Add Connection"); err != nil {
		t.Fatal(err)
	}
	if !a.editOpen || a.draft == nil {
		t.Fatal("Add Connection did not open the dialog")
	}
	if !tt.HasText("Basic") || !tt.HasText("Advanced") {
		t.Errorf("the dialog has no tabs: %q", tt.Texts())
	}
	snapshot(t, tt)
}

func TestAddConnectionSavesWhatWasTyped(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.view, 940, 580)
	if err := tt.Click("Add Connection"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Name"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Home server")
	if err := tt.Click("Server"); err != nil {
		t.Fatal(err)
	}
	tt.Type("192.168.1.20")
	if err := tt.Click("User"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ada")
	if err := tt.Click("Remote path"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	conns := a.store.Connections()
	if len(conns) != 1 {
		t.Fatalf("%d connections saved, want 1", len(conns))
	}
	got := conns[0]
	if got.Name != "Home server" || got.Host != "192.168.1.20" || got.User != "ada" {
		t.Errorf("saved %+v", got)
	}
	if a.editOpen {
		t.Error("the dialog stayed open")
	}
	if !tt.HasText("Home server") {
		t.Errorf("the new connection is not in the list: %q", tt.Texts())
	}
}

func TestSaveRefusesAnIncompleteConnection(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.view, 940, 580)
	if err := tt.Click("Add Connection"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if len(a.store.Connections()) != 0 {
		t.Fatal("an empty connection was saved")
	}
	if !tt.HasText("Give the connection a name.") {
		t.Errorf("no error shown: %q", tt.Texts())
	}
	if !a.editOpen {
		t.Error("the dialog closed on an invalid connection")
	}
}

func TestEditKeepsRuntimeStateAndOrder(t *testing.T) {
	home := sample("Home", "host", "ada", "/", "M:", statusConnected)
	work := sample("Work", "host2", "bob", "/", "", statusDisconnected)
	a := testApp(home, work)

	a.editConnection(work)
	a.draft.Name = "Work (renamed)"
	a.saveDraft()

	conns := a.store.Connections()
	if len(conns) != 2 || conns[1].Name != "Work (renamed)" || conns[0].ID != home.ID {
		t.Fatalf("after editing: %+v", conns)
	}
	if conns[0].Status != statusConnected {
		t.Errorf("editing another connection changed the mounted one: %+v", conns[0])
	}
}

func TestDuplicate(t *testing.T) {
	home := sample("Home", "host", "ada", "/", "M:", statusConnected)
	a := testApp(home)
	a.duplicateConnection(home)
	conns := a.store.Connections()
	if len(conns) != 2 {
		t.Fatalf("%d connections, want 2", len(conns))
	}
	dup := conns[1]
	if dup.ID == home.ID {
		t.Error("the copy kept the original's id")
	}
	if dup.Name != "Home (copy)" {
		t.Errorf("the copy is named %q", dup.Name)
	}
	if dup.Status != statusDisconnected {
		t.Errorf("the copy thinks it is mounted: %+v", dup)
	}
}

func TestDeleteAsksFirst(t *testing.T) {
	home := sample("Home", "host", "ada", "/", "M:", statusDisconnected)
	a := testApp(home)
	tt := ui.NewTester(a.view, 940, 580)

	a.confirm = home
	a.confirmOpen = true
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if len(a.store.Connections()) != 1 {
		t.Fatal("Cancel deleted the connection")
	}

	a.confirm = home
	a.confirmOpen = true
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if len(a.store.Connections()) != 0 {
		t.Fatal("Delete kept the connection")
	}
}

func TestSettingsDialogSaves(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.view, 940, 580)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !a.settingsOpen {
		t.Fatal("Settings did not open")
	}
	a.settingsDraft.ShowDebugPanel = true
	tt.Frame()
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	if !a.store.Settings().ShowDebugPanel {
		t.Error("the debug panel setting was not saved")
	}
	if !tt.HasText("DEBUG OUTPUT") {
		t.Errorf("the debug panel is not shown: %q", tt.Texts())
	}
	snapshot(t, tt)
}

func TestDebugPanelShowsProcessOutput(t *testing.T) {
	a := testApp(sample("Home", "host", "ada", "/", "M:", statusConnected))
	settings := a.store.Settings()
	settings.ShowDebugPanel = true
	a.store.settings = settings
	a.logs.Add("service sshfs has been started")

	tt := ui.NewTester(a.view, 940, 580)
	if !tt.HasText("DEBUG OUTPUT") {
		t.Fatalf("no debug panel: %q", tt.Texts())
	}
	if !tt.HasText("service sshfs has been started") {
		t.Errorf("the log line is missing: %q", tt.Texts())
	}
	if err := tt.Click("Copy debug output"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != "service sshfs has been started" {
		t.Errorf("clipboard holds %q", got)
	}
	if err := tt.Click("Clear debug output"); err != nil {
		t.Fatal(err)
	}
	if a.logs.Len() != 0 {
		t.Error("Clear kept the lines")
	}
}

func TestAdvancedTabHasMountOptions(t *testing.T) {
	a := testApp()
	a.addConnection()
	a.tab = 1
	tt := ui.NewTester(a.view, 940, 580)
	for _, text := range []string{"On startup", "Read-only"} {
		if !tt.HasText(text) {
			t.Fatalf("%q is missing: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt)
}

func TestDarkTheme(t *testing.T) {
	home := sample("Home server", "192.168.1.20", "ada", "/home/ada", "M:", statusConnected)
	work := sample("Build box", "build.example.com", "ci", "/srv/build", "", statusDisconnected)
	a := testApp(home, work)
	tt := ui.NewTesterWith(a.view, 940, 580, ui.TesterOptions{Dark: true})
	if !tt.HasText("Home server") {
		t.Fatalf("dark window: %q", tt.Texts())
	}
	snapshot(t, tt)
}
