//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBenchBrowse times the calls Explorer makes when a folder is opened:
// enumerate, then stat and open a few entries, plus the speculative lookups
// Windows does for desktop.ini and friends. It needs a server to mount:
//
//	SSHFSMGR_LIVE_HOST=192.0.2.10 go test -run TestBenchBrowse -v .
//
// The numbers to watch are stat and open: they are what makes a server feel
// slow to browse, and both should be near zero once the caches are warm.
func TestBenchBrowse(t *testing.T) {
	host := os.Getenv("SSHFSMGR_LIVE_HOST")
	if host == "" {
		t.Skip("no host")
	}
	home, _ := os.UserHomeDir()
	m := &mounter{logf: func(line string) { t.Log(line) }}
	c := NewConnection()
	c.Name, c.Host, c.User = "bench", host, "qiao"
	c.Auth = authKeyFile
	c.KeyFile = filepath.Join(home, ".ssh", "id_ed25519")
	c.Folder = "/tmp"
	mt, err := m.Mount(c, defaultSettings(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer mt.Unmount()
	root := mt.Drive() + `\`

	dir := filepath.Join(root, "bench-"+newID())
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const n = 300
	payload := make([]byte, 4096)
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, "file"+itoa(i)+".dat"), payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if err := os.Mkdir(filepath.Join(dir, "sub"+itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	report := func(name string, total time.Duration, ops int) {
		per := total / time.Duration(ops)
		t.Logf("%-26s %-10s total  %-9s/op", name, total.Round(time.Millisecond), per.Round(10*time.Microsecond))
	}

	for round := 0; round < 3; round++ {
		begin := time.Now()
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		report("readdir x"+itoa(round), time.Since(begin), len(ents))
	}

	for pass := 0; pass < 3; pass++ {
		begin := time.Now()
		for _, e := range mustReadDir(t, dir) {
			if _, err := os.Stat(filepath.Join(dir, e)); err != nil {
				t.Fatal(err)
			}
		}
		report("stat each pass"+itoa(pass), time.Since(begin), n+20)
	}

	var begin time.Time
	begin = time.Now()
	for _, name := range []string{"desktop.ini", "Thumbs.db", ".DS_Store", "Icon\r"} {
		for i := 0; i < 25; i++ {
			_, _ = os.Stat(filepath.Join(dir, name))
		}
	}
	report("speculative misses", time.Since(begin), 100)

	begin = time.Now()
	for i := 0; i < 20; i++ {
		p := filepath.Join(dir, "file"+itoa(i)+".dat")
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4096)
		if _, err := f.Read(b); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	report("open+read+close 4K", time.Since(begin), 20)

	begin = time.Now()
	for i := 0; i < 20; i++ {
		if err := os.Mkdir(filepath.Join(dir, "new"+itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	report("mkdir", time.Since(begin), 20)
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
