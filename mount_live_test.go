//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveMount mounts a real drive and uses it the way Explorer and an editor
// would, through the Windows file API:
//
//	SSHFSMGR_LIVE_HOST=192.0.2.10 go test -run TestLiveMount -v .
//
// driveLetters is what the system says it has, for the test's log.
func driveLetters() []string {
	mask, _, _ := procGetLogicalDrives.Call()
	var out []string
	for l := byte('A'); l <= 'Z'; l++ {
		if mask&(1<<(l-'A')) != 0 {
			out = append(out, string(l)+":")
		}
	}
	return out
}

func TestLiveMount(t *testing.T) {
	host := os.Getenv("SSHFSMGR_LIVE_HOST")
	if host == "" {
		t.Skip("set SSHFSMGR_LIVE_HOST to mount a real drive")
	}
	user := os.Getenv("SSHFSMGR_LIVE_USER")
	if user == "" {
		user = "qiao"
	}
	key := os.Getenv("SSHFSMGR_LIVE_KEY")
	if key == "" {
		home, _ := os.UserHomeDir()
		key = filepath.Join(home, ".ssh", "id_ed25519")
	}

	m := &mounter{logf: func(line string) { t.Log(line) }}
	c := NewConnection()
	c.Name = "live test"
	c.Host = host
	c.User = user
	c.Auth = authKeyFile
	c.KeyFile = key
	c.Folder = "/tmp"
	c.MountPoint = "auto"

	start := time.Now()
	mt, err := m.Mount(c, defaultSettings(), "")
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer mt.Unmount()
	root := mt.Drive() + `\`
	t.Logf("mounted %s in %s", mt.Drive(), time.Since(start).Round(time.Millisecond))

	var fi os.FileInfo
	for i := 0; i < 40; i++ {
		if fi, err = os.Stat(root); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("drives: %v", driveLetters())
	if err != nil {
		t.Fatalf("stat the drive: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("the drive is not a directory: %v", fi.Mode())
	}

	dir := filepath.Join(root, "sshfsmgr-live-"+newID())
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	name := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(name, []byte("hello from windows"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello from windows" {
		t.Fatalf("read back %q", got)
	}

	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 1 || ents[0].Name() != "hello.txt" {
		t.Fatalf("readdir: %v, %v", err, ents)
	}

	renamed := filepath.Join(dir, "renamed.txt")
	if err := os.Rename(name, renamed); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if fi, err := os.Stat(renamed); err != nil || fi.Size() != 18 {
		t.Fatalf("stat after rename: %v, %v", fi, err)
	}

	const mb = 8
	blob := make([]byte, mb<<20)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")

	begin := time.Now()
	if err := os.WriteFile(big, blob, 0o644); err != nil {
		t.Fatalf("write %d MB: %v", mb, err)
	}
	wrote := time.Since(begin)

	begin = time.Now()
	back, err := os.ReadFile(big)
	if err != nil {
		t.Fatalf("read %d MB: %v", mb, err)
	}
	read := time.Since(begin)
	if !bytes.Equal(back, blob) {
		t.Fatal("the big file came back different")
	}
	t.Logf("%d MB: write %s (%.1f MB/s), read %s (%.1f MB/s)",
		mb, wrote.Round(time.Millisecond), float64(mb)/wrote.Seconds(),
		read.Round(time.Millisecond), float64(mb)/read.Seconds())

	// A hundred small files, which is what a source tree looks like.
	begin = time.Now()
	for i := 0; i < 100; i++ {
		p := filepath.Join(dir, "small-"+itoa(i)+".txt")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write small file %d: %v", i, err)
		}
	}
	created := time.Since(begin)
	begin = time.Now()
	if _, err := os.ReadDir(dir); err != nil {
		t.Fatalf("list after creating: %v", err)
	}
	listed := time.Since(begin)
	t.Logf("100 small files: create %s, list %s", created.Round(time.Millisecond), listed.Round(time.Millisecond))

	for _, p := range []string{renamed, big} {
		if err := os.Remove(p); err != nil {
			t.Errorf("remove %s: %v", p, err)
		}
	}
	for i := 0; i < 100; i++ {
		_ = os.Remove(filepath.Join(dir, "small-"+itoa(i)+".txt"))
	}
	if err := os.Remove(dir); err != nil {
		t.Errorf("rmdir: %v", err)
	}

	if err := mt.Unmount(); err != nil {
		t.Fatalf("unmount: %v", err)
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("the drive is still there after unmount")
	}
}
