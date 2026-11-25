package main

import (
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// defaultCacheTTL is how long an attribute stays good. Windows asks for the
// same handful of paths over and over (Explorer, an editor saving a file), and
// every answer would otherwise be a round trip to the server.
const defaultCacheTTL = 20 * time.Second

// maxCachedPaths bounds the attribute cache; a huge tree would otherwise grow
// it without limit.
const maxCachedPaths = 8192

// FUSE open flags, repeated here so the file system does not depend on the
// platform package and stays testable everywhere.
const (
	flagWriteOnly = 0x0001
	flagReadWrite = 0x0002
	flagAppend    = 0x0008
	flagCreate    = 0x0100
	flagTruncate  = 0x0200
	flagExclusive = 0x0400
)

var (
	errReadOnly  = errors.New("the mount is read-only")
	errIsDir     = errors.New("is a directory")
	errNotDir    = errors.New("not a directory")
	errNoHandle  = errors.New("no such open file")
	errDirInUse  = errors.New("directory is not empty")
	errNoSymlink = errors.New("symlinks are not supported")
)

// attr is what the cache remembers about a path: enough to answer a stat
// without asking the server again.
type attr struct {
	dir   bool
	size  int64
	mtime time.Time
	mode  os.FileMode
}

func attrOf(fi os.FileInfo) attr {
	return attr{dir: fi.IsDir(), size: fi.Size(), mtime: fi.ModTime(), mode: fi.Mode()}
}

type attrEntry struct {
	a      attr
	absent bool
	exp    time.Time
}

type dirent struct {
	name string
	a    attr
}

type dirEntry struct {
	ents []dirent
	exp  time.Time
}

// openFile is a handle Windows holds. The server file behind it is opened on
// first use rather than here: Windows opens a file just to read its
// attributes, and a round trip for that is what makes browsing a server feel
// slow. An untouched handle costs nothing, and its release costs nothing.
type openFile struct {
	mu    sync.Mutex
	f     remoteFile
	path  string // the server path, with the case the server uses
	flags int    // what the first real read or write should ask for
}

// ensure opens the server file if this handle has not needed it yet.
func (o *openFile) ensure(v *vfs) (remoteFile, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.f != nil {
		return o.f, nil
	}
	f, err := v.remote.OpenFile(o.path, o.flags)
	if err != nil {
		return nil, translate(err)
	}
	o.f = f
	return f, nil
}

// close shuts the server file if it was ever opened.
func (o *openFile) close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.f == nil {
		return nil
	}
	err := o.f.Close()
	o.f = nil
	return translate(err)
}

// vfs is one mount's view of a remote tree: it maps the paths a drive sees to
// the paths the server knows, caches attributes, and keeps the open handles
// Windows expects to be able to share.
type vfs struct {
	root     string // remote directory the drive shows; "/" or "/home/ada"
	readOnly bool
	ttl      time.Duration
	remote   remote
	now      func() time.Time

	mu    sync.Mutex
	attrs map[string]attrEntry
	dirs  map[string]dirEntry
	files map[uint64]*openFile
	next  uint64

	// running is the set of requests in flight, by path. Several things ask
	// for the same folder at the same moment (Explorer, an overlay provider,
	// the indexer), and one round trip is enough for all of them.
	running map[string]*flight
	closed  bool

	// volume is the free-space answer, kept because Windows asks often.
	volume    *sftp.StatVFS
	volumeExp time.Time

	closeOnce sync.Once
}

// flight is one request another caller can wait for.
type flight struct {
	done chan struct{}
}

func newVFS(r remote, root string, readOnly bool, ttl time.Duration) *vfs {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &vfs{
		root:     path.Clean("/" + root),
		readOnly: readOnly,
		ttl:      ttl,
		remote:   r,
		now:      time.Now,
		attrs:    make(map[string]attrEntry),
		dirs:     make(map[string]dirEntry),
		files:    make(map[uint64]*openFile),
		running:  make(map[string]*flight),
	}
}

