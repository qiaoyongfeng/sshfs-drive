//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHoldDrive mounts a drive on a fixed letter and keeps it there until a
// sentinel file appears, so another process can exercise the drive the way
// Explorer does while the mount stays up.
//
//	SSHFSMGR_HOLD=V: SSHFSMGR_TRACE=1 SSHFSMGR_LIVE_HOST=192.0.2.10 \
//	  go test -run TestHoldDrive -v -timeout 15m .
func TestHoldDrive(t *testing.T) {
	drive := os.Getenv("SSHFSMGR_HOLD")
	if drive == "" {
		t.Skip("set SSHFSMGR_HOLD to hold a drive")
	}
	host := os.Getenv("SSHFSMGR_LIVE_HOST")
	if host == "" {
		host = "192.0.2.10"
	}
	stop := filepath.Join(os.TempDir(), "stop-hold-drive")
	_ = os.Remove(stop)

	logPath := filepath.Join(os.TempDir(), "fs-trace.log")
	_ = os.Remove(logPath)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	m := &mounter{logf: func(line string) {
		fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), line)
	}}
	home, _ := os.UserHomeDir()
	c := NewConnection()
	c.Name, c.Host, c.User = "hold", host, "qiao"
	c.Auth = authKeyFile
	c.KeyFile = filepath.Join(home, ".ssh", "id_ed25519")
	c.Folder = "/home/qiao"
	c.MountPoint = drive

	mt, err := m.Mount(c, defaultSettings(), "")
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	t.Logf("holding %s, stop with: touch %s", mt.Drive(), stop)

	for i := 0; i < 3000; i++ {
		if _, err := os.Stat(stop); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := mt.Unmount(); err != nil {
		t.Errorf("unmount: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	t.Logf("trace: %d bytes at %s", len(data), logPath)
}
