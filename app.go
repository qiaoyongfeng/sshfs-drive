package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// version is what the About dialog shows.
const version = "2.0.3"

// appName is what the window, the tray, the notification and the About dialog
// call this app. It is also the directory under %APPDATA%, so changing it
// moves the configuration.
const appName = "SSHFS Drive"

// unmountGrace is how long a quit waits for the drives to come down. A drive
// that will not let go must not keep the app alive: WinFsp clears the mounts
// of a process that ends, and the user asked to quit.
const unmountGrace = 1500 * time.Millisecond

// app is the state behind the window.
type app struct {
	store   *Store
	win     *mygo.Window
	tray    *mygo.Tray
	mounter mounter

	// mounts are the live drives, by connection id.
	mounts  map[string]*mount
	mountMu sync.Mutex

	logs    *logBuffer
	logList ui.ListState

	// logFile is the optional trace file, opened the first time a line is
	// written to it.
	logFile *os.File
	logOnce sync.Once

	list ui.ListState

	// Add and edit dialog.
	editOpen bool
	draft    *Connection
	editing  bool
	tab      int
	touched  bool

	settingsOpen  bool
	settingsDraft Settings

	aboutOpen bool

	askOpen bool
	ask     *Connection
	askText string

	confirmOpen bool
	confirm     *Connection

	// toasts shows messages that come from outside the view.
	toasts []string

	done chan struct{}
	// quitting is set when a quit starts. The window then has to close for
	// real: hiding it would cancel the quit, because a window that keeps
	// itself open in OnClose cancels it.
	quitting   bool
	closingMsg bool
}

func newApp() *app {
	a := &app{
		logs:   newLogBuffer(),
		done:   make(chan struct{}),
		mounts: map[string]*mount{},
	}
	a.mounter.logf = a.log
	return a
}

// log keeps a process line and redraws the debug panel while it is open.
func (a *app) log(line string) {
	a.logs.Add(line)
	a.traceLog(line)
	if a.win != nil && !a.quitting && a.store != nil && a.store.Settings().ShowDebugPanel {
		a.win.Invalidate()
	}
}

// traceLog copies what a mount does into the file SSHFSMGR_LOG names, so a
// drive that feels slow can be looked at after the fact. The debug panel only
// holds the last couple of thousand lines and lives in a window.
func (a *app) traceLog(line string) {
	path := os.Getenv("SSHFSMGR_LOG")
	if path == "" {
		return
	}
	a.logOnce.Do(func() {
		if f, err := os.Create(path); err == nil {
			a.logFile = f
		}
	})
	if a.logFile != nil {
		fmt.Fprintf(a.logFile, "%s %s\n", time.Now().Format("15:04:05.000"), line)
	}
}

// toast queues a message the view shows as a toast on its next build.
func (a *app) toast(message string) {
	a.toasts = append(a.toasts, message)
}

// errorf shows a message in the window's error style.
func (a *app) errorf(format string, args ...any) {
	a.toast(fmt.Sprintf(format, args...))
	if a.store != nil && a.store.Settings().ShowDebugPanel {
		a.log("! " + fmt.Sprintf(format, args...))
	}
}

// start loads the configuration and builds the window and its tray icon.
func (a *app) start() {
	store, err := OpenStore()
	if err != nil {
		log.Printf("config: %v", err)
	}
	a.store = store
	a.settingsDraft = store.Settings()
	if a.settingsDraft.ShowDebugPanel {
		a.logList.FollowEnd = true
	}

	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:          appName,
		Width:          940,
		Height:         580,
		UseContentSize: true,
		MinWidth:       780,
		MinHeight:      460,
		StateKey:       "main",
		Content:        ui.View(a.view),
	})

	// Closing the window leaves the mounts running: the app lives in the
	// tray, as it did before. A quit is the exception, and it has to be told
	// apart here: a window that prevents its close cancels the quit, which
	// would leave the app in the tray after Quit was chosen.
	a.win.OnClose(a.onWindowClose)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })

	a.setUpTray()

	// Keep the login item in step with the setting, as the old app did.
	_ = mygo.App.SetOpenAtLogin(a.store.Settings().StartupWithOS)

	for _, c := range a.store.Connections() {
		if c.Advanced.ConnectOnStartup {
			a.connect(c)
		}
	}
}