// mountPath turns what the drive shows ("/src/main.go") into what the server
// knows ("/home/ada/src/main.go").
func (v *vfs) mountPath(p string) string {
	p = path.Clean("/" + p)
	if v.root == "/" {
		return p
	}
	if p == "/" {
		return v.root
	}
	return v.root + p
}

// stat answers a lookup, from the cache when it can. It also returns the server
// path the answer belongs to, which differs from the requested one when the
// server's spelling of the name has another case.
//
// An answer past its time is still handed over at once, with a refresh started
// behind the caller. Waiting for the server to re-confirm what was already
// known is what makes a folder with thousands of entries freeze Explorer every
// time the cache ages out: it asks for all of them again, and each answer costs
// a round trip on a link where a round trip is a tenth of a second.
func (v *vfs) stat(p string) (attr, string, error) {
	full := v.mountPath(p)
	if a, absent, ok, fresh := v.cached(full); ok {
		if !fresh {
			v.refreshStat(full)
		}
		if absent {
			return attr{}, "", os.ErrNotExist
		}
		return a, full, nil
	}

	// The folder was listed a while ago, and a listing names everything in it
	// along with the case the server uses. Windows probes far more names than
	// it opens (desktop.ini, Thumbs.db, .git, a menu's worth of shell
	// extensions), and answering each of those from the listing is what keeps
	// a folder on a slow link from feeling blocked.
	if name, a, found, known, fresh := v.fromListing(full); known {
		if !fresh {
			v.refreshList(path.Dir(full))
		}
		if !found {
			v.rememberAbsent(full)
			return attr{}, "", os.ErrNotExist
		}
		canon := path.Join(path.Dir(full), name)
		v.remember(full, a)
		if canon != full {
			v.remember(canon, a)
		}
		return a, canon, nil
	}

	a, err := v.statServer(full)
	if err == nil {
		return a, full, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		if canon, a2, ok := v.foldCase(p, full); ok {
			v.remember(full, a2)
			if canon != full {
				v.remember(canon, a2)
			}
			return a2, canon, nil
		}
	}
	return attr{}, "", err
}

// fromListing looks a name up in its folder's cached listing. known is false
// when the folder has no listing to answer from, which is the only case where
// the server has to be asked. fresh says the listing is recent enough to be
// trusted without a refresh.
func (v *vfs) fromListing(full string) (name string, a attr, found, known, fresh bool) {
	dir := path.Dir(full)
	if dir == full {
		return "", attr{}, false, false, false
	}
	ents, ok, fresh := v.listed(dir)
	if !ok {
		return "", attr{}, false, false, false
	}
	want := path.Base(full)
	for _, e := range ents {
		if e.name == want {
			return e.name, e.a, true, true, fresh
		}
	}
	for _, e := range ents {
		if strings.EqualFold(e.name, want) {
			return e.name, e.a, true, true, fresh
		}
	}
	return "", attr{}, false, true, fresh
}

// listed returns a folder's cached listing, without going to the server. An
// aged listing is returned too, with fresh false: it is still the best answer
// at hand.
func (v *vfs) listed(dir string) (ents []dirent, ok, fresh bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.dirs[dir]
	if !ok {
		return nil, false, false
	}
	return e.ents, true, !v.now().After(e.exp)
}

func (v *vfs) statRemote(full string) (attr, error) {
	fi, err := v.remote.Stat(full)
	if err != nil {
		return attr{}, translate(err)
	}
	return attrOf(fi), nil
}

