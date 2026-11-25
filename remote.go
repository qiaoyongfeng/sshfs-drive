package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pkg/sftp"
)

// remote is the slice of an SFTP client a mount needs. The interface exists so
// the file system can be exercised against an in-memory server in tests.
type remote interface {
	Close() error
	Stat(p string) (os.FileInfo, error)
	ReadDir(p string) ([]os.FileInfo, error)
	ReadLink(p string) (string, error)
	Symlink(oldname, newname string) error
	Mkdir(p string) error
	Remove(p string) error
	RemoveDirectory(p string) error
	Rename(oldname, newname string) error
	PosixRename(oldname, newname string) error
	Chmod(p string, mode os.FileMode) error
	Chtimes(p string, atime, mtime time.Time) error
	Truncate(p string, size int64) error
	StatVFS(p string) (*sftp.StatVFS, error)
	OpenFile(p string, flags int) (remoteFile, error)
}

// remoteFile is an open file on the server: reads and writes carry an offset,
// so many of them can be in flight without a seek.
type remoteFile interface {
	io.ReaderAt
	io.WriterAt
	Stat() (os.FileInfo, error)
	Truncate(size int64) error
	Sync() error
	Close() error
}

// sftpRemote adapts an *sftp.Client to remote. The wrapper is needed because
// OpenFile returns a concrete *sftp.File that the interface cannot name.
type sftpRemote struct {
	*sftp.Client
	// trace logs each call and how long it took, when set.
	trace func(op, path string, d time.Duration, err error)
}

func (r sftpRemote) note(op, p string, begin time.Time, err error) {
	if r.trace != nil {
		r.trace(op, p, time.Since(begin), err)
	}
}

func (r sftpRemote) Stat(p string) (os.FileInfo, error) {
	begin := time.Now()
	fi, err := r.Client.Stat(p)
	r.note("stat", p, begin, err)
	return fi, err
}

func (r sftpRemote) ReadDir(p string) ([]os.FileInfo, error) {
	begin := time.Now()
	infos, err := r.Client.ReadDir(p)
	r.note("readdir", p, begin, err)
	return infos, err
}

func (r sftpRemote) OpenFile(p string, flags int) (remoteFile, error) {
	begin := time.Now()
	f, err := r.Client.OpenFile(p, flags)
	r.note("openfile", p, begin, err)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// TraceRemote makes a session log its calls, for when a drive feels slow.
func traceRemote(r sftpRemote, logf func(string)) sftpRemote {
	r.trace = func(op, p string, d time.Duration, err error) {
		msg := fmt.Sprintf("sftp: %-8s %-44s %s", op, p, d.Round(10*time.Microsecond))
		if err != nil {
			msg += "  " + err.Error()
		}
		logf(msg)
	}
	return r
}
