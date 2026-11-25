//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/crypto/ssh"
)

// mounter mounts connections as drives and takes them down again.
type mounter struct {
	logf func(string)
}

// mount is one live drive: the WinFsp host serving it, the SSH connection under
// it, and a channel that closes when it goes away.
type mount struct {
	conn  *Connection
	drive string
	host  *fuse.FileSystemHost
	fs    *winfs
	ssh   *ssh.Client
	sftp  *sftp.Client
	done  chan struct{}

	mu  sync.Mutex
	err error
}

// Mount dials the server, mounts the drive, and returns once Windows can use
// it. The caller runs it off the main thread.
func (m *mounter) Mount(c *Connection, s Settings, password string) (*mount, error) {
	if !winfspInstalled() {
		return nil, errors.New("WinFsp is not installed: get it from winfsp.dev, then connect again")
	}

	drive := c.MountPoint
	if drive == "" || drive == "auto" {
		d, err := freeDrive(c.MountedDrive)
		if err != nil {
			return nil, err
		}
		drive = d
	}

	timeout := time.Duration(s.ConnectTimeout) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	logf := func(line string) {
		if m.logf != nil {
			m.logf(line)
		}
	}

	logf(strings.Repeat("-", 60))
	logf("date: " + time.Now().Format(time.RFC3339))
	logf(fmt.Sprintf("conn: {%s} (%s) %s", c.ID, c.Name, c.Auth))
	logf(fmt.Sprintf("mount: %s@%s:%s on %s", c.User, c.Host, folderOf(c), drive))

	sshClient, err := connectSSH(c, password, timeout, logf)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient,
		sftp.UseConcurrentReads(true),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64))
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("start the sftp session: %w", err)
	}

	root := folderOf(c)
	if fi, err := sftpClient.Stat(root); err != nil {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("remote folder %s: %w", root, err)
	} else if !fi.IsDir() {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("remote folder %s is not a directory", root)
	}
	logf("sftp: session open, serving " + root)

	session := sftpRemote{Client: sftpClient}
	if tracing() {
		session = traceRemote(session, logf)
	}
	v := newVFS(session, root, c.Advanced.ReadOnly,
		time.Duration(s.CacheTimeout)*time.Second)
	fs := newWinFS(v, logf)
	host := fuse.NewFileSystemHost(fs)
	// Read a directory with its attributes in one call: Explorer stats every
	// entry it lists, and this halves the round trips.
	host.SetCapReaddirPlus(true)

	opts := []string{
		"-o", "volname=" + truncate(c.Name, 32),
		"-o", "umask=000",
		"-o", "uid=-1",
		"-o", "gid=-1",
	}
	// Windows keeps file information for one second before asking again, and
	// on a network drive every one of those questions is a round trip. Letting
	// it keep the answer as long as this app does is what stops Explorer from
	// re-asking for what it was just told.
	info := s.CacheTimeout * 1000
	if info <= 0 {
		info = 20000
	}
	if info > 20000 {
		info = 20000
	}
	opts = append(opts,
		"-o", "FileInfoTimeout="+itoa(info),
		"-o", "VolumeInfoTimeout=60000",
	)

	// WinFsp's Mount blocks until the drive is unmounted, and panics when the
	// DLL it needs is missing; both are handled here.
	ret := make(chan bool, 1)
	go func() {
		ok := false
		defer func() {
			if r := recover(); r != nil {
				logf(fmt.Sprintf("mount: %v", r))
			}
			ret <- ok
		}()
		ok = host.Mount(drive, opts)
	}()

	select {
	case <-fs.ready:
	case ok := <-ret:
		_ = sftpClient.Close()
		_ = sshClient.Close()
		if !ok {
			return nil, fmt.Errorf("cannot mount %s: is the drive letter in use?", drive)
		}
		return nil, errors.New("the drive went away as soon as it mounted")
	case <-time.After(timeout):
		host.Unmount()
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, fmt.Errorf("mounting %s timed out after %s", drive, timeout)
	}

	// WinFsp says the file system started before Windows will accept I/O on
	// the volume, so wait for the drive to answer: "connected" has to mean
	// usable, or the first thing the user does fails.
	if err := waitDrive(drive); err != nil {
		host.Unmount()
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, err
	}
	logf("drive: " + drive + " is ready")

	mt := &mount{conn: c, drive: drive, host: host, fs: fs, ssh: sshClient, sftp: sftpClient, done: make(chan struct{})}
	go func() {
		if !<-ret {
			mt.setErr(errors.New("the mount stopped"))
		}
		close(mt.done)
	}()
	mt.keepAlive(logf)
	return mt, nil
}

// keepAlive asks the server for a sign of life now and then. A drive whose
// connection died would otherwise look mounted and fail every read, which is
// worse than saying it went away.
func (mt *mount) keepAlive(logf func(string)) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-mt.done:
				return
			case <-ticker.C:
				if _, _, err := mt.ssh.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					logf("ssh: keepalive failed, unmounting")
					mt.setErr(errors.New("the connection to the server was lost"))
					mt.host.Unmount()
					return
				}
			}
		}
	}()
}

func (mt *mount) setErr(err error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if mt.err == nil {
		mt.err = err
	}
}

// Drive is the drive letter the mount took, which "auto" decides at connect.
func (mt *mount) Drive() string { return mt.drive }

// Wait blocks until the drive goes away, and says why it did. A nil error
// means it was unmounted; anything else means it broke.
func (mt *mount) Wait() error {
	<-mt.done
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.err
}

// Unmount takes the drive down and closes the connection under it. It does not
// wait long: the caller may be quitting, and a drive Explorer is busy with
// must not hold the app up. Closing the session makes whatever is still in
// flight fail at once instead of hanging.
func (mt *mount) Unmount() error {
	mt.host.Unmount()
	var err error
	select {
	case <-mt.done:
	case <-time.After(5 * time.Second):
		err = errors.New("the drive did not unmount")
	}
	_ = mt.sftp.Close()
	_ = mt.ssh.Close()
	return err
}

// waitDrive waits until Windows can look inside the new drive.
func waitDrive(drive string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(drive + `\`); err == nil && fi.IsDir() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the drive %s did not become usable", drive)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// winfspInstalled reports whether the DLL cgofuse loads is where it expects:
// next to the executable's PATH, or in WinFsp's own directory, which cgofuse
// finds through the registry.
func winfspInstalled() bool {
	if _, err := syscall.LoadDLL("winfsp-x64.dll"); err == nil {
		return true
	}
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if base == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, "WinFsp", "bin", "winfsp-x64.dll")); err == nil {
			return true
		}
	}
	return false
}