// statServer asks the server and records the answer. Concurrent callers for
// the same path share one request.
func (v *vfs) statServer(full string) (attr, error) {
	var a attr
	var err error
	if v.once("stat:"+full, func() {
		a, err = v.statRemote(full)
		switch {
		case err == nil:
			v.remember(full, a)
		case errors.Is(err, os.ErrNotExist):
			v.rememberAbsent(full)
		}
	}) {
		return a, err
	}
	// Someone else was already asking; use what they found, and only ask
	// again if they found nothing usable.
	if a, absent, ok, _ := v.cached(full); ok {
		if absent {
			return attr{}, os.ErrNotExist
		}
		return a, nil
	}
	a, err = v.statRemote(full)
	if err == nil {
		v.remember(full, a)
	} else if errors.Is(err, os.ErrNotExist) {
		v.rememberAbsent(full)
	}
	return a, err
}

// foldCase finds the names the server has for a path Windows spelled with
// another case: "readme.md" resolves to "/Docs/README.md". Listing already at
// hand answers both halves of the question - is the name there, and how does
// the server spell it - so walking down a folder Windows has listed costs no
// round trips. known is the path the caller has just been told does not exist.
func (v *vfs) foldCase(p, known string) (string, attr, bool) {
	rest := strings.TrimPrefix(path.Clean("/"+p), "/")
	if rest == "" {
		return "", attr{}, false
	}
	cur := v.root
	var found attr
	for _, part := range strings.Split(rest, "/") {
		next := path.Join(cur, part)
		// A component already answered is not asked again: probing a deep
		// name that does not exist would otherwise cost a round trip per
		// directory on the way down.
		if a, absent, ok, fresh := v.cached(next); ok {
			if !fresh {
				v.refreshStat(next)
			}
			if absent {
				return "", attr{}, false
			}
			cur, found = next, a
			continue
		}
		ents, listed, fresh := v.listed(cur)
		if !listed && next != known {
			if fi, err := v.remote.Stat(next); err == nil {
				cur, found = next, attrOf(fi)
				continue
			}
		}
		if !listed {
			// The listing is cached for a while, and creations edit it in
			// place, so probing names that do not exist does not list the
			// directory again each time.
			var err error
			if ents, err = v.list(cur); err != nil {
				return "", attr{}, false
			}
		} else if !fresh {
			v.refreshList(cur)
		}
		name, a, ok := entryFor(ents, part, next == known)
		if !ok {
			return "", attr{}, false
		}
		cur, found = path.Join(cur, name), a
	}
	return cur, found, true
}

// entryFor finds a name in a listing: its exact spelling first, the server's
// case for it after that. gone says the name has just been reported missing,
// which only a differently spelled entry can explain.
func entryFor(ents []dirent, part string, gone bool) (string, attr, bool) {
	for _, e := range ents {
		if e.name == part {
			if gone {
				return "", attr{}, false
			}
			return e.name, e.a, true
		}
	}
	for _, e := range ents {
		if strings.EqualFold(e.name, part) {
			return e.name, e.a, true
		}
	}
	return "", attr{}, false
}

func (v *vfs) cached(full string) (a attr, absent bool, ok bool, fresh bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.attrs[full]
	if !ok {
		return attr{}, false, false, false
	}
	return e.a, e.absent, true, !v.now().After(e.exp)
}

func (v *vfs) remember(full string, a attr) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.attrs) >= maxCachedPaths {
		v.attrs = make(map[string]attrEntry, maxCachedPaths/2)
	}
	v.attrs[full] = attrEntry{a: a, exp: v.now().Add(v.ttl)}
}

func (v *vfs) rememberAbsent(full string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.attrs) >= maxCachedPaths {
		v.attrs = make(map[string]attrEntry, maxCachedPaths/2)
	}
	v.attrs[full] = attrEntry{absent: true, exp: v.now().Add(v.ttl)}
}

// forget drops what the cache knows about a path and anything below it.
func (v *vfs) forget(full string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.attrs, full)
	delete(v.dirs, full)
	prefix := full + "/"
	for k := range v.attrs {
		if strings.HasPrefix(k, prefix) {
			delete(v.attrs, k)
		}
	}
	for k := range v.dirs {
		if strings.HasPrefix(k, prefix) {
			delete(v.dirs, k)
		}
	}
}

