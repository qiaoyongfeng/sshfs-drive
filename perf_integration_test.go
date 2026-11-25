package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// TestPerfSFTP measures the same work the drive does, without WinFsp or
// Windows on top, so a slow drive can be blamed on the right layer.
//
//	SSHFSMGR_TEST_HOST=192.0.2.10 go test -run TestPerfSFTP -v -timeout 300s .
func TestPerfSFTP(t *testing.T) {
	host := os.Getenv("SSHFSMGR_TEST_HOST")
	if host == "" {
		t.Skip("set SSHFSMGR_TEST_HOST to measure a real server")
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

	client, err := connectSSH(c, "", 15*time.Second, func(string) {})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	for _, packet := range []int{32 << 10, 128 << 10, 256 << 10} {
		session, err := sftp.NewClient(client,
			sftp.UseConcurrentReads(true),
			sftp.UseConcurrentWrites(true),
			sftp.MaxConcurrentRequestsPerFile(64),
			sftp.MaxPacketUnchecked(packet))
		if err != nil {
			t.Fatalf("sftp: %v", err)
		}
		v := newVFS(sftpRemote{Client: session}, "/tmp", false, 20*time.Second)

		dir := "/perf-" + newID()
		if err := v.mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		const files = 100
		begin := time.Now()
		for i := 0; i < files; i++ {
			fh, err := v.open(dir+"/s"+itoa(i)+".txt", flagReadWrite|flagCreate, true, 0o644)
			if err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
			if _, err := v.write(fh, 0, []byte("x")); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			if err := v.release(fh); err != nil {
				t.Fatalf("release %d: %v", i, err)
			}
		}
		perFile := time.Since(begin) / files

		const mb = 8
		blob := make([]byte, mb<<20)
		_, _ = rand.Read(blob)

		begin = time.Now()
		fh, err := v.open(dir+"/big.bin", flagReadWrite|flagCreate, true, 0o644)
		if err != nil {
			t.Fatalf("create big: %v", err)
		}
		if _, err := v.write(fh, 0, blob); err != nil {
			t.Fatalf("write big: %v", err)
		}
		v.release(fh)
		wrote := time.Since(begin)

		begin = time.Now()
		fh, err = v.open(dir+"/big.bin", 0, false, 0)
		if err != nil {
			t.Fatalf("open big: %v", err)
		}
		buf := make([]byte, len(blob))
		if _, err := v.read(fh, 0, buf); err != nil {
			t.Fatalf("read big: %v", err)
		}
		v.release(fh)
		read := time.Since(begin)

		t.Logf("packet %3d KB: create %s/file, write %5.1f MB/s, read %5.1f MB/s",
			packet>>10, perFile.Round(100*time.Microsecond),
			float64(mb)/wrote.Seconds(), float64(mb)/read.Seconds())

		_ = v.unlink(dir + "/big.bin")
		for i := 0; i < files; i++ {
			_ = v.unlink(dir + "/s" + itoa(i) + ".txt")
		}
		_ = v.rmdir(dir)
		v.close()
	}
}
