//go:build windows

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// winfs is the drive WinFsp shows for a vfs: it translates the FUSE calls into
// the file system's own, and the errors back into the codes Windows knows.
type winfs struct {
	fuse.FileSystemBase
	v *vfs

	ready chan struct{}
	once  sync.Once
	logf  func(string)
	trace bool

	slowMu   sync.Mutex
	slowNext time.Time
}

func newWinFS(v *vfs, logf func(string)) *winfs {
	return &winfs{
		v:     v,
		ready: make(chan struct{}),
		logf:  logf,
		trace: tracing(),
	}
}

// slowOp is when a call is worth a log line even with tracing off. A call this
// slow is exactly what a "the drive froze" report is about, and the line names
// the path that froze it.
const slowOp = 250 * time.Millisecond

// tracing logs how long one FUSE call took. With SSHFSMGR_TRACE it logs every
// call, which shows what Windows asks for and what it costs; without it only
// the calls slow enough to be felt are logged.
func (w *winfs) tracing(op, p string) func() {
	begin := time.Now()
	if w.trace {
		return func() {
			w.logf(fmt.Sprintf("fs: %-9s %-44s %s", op, p, time.Since(begin).Round(10*time.Microsecond)))
		}
	}
	if w.logf == nil {
		return func() {}
	}
	return func() {
		if d := time.Since(begin); d >= slowOp {
			w.slow(op, p, d)
		}
	}
}

// slow writes a slow call, a few per second at most: a crawler on a slow link
// makes hundreds of calls slow at once, and the log has to stay readable.
func (w *winfs) slow(op, p string, d time.Duration) {
	w.slowMu.Lock()
	if time.Now().Before(w.slowNext) {
		w.slowMu.Unlock()
		return
	}
	w.slowNext = time.Now().Add(200 * time.Millisecond)
	w.slowMu.Unlock()
	w.logf(fmt.Sprintf("fs: %-9s %-44s %s slow", op, p, d.Round(time.Millisecond)))
}

// Init runs once the drive is mounted, which is what Mount waits for.
func (w *winfs) Init() {
	w.once.Do(func() { close(w.ready) })
}

// Destroy runs when the drive goes away, after the last call into it.
func (w *winfs) Destroy() { w.v.close() }

func (w *winfs) Statfs(p string, st *fuse.Statfs_t) int {
	v := w.v.statfs(p)
	st.Bsize = v.Bsize
	st.Frsize = v.Frsize
	if st.Frsize == 0 {
		st.Frsize = st.Bsize
	}
	if st.Bsize == 0 {
		st.Bsize = 4096
		st.Frsize = 4096
	}
	st.Blocks = v.Blocks
	st.Bfree = v.Bfree
	st.Bavail = v.Bavail
	st.Files = v.Files
	st.Ffree = v.Ffree
	st.Favail = v.Favail
	st.Namemax = v.Namemax
	if st.Namemax == 0 {
		st.Namemax = 255
	}
	return 0
}

func (w *winfs) Getattr(p string, st *fuse.Stat_t, fh uint64) int {
	defer w.tracing("getattr", p)()
	a, _, err := w.v.stat(p)
	if err != nil {
		return errno(err)
	}
	toFuseStat(a, st)
	return 0
}

func (w *winfs) Opendir(p string) (int, uint64) {
	defer w.tracing("opendir", p)()
	// Windows opens a folder far more often to read its attributes than to
	// look inside it: every CreateFile on a directory lands here. Listing the
	// folder at this point would turn each of those into a full readdir on
	// the server, which is what makes a tree of folders crawl. Readdir lists
	// when a listing is actually wanted, and it is cached from then on.
	if a, _, err := w.v.stat(p); err != nil {
		return errno(err), ^uint64(0)
	} else if !a.dir {
		return -fuse.ENOTDIR, ^uint64(0)
	}
	return 0, 0
}

func (w *winfs) Readdir(p string, fill func(string, *fuse.Stat_t, int64) bool, ofst int64, fh uint64) int {
	defer w.tracing("readdir", p)()
	var self fuse.Stat_t
	toFuseStat(attr{dir: true, mtime: time.Now()}, &self)
	if !fill(".", &self, 0) {
		return 0
	}
	if !fill("..", nil, 0) {
		return 0
	}
	ents, err := w.v.readDir(p)
	if err != nil {
		return errno(err)
	}
	for _, e := range ents {
		var child fuse.Stat_t
		toFuseStat(e.a, &child)
		if !fill(e.name, &child, 0) {
			break
		}
	}
	return 0
}

func (w *winfs) Releasedir(p string, fh uint64) int { return 0 }

func (w *winfs) Open(p string, flags int) (int, uint64) {
	return w.open(p, flags, false, 0)
}

func (w *winfs) Create(p string, flags int, mode uint32) (int, uint64) {
	return w.open(p, flags, true, os.FileMode(mode))
}

