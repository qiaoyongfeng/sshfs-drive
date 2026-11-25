package main

import (
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// memRemote is a server small enough to keep in a map: it answers the same
// calls an SFTP session does, so the file system can be tested without a
// network.
type memRemote struct {
	mu    sync.Mutex
	files map[string][]byte
	mtime map[string]time.Time
	dirs  map[string]bool

	statCalls  int
	statPaths  []string
	dirCalls   int
	openCalls  int
	closeCalls int
	statvfs    *sftp.StatVFS
	closed     bool
}

func newMemRemote() *memRemote {
	r := &memRemote{
		files: map[string][]byte{},
		mtime: map[string]time.Time{},
		dirs:  map[string]bool{"/": true},
	}
	return r
}

// putFile adds a file and the directories above it.
func (r *memRemote) putFile(p string, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mkdirAllLocked(path.Dir(p))
	r.files[p] = []byte(data)
	r.mtime[p] = time.Unix(1700000000, 0)
}

func (r *memRemote) putDir(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mkdirAllLocked(p)
}

func (r *memRemote) mkdirAllLocked(p string) {
	for p != "/" && p != "." && p != "" {
		r.dirs[p] = true
		p = path.Dir(p)
	}
	r.dirs["/"] = true
}

func (r *memRemote) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *memRemote) Stat(p string) (os.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statCalls++
	r.statPaths = append(r.statPaths, p)
	return r.infoLocked(p)
}

// counts reads the round-trip tally under the lock, for checks that follow a
// request the file system made in the background.
func (r *memRemote) counts() (stats, dirs int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statCalls, r.dirCalls
}

func (r *memRemote) infoLocked(p string) (os.FileInfo, error) {
	if r.dirs[p] {
		return memInfo{name: path.Base(p), dir: true, mtime: time.Unix(1700000000, 0)}, nil
	}
	if data, ok := r.files[p]; ok {
		return memInfo{name: path.Base(p), size: int64(len(data)), mtime: r.mtime[p]}, nil
	}
	return nil, os.ErrNotExist
}