// setUpTray adds the taskbar icon with its menu.
func (a *app) setUpTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon(), ToolTip: appName})
	if err != nil {
		log.Printf("tray: %v", err)
		return
	}
	a.tray = tray
	a.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open " + appName, Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		mygo.Separator(),
		{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}))
}

// show brings the window back from the tray.
func (a *app) show() {
	if a.win == nil {
		return
	}
	a.win.Show()
	a.win.Focus()
}

// onWindowClose hides the window into the tray, unless a quit is what closed
// it: hiding would cancel the quit, and the app would never exit.
func (a *app) onWindowClose(e *mygo.CloseEvent) {
	if a.quitting {
		return
	}
	e.PreventDefault()
	if a.win == nil {
		return // views run in tests too, where there is no tray to speak of
	}
	a.win.Hide()
	if a.store != nil && a.store.Settings().TrayMessageOnClose && !a.closingMsg {
		a.closingMsg = true
		mygo.NewNotification(mygo.NotificationOptions{
			ID:    "tray",
			Title: appName,
			Body:  "Still running in the system tray",
		}).Show()
	}
}

// shutdown takes every drive down before the app exits. It waits only a
// moment: a drive that is busy in Explorer must not keep the app running
// after the user asked it to quit.
func (a *app) shutdown() {
	select {
	case <-a.done:
		return
	default:
		close(a.done)
	}
	mounts := a.mountList()
	// The icon goes first: a quit that waits on a busy drive would otherwise
	// leave it in the tray with nothing behind it.
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
	if len(mounts) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, mt := range mounts {
			wg.Add(1)
			go func(mt *mount) {
				defer wg.Done()
				_ = mt.Unmount()
			}(mt)
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(unmountGrace):
	}
}

// onMain runs work on the thread that owns the window, or right away when
// there is no window (the views run in tests too, without an event loop).
func (a *app) onMain(f func()) {
	if a.win == nil {
		f()
		return
	}
	a.win.Update(f)
}

func (a *app) putMount(c *Connection, mt *mount) {
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	a.mounts[c.ID] = mt
}

func (a *app) takeMount(id string) (*mount, bool) {
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	mt, ok := a.mounts[id]
	delete(a.mounts, id)
	return mt, ok
}

// mounted reports whether a connection has a live drive.
func (a *app) mounted(id string) bool {
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	_, ok := a.mounts[id]
	return ok
}

func (a *app) mountList() []*mount {
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	out := make([]*mount, 0, len(a.mounts))
	for _, mt := range a.mounts {
		out = append(out, mt)
	}
	return out
}

func (a *app) connections() []*Connection {
	if a.store == nil {
		return nil
	}
	return a.store.Connections()
}

// connect mounts a connection, asking for its password when it needs one.
func (a *app) connect(c *Connection) {
	if c.Status == statusConnected || c.Status == statusConnecting {
		return
	}
	if c.Host == "" || c.User == "" {
		a.errorf("Set a host and a user for %q first", c.Name)
		return
	}
	if c.Auth == authPasswordAsk {
		a.ask = c
		a.askText = ""
		a.askOpen = true
		return
	}
	a.store.password(c)
	c.Status = statusConnecting
	a.mountAsync(c)
}

// mountAsync dials and mounts off the main thread, and applies the result on it.
func (a *app) mountAsync(c *Connection) {
	settings := a.store.Settings()
	work := c.Clone()
	go func() {
		mt, err := a.mounter.Mount(work, settings, work.Password)
		a.onMain(func() {
			if err != nil {
				c.Status = statusDisconnected
				a.errorf("%s: %v", nameOf(c), err)
				return
			}
			c.MountedDrive = mt.Drive()
			c.Status = statusConnected
			if c.Auth == authPasswordAsk {
				c.Password = "" // asked again next time, never saved
			}
			a.putMount(c, mt)
			a.store.Save()
			a.watch(c, mt)
			a.toast(nameOf(c) + " mounted on " + c.MountLabel())
		})
	}()
}