func (w *winfs) open(p string, flags int, create bool, mode os.FileMode) (int, uint64) {
	op := "open"
	if create {
		op = "create"
	}
	defer w.tracing(op, p)()
	fh, err := w.v.open(p, flags, create, mode)
	if err != nil {
		w.debugf("open %s: %v", p, err)
		return errno(err), ^uint64(0)
	}
	return 0, fh
}

func (w *winfs) Read(p string, buf []byte, off int64, fh uint64) int {
	defer w.tracing("read", p)()
	n, err := w.v.read(fh, off, buf)
	if err != nil {
		return errno(err)
	}
	return n
}

func (w *winfs) Write(p string, buf []byte, off int64, fh uint64) int {
	defer w.tracing("write", p)()
	n, err := w.v.write(fh, off, buf)
	if err != nil {
		return errno(err)
	}
	return n
}

func (w *winfs) Flush(p string, fh uint64) int {
	if err := w.v.flush(fh); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Fsync(p string, datasync bool, fh uint64) int {
	if err := w.v.fsync(fh); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Release(p string, fh uint64) int {
	defer w.tracing("release", p)()
	if err := w.v.release(fh); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Truncate(p string, size int64, fh uint64) int {
	defer w.tracing("truncate", p)()
	if err := w.v.truncate(p, size, fh); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Mkdir(p string, mode uint32) int {
	defer w.tracing("mkdir", p)()
	if err := w.v.mkdir(p, os.FileMode(mode)); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Rmdir(p string) int {
	if err := w.v.rmdir(p); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Unlink(p string) int {
	defer w.tracing("unlink", p)()
	if err := w.v.unlink(p); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Rename(oldp, newp string) int {
	defer w.tracing("rename", oldp+" -> "+newp)()
	if err := w.v.rename(oldp, newp); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Chmod(p string, mode uint32) int {
	if err := w.v.chmod(p, os.FileMode(mode)); err != nil {
		return errno(err)
	}
	return 0
}

// Chown does nothing: an SFTP server owns its files as the account it runs
// under, and Windows asks for this on every copy.
func (w *winfs) Chown(p string, uid, gid uint32) int { return 0 }

func (w *winfs) Utimens(p string, tmsp []fuse.Timespec) int {
	var atime, mtime time.Time
	if len(tmsp) > 0 {
		atime, _ = fuseTime(tmsp[0])
	}
	if len(tmsp) > 1 {
		mtime, _ = fuseTime(tmsp[1])
	}
	if err := w.v.utimens(p, atime, mtime); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Access(p string, mask uint32) int {
	if mask&fuse.W_OK != 0 && w.v.readOnly {
		return -fuse.EROFS
	}
	if _, _, err := w.v.stat(p); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) Readlink(p string) (int, string) {
	target, err := w.v.readlink(p)
	if err != nil {
		return errno(err), ""
	}
	return 0, target
}

func (w *winfs) Symlink(target, p string) int {
	if err := w.v.symlink(target, p); err != nil {
		return errno(err)
	}
	return 0
}

func (w *winfs) debugf(format string, args ...any) {
	if w.logf != nil {
		w.logf(fmt.Sprintf("fs: "+format, args...))
	}
}

// toFuseStat fills the attributes Windows sees. Permissions are handed out as
// 0777, the way sshfs does: the server enforces what it enforces, and Windows
// only cares that it may write.
func toFuseStat(a attr, st *fuse.Stat_t) {
	if a.dir {
		st.Mode = fuse.S_IFDIR | 0o777
		st.Nlink = 2
	} else {
		st.Mode = fuse.S_IFREG | 0o777
		st.Nlink = 1
	}
	st.Size = a.size
	st.Blksize = 4096
	if a.size > 0 {
		st.Blocks = (a.size + 511) / 512
	}
	st.Atim = fuse.NewTimespec(a.mtime)
	st.Mtim = st.Atim
	st.Ctim = st.Atim
	st.Birthtim = st.Atim
}

// fuseTime reads a timestamp, honouring the two markers FUSE sends instead.
func fuseTime(ts fuse.Timespec) (time.Time, bool) {
	switch ts.Nsec {
	case fuse.UTIME_OMIT:
		return time.Time{}, false
	case fuse.UTIME_NOW:
		return time.Now(), true
	}
	return time.Unix(ts.Sec, ts.Nsec), true
}

// errno turns a Go error into the negative code FUSE reports.
func errno(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errReadOnly):
		return -fuse.EROFS
	case errors.Is(err, os.ErrNotExist):
		return -fuse.ENOENT
	case errors.Is(err, os.ErrPermission):
		return -fuse.EACCES
	case errors.Is(err, os.ErrExist):
		return -fuse.EEXIST
	case errors.Is(err, errIsDir):
		return -fuse.EISDIR
	case errors.Is(err, errNotDir):
		return -fuse.ENOTDIR
	case errors.Is(err, errNoHandle):
		return -fuse.EBADF
	case errors.Is(err, errDirInUse):
		return -fuse.ENOTEMPTY
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return -fuse.ETIMEDOUT
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return -fuse.EIO
	}
	return -fuse.EIO
}
