// SSHFS Drive mounts SSH servers as drives on Windows, with native UI
// drawn by MyGo: no web page, no Electron, one Go binary.
package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance shows its window
	}
	mygo.App.SetName(appName)
	mygo.App.SetVersion(version)
	// The mounts outlive the window: the app stays in the tray.
	mygo.App.OnWindowAllClosed(func() {})

	application := newApp()
	mygo.App.OnSecondInstance(func([]string, string) { application.show() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { application.shutdown() })
	mygo.App.WhenReady(func() { application.start() })

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