func (r *memRemote) ReadDir(p string) ([]os.FileInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirCalls++
	if !r.dirs[p] {
		return nil, os.ErrNotExist
	}
	prefix := strings.TrimSuffix(p, "/") + "/"
	var out []os.FileInfo
	for name := range r.files {
		if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			out = append(out, memInfo{name: path.Base(name), size: int64(len(r.files[name])), mtime: r.mtime[name]})
		}
	}
	for name := range r.dirs {
		if name != p && strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			out = append(out, memInfo{name: path.Base(name), dir: true, mtime: time.Unix(1700000000, 0)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (r *memRemote) ReadLink(p string) (string, error) { return "", os.ErrNotExist }

func (r *memRemote) Symlink(oldname, newname string) error { return errors.New("no symlinks here") }

func (r *memRemote) Mkdir(p string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirs[p] {
		return os.ErrExist
	}
	if !r.dirs[path.Dir(p)] {
		return os.ErrNotExist
	}
	r.dirs[p] = true
	return nil
}

func (r *memRemote) Remove(p string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.files[p]; !ok {
		return os.ErrNotExist
	}
	delete(r.files, p)
	delete(r.mtime, p)
	return nil
}

func (r *memRemote) RemoveDirectory(p string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirs[p] {
		return os.ErrNotExist
	}
	prefix := strings.TrimSuffix(p, "/") + "/"
	for name := range r.files {
		if strings.HasPrefix(name, prefix) {
			return errors.New("directory is not empty")
		}
	}
	for name := range r.dirs {
		if name != p && strings.HasPrefix(name, prefix) {
			return errors.New("directory is not empty")
		}
	}
	delete(r.dirs, p)
	return nil
}

func (r *memRemote) Rename(oldname, newname string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.files[newname]; ok {
		return os.ErrExist
	}
	return r.moveLocked(oldname, newname)
}

func (r *memRemote) PosixRename(oldname, newname string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.moveLocked(oldname, newname)
}

func (r *memRemote) moveLocked(oldname, newname string) error {
	if data, ok := r.files[oldname]; ok {
		if !r.dirs[path.Dir(newname)] {
			return os.ErrNotExist
		}
		r.files[newname] = data
		r.mtime[newname] = r.mtime[oldname]
		delete(r.files, oldname)
		delete(r.mtime, oldname)
		return nil
	}
	return os.ErrNotExist
}

func (r *memRemote) Chmod(p string, mode os.FileMode) error { return nil }

func (r *memRemote) Chtimes(p string, atime, mtime time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.files[p]; !ok {
		return os.ErrNotExist
	}
	r.mtime[p] = mtime
	return nil
}

func (r *memRemote) Truncate(p string, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, ok := r.files[p]
	if !ok {
		return os.ErrNotExist
	}
	r.files[p] = resize(data, size)
	return nil
}

func (r *memRemote) StatVFS(p string) (*sftp.StatVFS, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statvfs, nil
}

func (r *memRemote) OpenFile(p string, flags int) (remoteFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openCalls++
	data, exists := r.files[p]
	if !exists {
		if flags&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		if flags&os.O_EXCL != 0 {
			return nil, os.ErrExist
		}
		if !r.dirs[path.Dir(p)] {
			return nil, os.ErrNotExist
		}
		r.files[p] = []byte{}
		r.mtime[p] = time.Now()
		data = r.files[p]
	} else if flags&os.O_TRUNC != 0 && flags&os.O_RDONLY != flags {
		r.files[p] = []byte{}
		data = r.files[p]
	}
	_ = data
	return &memHandle{r: r, path: p}, nil
}

type memHandle struct {
	r    *memRemote
	path string
}

func (h *memHandle) ReadAt(p []byte, off int64) (int, error) {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	data, ok := h.r.files[h.path]
	if !ok {
		return 0, os.ErrNotExist
	}
	if off >= int64(len(data)) {
		return 0, errEOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, errEOF
	}
	return n, nil
}

func (h *memHandle) WriteAt(p []byte, off int64) (int, error) {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	data, ok := h.r.files[h.path]
	if !ok {
		return 0, os.ErrNotExist
	}
	if need := int(off) + len(p); need > len(data) {
		grown := make([]byte, need)
		copy(grown, data)
		data = grown
	}
	copy(data[off:], p)
	h.r.files[h.path] = data
	h.r.mtime[h.path] = time.Now()
	return len(p), nil
}

func (h *memHandle) Stat() (os.FileInfo, error) {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	return h.r.infoLocked(h.path)
}

func (h *memHandle) Truncate(size int64) error { return h.r.Truncate(h.path, size) }

func (h *memHandle) Sync() error { return nil }

func (h *memHandle) Close() error {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	h.r.closeCalls++
	return nil
}

var errEOF = errors.New("EOF")

type memInfo struct {
	name  string
	size  int64
	dir   bool
	mtime time.Time
}

func (i memInfo) Name() string { return i.name }
func (i memInfo) Size() int64  { return i.size }
func (i memInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (i memInfo) ModTime() time.Time { return i.mtime }
func (i memInfo) IsDir() bool        { return i.dir }
func (i memInfo) Sys() any           { return nil }

func resize(data []byte, size int64) []byte {
	out := make([]byte, size)
	copy(out, data)
	return out
}

// testVFS is a file system on a memory server, with a clock the test drives.
func testVFS(t *testing.T, root string, readOnly bool) (*vfs, *memRemote) {
	t.Helper()
	r := newMemRemote()
	v := newVFS(r, root, readOnly, 20*time.Second)
	now := time.Unix(1700000000, 0)
	v.now = func() time.Time { return now }
	return v, r
}

func TestVFSMapsTheDriveRootToTheRemoteFolder(t *testing.T) {
	v, r := testVFS(t, "/home/ada", false)
	r.putFile("/home/ada/docs/readme.md", "hello")

	a, remote, err := v.stat("/docs/readme.md")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if remote != "/home/ada/docs/readme.md" {
		t.Errorf("resolved to %q", remote)
	}
	if a.size != 5 || a.dir {
		t.Errorf("attr %+v", a)
	}

	if a, _, err := v.stat("/"); err != nil || !a.dir {
		t.Fatalf("the root is not a directory: %+v, %v", a, err)
	}

	if err := v.mkdir("/new", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !r.dirs["/home/ada/new"] {
		t.Errorf("mkdir made the wrong directory: %v", keys(r.dirs))
	}
}

func TestVFSCachesAttributesUntilTheTimeout(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/a.txt", "x")

	if _, _, err := v.stat("/a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.stat("/a.txt"); err != nil {
		t.Fatal(err)
	}
	if r.statCalls != 1 {
		t.Errorf("%d stat calls for one path, want 1", r.statCalls)
	}

	// Past the timeout the answer is still handed over at once, and the server
	// is asked again behind the caller's back: nobody waits for a re-confirm.
	v.now = func() time.Time { return time.Unix(1700000000+30, 0) }
	if _, _, err := v.stat("/a.txt"); err != nil {
		t.Fatal(err)
	}
	if stats, _ := r.counts(); stats != 1 {
		t.Errorf("%d stat calls before the refresh ran, want 1", stats)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if stats, _ := r.counts(); stats >= 2 {
			break
		}
		if time.Now().After(deadline) {
			stats, _ := r.counts()
			t.Fatalf("%d stat calls, want 2 after the refresh", stats)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An aged listing is still the best answer at hand: it is handed over at once
// and refreshed behind the caller. Waiting for the server would freeze a folder
// of a few thousand entries for a round trip per entry, which on a network
// drive is what "Explorer stopped responding" looks like.
func TestVFSServesAnAgedListingAndRefreshesIt(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/a.txt", "x")
	if _, err := v.readDir("/"); err != nil {
		t.Fatalf("list: %v", err)
	}
	_, dirs := r.counts()

	v.now = func() time.Time { return time.Unix(1700000000+30, 0) }
	ents, err := v.readDir("/")
	if err != nil {
		t.Fatalf("aged list: %v", err)
	}
	if len(ents) == 0 {
		t.Error("the aged listing came back empty")
	}
	if _, dirs2 := r.counts(); dirs2 != dirs {
		t.Errorf("the caller waited for a re-list: %d listings, want %d", dirs2, dirs)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if _, dirs2 := r.counts(); dirs2 > dirs {
			return
		}
		if time.Now().After(deadline) {
			_, dirs2 := r.counts()
			t.Fatalf("%d listings, want a refresh after %d", dirs2, dirs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestVFSCachesMisses(t *testing.T) {
	v, r := testVFS(t, "/", false)
	if _, _, err := v.stat("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat of a missing file: %v", err)
	}
	if _, _, err := v.stat("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second stat: %v", err)
	}
	// One to learn it is missing, one to look for another spelling of it.
	if r.statCalls > 2 {
		t.Errorf("%d stat calls, want at most 2", r.statCalls)
	}
}

// A folder Windows has listed already names everything in it, so the names the
// shell goes on to probe there - desktop.ini, Thumbs.db, a working copy's .git -
// are answered without a round trip. On a slow link that is the difference
// between a menu that opens and one that hangs.
func TestVFSAnswersProbesFromTheListing(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/notes.txt", "x")
	r.putDir("/docs")

	if _, err := v.readDir("/"); err != nil {
		t.Fatalf("list: %v", err)
	}
	stats, dirs := r.statCalls, r.dirCalls

	for _, name := range []string{"desktop.ini", "Thumbs.db", ".git", "HEAD", "missing"} {
		if _, _, err := v.stat("/" + name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	if r.statCalls != stats || r.dirCalls != dirs {
		t.Errorf("%d stat and %d readdir calls for probes a listing answers, want none",
			r.statCalls-stats, r.dirCalls-dirs)
	}

	// A name that is there comes back with the case the server uses.
	a, canon, err := v.stat("/NOTES.TXT")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if canon != "/notes.txt" || a.size != 1 {
		t.Errorf("resolved to %q, attr %+v", canon, a)
	}
	if r.statCalls != stats {
		t.Errorf("a lookup the listing answers asked the server")
	}
}

// One lookup may cost one stat per path, never two: the case-insensitive
// second chance must not ask again for the name that was just refused.
func TestVFSAsksOnceForAMissingName(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putDir("/docs")
	if _, _, err := v.stat("/docs/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat: %v", err)
	}
	seen := map[string]int{}
	for _, p := range r.statPaths {
		seen[p]++
	}
	if len(seen) == 0 {
		t.Fatal("the lookup never asked the server")
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("%s was stat-ed %d times for one lookup", p, n)
		}
	}
}

func TestVFSFoldsTheCaseOfNames(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/Docs/README.md", "hello")

	// Explorer lists a folder before it opens anything in it, and folding
	// only uses listings already at hand: a probe of a name that does not
	// exist must not cost a round trip.
	if _, err := v.readDir("/"); err != nil {
		t.Fatalf("list: %v", err)
	}

	a, remote, err := v.stat("/docs/readme.md")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if a.size != 5 {
		t.Errorf("attr %+v", a)
	}
	if remote != "/Docs/README.md" {
		t.Errorf("resolved to %q, want the server's spelling", remote)
	}

	if _, _, err := v.stat("/DOCS"); err != nil {
		t.Errorf("the directory itself does not fold: %v", err)
	}

	ents, err := v.readDir("/docs")
	if err != nil || len(ents) != 1 || ents[0].name != "README.md" {
		t.Fatalf("readDir: %v %+v", err, ents)
	}
}

func TestVFSWritesAndReads(t *testing.T) {
	v, r := testVFS(t, "/", false)

	fh, err := v.open("/notes.txt", flagReadWrite|flagCreate, true, 0o644)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, err := v.write(fh, 0, []byte("hello")); err != nil || n != 5 {
		t.Fatalf("write: %d, %v", n, err)
	}
	if n, err := v.write(fh, 5, []byte(" world")); err != nil || n != 6 {
		t.Fatalf("append: %d, %v", n, err)
	}
	if err := v.release(fh); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := string(r.files["/notes.txt"]); got != "hello world" {
		t.Errorf("the server holds %q", got)
	}

	fh, err = v.open("/notes.txt", 0, false, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer v.release(fh)
	buf := make([]byte, 5)
	if n, err := v.read(fh, 6, buf); err != nil && err != errEOF {
		t.Fatalf("read: %v", err)
	} else if string(buf[:n]) != "world" {
		t.Errorf("read %q", buf[:n])
	}

	// A stat right after a write must not need the server.
	before := r.statCalls
	if a, _, err := v.stat("/notes.txt"); err != nil || a.size != 11 {
		t.Fatalf("stat after write: %+v, %v", a, err)
	}
	if r.statCalls != before {
		t.Errorf("stat after a write asked the server again")
	}
}

func TestVFSKeepsHandlesApart(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/shared.txt", "content")

	a, err := v.open("/shared.txt", 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.open("/shared.txt", 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("both opens returned the same handle")
	}
	buf := make([]byte, 3)
	for _, fh := range []uint64{a, b} {
		if n, _ := v.read(fh, 0, buf); string(buf[:n]) != "con" {
			t.Errorf("handle %d read %q", fh, buf[:n])
		}
	}
	v.release(a)
	v.release(b)
	if err := v.release(a); err == nil {
		t.Error("releasing a closed handle succeeded")
	}
}

func TestVFSTruncates(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/big.txt", "0123456789")

	if err := v.truncate("/big.txt", 4, 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if got := string(r.files["/big.txt"]); got != "0123" {
		t.Errorf("the server holds %q", got)
	}
}

func TestVFSRenameReplacesTheTarget(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/a.txt", "new")
	r.putFile("/b.txt", "old")

	if err := v.rename("/a.txt", "/b.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := string(r.files["/b.txt"]); got != "new" {
		t.Errorf("b.txt holds %q", got)
	}
	if _, ok := r.files["/a.txt"]; ok {
		t.Error("a.txt is still there")
	}
}

func TestVFSReadOnlyRefusesEveryChange(t *testing.T) {
	v, _ := testVFS(t, "/", true)
	if _, err := v.open("/x", flagReadWrite|flagCreate, true, 0o644); !errors.Is(err, errReadOnly) {
		t.Errorf("create: %v", err)
	}
	if err := v.unlink("/x"); !errors.Is(err, errReadOnly) {
		t.Errorf("unlink: %v", err)
	}
	if err := v.mkdir("/d", 0o755); !errors.Is(err, errReadOnly) {
		t.Errorf("mkdir: %v", err)
	}
	if err := v.rename("/a", "/b"); !errors.Is(err, errReadOnly) {
		t.Errorf("rename: %v", err)
	}
}

func TestVFSStatfsFallsBackWhenTheServerHasNoAnswer(t *testing.T) {
	v, r := testVFS(t, "/", false)
	st := v.statfs("/")
	if st == nil || st.Bsize != 4096 {
		t.Fatalf("statfs %+v", st)
	}

	r.statvfs = &sftp.StatVFS{Bsize: 8192, Blocks: 100, Bfree: 40, Namemax: 255}
	// The fallback is cached for a little while; step past it.
	v.now = func() time.Time { return time.Unix(1700000000+60, 0) }
	st = v.statfs("/")
	if st.Bsize != 8192 || st.Bfree != 40 {
		t.Errorf("statfs did not use the server's numbers: %+v", st)
	}
}

// Windows opens a file to read its attributes far more often than it reads
// its contents. Those opens must not reach the server, or listing a folder
// costs a round trip per entry.
func TestVFSOpensTheServerFileOnlyWhenItIsUsed(t *testing.T) {
	v, r := testVFS(t, "/", false)
	r.putFile("/notes.txt", "hello")

	fh, err := v.open("/notes.txt", 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.openCalls != 0 {
		t.Errorf("opening for attributes opened the server file %d times", r.openCalls)
	}
	if err := v.release(fh); err != nil {
		t.Fatal(err)
	}
	if r.closeCalls != 0 {
		t.Errorf("releasing an unused handle closed the server file %d times", r.closeCalls)
	}

	fh, err = v.open("/notes.txt", 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if n, err := v.read(fh, 0, buf); err != nil || n != 5 {
		t.Fatalf("read: %d, %v", n, err)
	}
	if r.openCalls != 1 {
		t.Errorf("reading did not open the server file: %d", r.openCalls)
	}
	if err := v.release(fh); err != nil {
		t.Fatal(err)
	}
	if r.closeCalls != 1 {
		t.Errorf("releasing closed %d server files, want 1", r.closeCalls)
	}
}

func TestVFSClosesWhatItOpened(t *testing.T) {
	v, r := testVFS(t, "/", false)
	if _, err := v.open("/a", flagReadWrite|flagCreate, true, 0o644); err != nil {
		t.Fatal(err)
	}
	v.close()
	v.close() // once, however many times it is called
	if !r.closed {
		t.Error("the session was not closed")
	}
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