// addEntry keeps a cached listing in step with a creation, so the folder
// still answers lookups and Explorer's refresh sees the new name.
func (v *vfs) addEntry(dir, name string, a attr) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.dirs[dir]
	if !ok {
		return
	}
	for i, d := range e.ents {
		if strings.EqualFold(d.name, name) {
			e.ents[i] = dirent{name: name, a: a}
			v.dirs[dir] = e
			return
		}
	}
	e.ents = append(e.ents, dirent{name: name, a: a})
	v.dirs[dir] = e
}

// dropEntry takes a name out of a cached listing.
func (v *vfs) dropEntry(dir, name string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.dirs[dir]
	if !ok {
		return
	}
	for i, d := range e.ents {
		if strings.EqualFold(d.name, name) {
			e.ents = append(e.ents[:i], e.ents[i+1:]...)
			v.dirs[dir] = e
			return
		}
	}
}

// wrote records the size a handle knows about, so a stat right after a write
// does not need the server.
func (v *vfs) wrote(full string, size int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.attrs[full]
	if !ok || e.absent {
		return
	}
	if size > e.a.size {
		e.a.size = size
	}
	e.a.mtime = v.now()
	e.exp = v.now().Add(v.ttl)
	v.attrs[full] = e
}

// resized records the size a truncate left behind, which may be smaller.
func (v *vfs) resized(full string, size int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.attrs[full]
	if !ok || e.absent {
		return
	}
	e.a.size = size
	e.a.mtime = v.now()
	e.exp = v.now().Add(v.ttl)
	v.attrs[full] = e
}

// list returns a directory's entries, cached briefly. Reading a directory is
// the one call Windows makes in bursts, and it is also what case folding and
// the negative cache lean on. Like stat, it hands over what it has when the
// listing has aged, and refreshes it behind the caller.
func (v *vfs) list(dir string) ([]dirent, error) {
	v.mu.Lock()
	if e, ok := v.dirs[dir]; ok {
		fresh := !v.now().After(e.exp)
		ents := e.ents
		v.mu.Unlock()
		if !fresh {
			v.refreshList(dir)
		}
		return ents, nil
	}
	if e, ok := v.attrs[dir]; ok && e.absent {
		fresh := !v.now().After(e.exp)
		v.mu.Unlock()
		if !fresh {
			v.refreshStat(dir)
		}
		return nil, os.ErrNotExist
	}
	v.mu.Unlock()
	return v.listServer(dir)
}

// listServer reads a directory from the server and caches what it holds.
// Concurrent callers for the same folder share one request.
func (v *vfs) listServer(dir string) ([]dirent, error) {
	var ents []dirent
	var err error
	if !v.once("list:"+dir, func() {
		ents, err = v.readRemote(dir)
	}) {
		if e, ok, _ := v.listed(dir); ok {
			return e, nil
		}
		ents, err = v.readRemote(dir)
	}
	return ents, err
}

// readRemote is the round trip itself: the folder, with the attributes of
// everything in it, in one request.
func (v *vfs) readRemote(dir string) ([]dirent, error) {
	infos, err := v.remote.ReadDir(dir)
	if err != nil {
		// The spelling is not remembered as missing here: the folder may
		// be there under another case, which folding sorts out.
		return nil, translate(err)
	}
	ents := make([]dirent, 0, len(infos))
	for _, fi := range infos {
		a := attrOf(fi)
		ents = append(ents, dirent{name: fi.Name(), a: a})
		v.remember(path.Join(dir, fi.Name()), a)
	}

	v.mu.Lock()
	if len(v.dirs) >= maxCachedPaths {
		v.dirs = make(map[string]dirEntry, maxCachedPaths/2)
	}
	v.dirs[dir] = dirEntry{ents: ents, exp: v.now().Add(v.ttl)}
	v.mu.Unlock()
	return ents, nil
}