// watch reports a drive that goes away on its own: an Explorer unmount, a
// dropped network, a server that stopped answering.
func (a *app) watch(c *Connection, mt *mount) {
	go func() {
		err := mt.Wait()
		a.onMain(func() {
			if cur, ok := a.takeMount(c.ID); !ok || cur != mt {
				return // disconnect is already taking it down
			}
			c.Status = statusDisconnected
			if err != nil {
				a.errorf("%s was disconnected: %v", nameOf(c), err)
			} else {
				a.toast(nameOf(c) + " unmounted")
			}
		})
	}()
}

// disconnect unmounts a connection.
func (a *app) disconnect(c *Connection) {
	mt, ok := a.takeMount(c.ID)
	if !ok {
		c.Status = statusDisconnected
		return
	}
	c.Status = statusClosing
	go func() {
		err := mt.Unmount()
		a.onMain(func() {
			c.Status = statusDisconnected
			if err != nil {
				a.errorf("unmount %s: %v", nameOf(c), err)
			} else {
				a.toast(nameOf(c) + " unmounted")
			}
		})
	}()
}

// nameOf is the label the list and the messages use for a connection.
func nameOf(c *Connection) string {
	if c.Name != "" {
		return c.Name
	}
	if c.Host != "" {
		return c.User + "@" + c.Host
	}
	return "Connection"
}

// addConnection opens the dialog on a new connection.
func (a *app) addConnection() {
	a.draft = NewConnection()
	a.editing = false
	a.tab = 0
	a.touched = false
	a.editOpen = true
}

// editConnection opens the dialog on a copy of an existing connection.
func (a *app) editConnection(c *Connection) {
	a.store.password(c)
	a.draft = c.Clone()
	a.editing = true
	a.tab = 0
	a.touched = false
	a.editOpen = true
}

// duplicateConnection saves a copy of a connection under a new name.
func (a *app) duplicateConnection(c *Connection) {
	dup := c.Clone()
	dup.ID = newID()
	dup.Name = nameOf(c) + " (copy)"
	dup.Status = statusDisconnected
	dup.MountedDrive = ""
	a.store.Add(dup)
	a.toast("Duplicated " + nameOf(c))
}

// saveDraft stores what the edit dialog holds.
func (a *app) saveDraft() {
	d := a.draft
	if d == nil {
		return
	}
	d.Name = trimSpace(d.Name)
	d.Host = trimSpace(d.Host)
	d.User = trimSpace(d.User)
	d.Folder = trimSpace(d.Folder)
	if d.Folder == "" {
		d.Folder = "/"
	}
	if d.Port <= 0 {
		d.Port = 22
	}

	if a.editing {
		a.store.Update(d)
	} else {
		d.Status = statusDisconnected
		a.store.Add(d)
	}
	a.editOpen = false
	a.draft = nil
	a.toast("Saved " + nameOf(d))
}

// applyLoginItem writes the startup setting to the system's login items. It
// runs off the main thread, and only once there is a window: views run in
// tests too, where the app's event loop could not do the work.
func (a *app) applyLoginItem() {
	if a.win == nil {
		return
	}
	startup := a.store.Settings().StartupWithOS
	go func() {
		if err := mygo.App.SetOpenAtLogin(startup); err != nil {
			log.Printf("login item: %v", err)
		}
	}()
}

// openMount shows a mounted drive in Explorer.
func (a *app) openMount(c *Connection) {
	drive := c.MountedDrive
	if drive == "" {
		drive = c.MountPoint
	}
	if drive == "" || drive == "auto" {
		a.errorf("%s has no drive yet", nameOf(c))
		return
	}
	go func() {
		if err := mygo.Shell.OpenPath(drive + `\`); err != nil {
			a.win.Update(func() { a.errorf("open %s: %v", drive, err) })
		}
	}()
}

// toggleDebug shows or hides the debug panel, and remembers it.
func (a *app) toggleDebug() {
	s := a.store.Settings()
	s.ShowDebugPanel = !s.ShowDebugPanel
	a.store.SetSettings(s)
	a.logList.FollowEnd = true
}

// trimSpace trims the whitespace users leave around what they type.
func trimSpace(s string) string { return strings.TrimSpace(s) }
