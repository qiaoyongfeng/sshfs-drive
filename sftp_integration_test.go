package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// TestRealServer drives a whole mount against a server that really speaks SSH
// and SFTP, which is what proves the library does what ssh.exe used to:
//
//	SSHFSMGR_TEST_HOST=192.0.2.10 go test -run TestRealServer -v .
func TestRealServer(t *testing.T) {
	host := os.Getenv("SSHFSMGR_TEST_HOST")
	if host == "" {
		t.Skip("set SSHFSMGR_TEST_HOST to run against a real server")
	}
	user := os.Getenv("SSHFSMGR_TEST_USER")
	if user == "" {
		user = "qiao"
	}
	key := os.Getenv("SSHFSMGR_TEST_KEY")
	if key == "" {
		home, _ := os.UserHomeDir()
		key = filepath.Join(home, ".ssh", "id_ed25519")
	}

	c := NewConnection()
	c.Host = host
	c.User = user
	c.Auth = authKeyFile
	c.KeyFile = key

	logf := func(line string) { t.Log(line) }
	client, err := connectSSH(c, "", 15*time.Second, logf)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	session, err := sftp.NewClient(client,
		sftp.UseConcurrentReads(true),
		sftp.UseConcurrentWrites(true))
	if err != nil {
		t.Fatalf("sftp: %v", err)
	}

	v := newVFS(sftpRemote{Client: session}, "/tmp", false, time.Second)
	defer v.close()

	dir := "/sshfsmgr-" + newID()
	if err := v.mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer func() {
		_ = v.unlink(dir + "/notes.txt")
		_ = v.rmdir(dir)
	}()

	// Create, write at an offset, read back.
	fh, err := v.open(dir+"/notes.txt", flagReadWrite|flagCreate, true, 0o644)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, err := v.write(fh, 0, []byte("hello from go")); err != nil || n != 13 {
		t.Fatalf("write: %d %v", n, err)
	}
	if err := v.fsync(fh); err != nil {
		t.Fatalf("fsync: %v", err)
	}
	if err := v.release(fh); err != nil {
		t.Fatalf("release: %v", err)
	}

	fh, err = v.open(dir+"/notes.txt", 0, false, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	buf := make([]byte, 13)
	n, rerr := v.read(fh, 0, buf)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if string(buf[:n]) != "hello from go" {
		t.Fatalf("read %q", buf[:n])
	}
	v.release(fh)

	// The attributes Explorer asks for.
	a, remote, err := v.stat(dir + "/notes.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if a.size != 13 || a.dir {
		t.Errorf("attr %+v", a)
	}
	if remote != "/tmp"+dir+"/notes.txt" {
		t.Errorf("stat resolved to %q", remote)
	}

	// Listing.
	ents, err := v.readDir(dir)
	if err != nil || len(ents) != 1 || ents[0].name != "notes.txt" {
		t.Fatalf("readDir: %v %+v", err, ents)
	}

	// Renaming over an existing name, which is what Explorer's "replace" does.
	if _, err := v.open(dir+"/other.txt", flagReadWrite|flagCreate, true, 0o644); err != nil {
		t.Fatalf("create other: %v", err)
	}
	if err := v.rename(dir+"/notes.txt", dir+"/other.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if a, _, err := v.stat(dir + "/other.txt"); err != nil || a.size != 13 {
		t.Fatalf("after rename: %+v %v", a, err)
	}

	// Timestamps, which backups and make rely on.
	when := time.Unix(1600000000, 0)
	if err := v.utimens(dir+"/other.txt", when, when); err != nil {
		t.Errorf("utimens: %v", err)
	}
	if a, _, err := v.stat(dir + "/other.txt"); err == nil && a.mtime.Unix() != when.Unix() {
		t.Errorf("mtime is %s, want %s", a.mtime, when)
	}

	if err := v.unlink(dir + "/other.txt"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, _, err := v.stat(dir + "/other.txt"); err == nil {
		t.Error("the file is still there after unlink")
	}
	if err := v.rmdir(dir); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
}