// once runs do unless an identical request is already under way, in which case
// it waits for that one instead. It reports whether do ran here.
func (v *vfs) once(key string, do func()) bool {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return false
	}
	if f, ok := v.running[key]; ok {
		v.mu.Unlock()
		<-f.done
		return false
	}
	f := &flight{done: make(chan struct{})}
	v.running[key] = f
	v.mu.Unlock()

	defer func() {
		v.mu.Lock()
		delete(v.running, key)
		v.mu.Unlock()
		close(f.done)
	}()
	do()
	return true
}

// refreshStat and refreshList re-ask the server off the caller's thread, so an
// aged answer never makes anybody wait for it to be re-confirmed.
func (v *vfs) refreshStat(full string) {
	if v.busy("stat:" + full) {
		return
	}
	go func() { _, _ = v.statServer(full) }()
}

func (v *vfs) refreshList(dir string) {
	if v.busy("list:" + dir) {
		return
	}
	go func() { _, _ = v.listServer(dir) }()
}

// busy reports whether a request for this key is already in flight, so a stale
// path asked about a thousand times a second still starts one refresh.
func (v *vfs) busy(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.running[key]
	return ok
}

// readDir lists what a drive path holds, folding the case of the path itself
// when Windows asks for "/Docs" and the server has "/docs".
func (v *vfs) readDir(p string) ([]dirent, error) {
	full := v.mountPath(p)
	ents, err := v.list(full)
	if err == nil {
		return ents, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if canon, _, ok := v.foldCase(p, full); ok {
		return v.list(canon)
	}
	return nil, err
}

// open opens a file and returns its handle. Create makes the file first, the
// way Windows does for a new document.
func (v *vfs) open(p string, flags int, create bool, perm os.FileMode) (uint64, error) {
	write := flags&(flagWriteOnly|flagReadWrite) != 0 ||
		flags&(flagTruncate|flagAppend) != 0 || create
	if write && v.readOnly {
		return 0, errReadOnly
	}

	full := v.mountPath(p)
	var f remoteFile
	if create {
		// A new file has to exist before the create returns, so this one
		// dials now. Its mode comes from the server's own umask, the way a
		// file made on the server would; a second round trip to force one
		// on it would only make creating files slower.
		opened, err := v.remote.OpenFile(full, osFlags(flags, true))
		if err != nil {
			return 0, translate(err)
		}
		f = opened
	} else {
		a, canon, err := v.stat(p)
		if err != nil {
			return 0, err
		}
		if a.dir {
			return 0, errIsDir
		}
		full = canon
	}

	if create {
		v.forget(full)
		v.addEntry(path.Dir(full), path.Base(full), attr{mtime: v.now(), mode: perm})
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.next++
	fh := v.next
	v.files[fh] = &openFile{f: f, path: full, flags: osFlags(flags, create)}
	return fh, nil
}

// osFlags maps the flags FUSE hands us to the ones the sftp package wants.
func osFlags(flags int, create bool) int {
	out := os.O_RDONLY
	switch flags & 3 {
	case flagWriteOnly:
		out = os.O_WRONLY
	case flagReadWrite:
		out = os.O_RDWR
	}
	if create || flags&flagCreate != 0 {
		out |= os.O_CREATE
	}
	if flags&flagTruncate != 0 {
		out |= os.O_TRUNC
	}
	if flags&flagAppend != 0 {
		out |= os.O_APPEND
	}
	if flags&flagExclusive != 0 {
		out |= os.O_EXCL
	}
	return out
}

func (v *vfs) handle(fh uint64) (*openFile, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	f, ok := v.files[fh]
	if !ok {
		return nil, errNoHandle
	}
	return f, nil
}

func (v *vfs) read(fh uint64, off int64, buf []byte) (int, error) {
	h, err := v.handle(fh)
	if err != nil {
		return 0, err
	}
	f, err := h.ensure(v)
	if err != nil {
		return 0, err
	}
	n, err := f.ReadAt(buf, off)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return n, translate(err)
}

func (v *vfs) write(fh uint64, off int64, buf []byte) (int, error) {
	if v.readOnly {
		return 0, errReadOnly
	}
	h, err := v.handle(fh)
	if err != nil {
		return 0, err
	}
	f, err := h.ensure(v)
	if err != nil {
		return 0, err
	}
	n, err := f.WriteAt(buf, off)
	if n > 0 {
		v.wrote(h.path, off+int64(n))
	}
	return n, translate(err)
}

func (v *vfs) flush(fh uint64) error {
	_, err := v.handle(fh)
	return err
}

func (v *vfs) fsync(fh uint64) error {
	h, err := v.handle(fh)
	if err != nil {
		return err
	}
	h.mu.Lock()
	f := h.f
	h.mu.Unlock()
	if f == nil {
		return nil // nothing was written through this handle
	}
	if err := f.Sync(); err != nil {
		return translate(err)
	}
	return nil
}

func (v *vfs) release(fh uint64) error {
	v.mu.Lock()
	h, ok := v.files[fh]
	if ok {
		delete(v.files, fh)
	}
	v.mu.Unlock()
	if !ok {
		return errNoHandle
	}
	return h.close()
}

func (v *vfs) truncate(p string, size int64, fh uint64) error {
	if v.readOnly {
		return errReadOnly
	}
	if fh != 0 {
		if h, err := v.handle(fh); err == nil {
			f, err := h.ensure(v)
			if err != nil {
				return err
			}
			if err := f.Truncate(size); err != nil {
				return translate(err)
			}
			v.resized(h.path, size)
			return nil
		}
	}
	_, canon, err := v.stat(p)
	if err != nil {
		return err
	}
	f, err := v.remote.OpenFile(canon, os.O_WRONLY)
	if err != nil {
		return translate(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return translate(err)
	}
	v.resized(canon, size)
	return nil
}

func (v *vfs) mkdir(p string, perm os.FileMode) error {
	if v.readOnly {
		return errReadOnly
	}
	full := v.mountPath(p)
	if err := v.remote.Mkdir(full); err != nil {
		return translate(err)
	}
	v.remember(full, attr{dir: true, mtime: v.now(), mode: perm})
	v.addEntry(path.Dir(full), path.Base(full), attr{dir: true, mtime: v.now(), mode: perm})
	return nil
}

func (v *vfs) rmdir(p string) error {
	if v.readOnly {
		return errReadOnly
	}
	full := v.mountPath(p)
	if err := v.remote.RemoveDirectory(full); err != nil {
		return translate(err)
	}
	v.forget(full)
	v.dropEntry(path.Dir(full), path.Base(full))
	return nil
}

func (v *vfs) unlink(p string) error {
	if v.readOnly {
		return errReadOnly
	}
	full := v.mountPath(p)
	if err := v.remote.Remove(full); err != nil {
		return translate(err)
	}
	v.forget(full)
	v.dropEntry(path.Dir(full), path.Base(full))
	return nil
}

// rename moves a file, replacing what is already at the destination the way
// Windows expects MoveFileEx to.
func (v *vfs) rename(oldp, newp string) error {
	if v.readOnly {
		return errReadOnly
	}
	oldFull := v.mountPath(oldp)
	newFull := v.mountPath(newp)

	err := v.remote.PosixRename(oldFull, newFull)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		err = v.remote.Rename(oldFull, newFull)
	}
	if err != nil {
		// SFTP's rename refuses an existing destination; replace it.
		if a, statErr := v.statRemote(newFull); statErr == nil {
			if a.dir {
				_ = v.remote.RemoveDirectory(newFull)
			} else {
				_ = v.remote.Remove(newFull)
			}
			err = v.remote.Rename(oldFull, newFull)
		}
	}
	if err != nil {
		return translate(err)
	}

	// The attributes travel with the file, so the cached answer for the old
	// name is the right answer for the new one. Asking the server again would
	// be one more round trip for every rename Explorer does.
	a, absent, known, _ := v.cached(oldFull)
	v.forget(oldFull)
	v.forget(newFull)
	v.dropEntry(path.Dir(oldFull), path.Base(oldFull))
	if known && !absent {
		v.remember(newFull, a)
	} else {
		a, _, _ = v.stat(newp)
	}
	v.addEntry(path.Dir(newFull), path.Base(newFull), a)
	return nil
}

// chmod pretends to change permissions: SFTP servers often lack chmod, and
// Windows applications ask for it constantly. The failure is not one the user
// can act on, so it is reported as success.
func (v *vfs) chmod(p string, mode os.FileMode) error {
	if v.readOnly {
		return errReadOnly
	}
	if _, canon, err := v.stat(p); err == nil {
		_ = v.remote.Chmod(canon, mode.Perm())
	}
	return nil
}

func (v *vfs) utimens(p string, atime, mtime time.Time) error {
	if v.readOnly {
		return errReadOnly
	}
	_, canon, err := v.stat(p)
	if err != nil {
		return err
	}
	if mtime.IsZero() {
		mtime = atime
	}
	if atime.IsZero() {
		atime = mtime
	}
	if err := v.remote.Chtimes(canon, atime, mtime); err != nil {
		return translate(err)
	}
	if a, absent, ok, _ := v.cached(canon); ok && !absent {
		a.mtime = mtime
		v.remember(canon, a)
	}
	return nil
}

func (v *vfs) readlink(p string) (string, error) {
	_, canon, err := v.stat(p)
	if err != nil {
		return "", err
	}
	target, err := v.remote.ReadLink(canon)
	if err != nil {
		return "", translate(err)
	}
	return target, nil
}

func (v *vfs) symlink(target, p string) error {
	if v.readOnly {
		return errReadOnly
	}
	full := v.mountPath(p)
	if err := v.remote.Symlink(target, full); err != nil {
		return translate(err)
	}
	v.forget(path.Dir(full))
	return nil
}

// statfs fills in the numbers Explorer shows for the drive. A server that does
// not answer statvfs gets a plausible empty volume instead of an error. The
// answer is kept for a while: Windows asks for the free space whenever it
// lists a drive, and the figure moves slowly.
func (v *vfs) statfs(p string) *sftp.StatVFS {
	v.mu.Lock()
	if v.volume != nil && v.now().Before(v.volumeExp) {
		st := v.volume
		v.mu.Unlock()
		return st
	}
	v.mu.Unlock()

	st, err := v.remote.StatVFS(v.mountPath(p))
	if err != nil || st == nil || st.Bsize == 0 {
		st = &sftp.StatVFS{Bsize: 4096, Frsize: 4096, Namemax: 255}
	}
	v.mu.Lock()
	v.volume = st
	v.volumeExp = v.now().Add(30 * time.Second)
	v.mu.Unlock()
	return st
}

// close shuts every open file and the server connection behind them. It runs
// once, however many paths reach it. Background refreshes started before this
// point find the mount closed and stop asking.
func (v *vfs) close() {
	v.closeOnce.Do(func() {
		v.mu.Lock()
		v.closed = true
		open := make([]*openFile, 0, len(v.files))
		for fh, f := range v.files {
			open = append(open, f)
			delete(v.files, fh)
		}
		v.mu.Unlock()
		for _, h := range open {
			_ = h.close()
		}
		if v.remote != nil {
			_ = v.remote.Close()
		}
	})
}

// translate makes the errors a user can meet name what happened, rather than
// the syscall that failed.
func translate(err error) error {
	if err == nil {
		return nil
	}
	var se *sftp.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case uint32(sftp.ErrSSHFxNoSuchFile):
			return os.ErrNotExist
		case uint32(sftp.ErrSSHFxPermissionDenied):
			return os.ErrPermission
		}
	}
	return err
}
