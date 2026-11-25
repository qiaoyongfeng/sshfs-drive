//go:build windows

package main

import (
	"os"
	"testing"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// testWinFS is the drive layer over a memory server: no WinFsp, no network,
// but every call Windows would make.
func testWinFS(t *testing.T, readOnly bool) (*winfs, *memRemote) {
	t.Helper()
	r := newMemRemote()
	r.putDir("/home/ada")
	v := newVFS(r, "/home/ada", readOnly, 20*time.Second)
	v.now = func() time.Time { return time.Unix(1700000000, 0) }
	return newWinFS(v, func(string) {}), r
}

func TestWinFSGetattr(t *testing.T) {
	w, r := testWinFS(t, false)
	r.putFile("/home/ada/notes.txt", "hello")
	r.putDir("/home/ada/docs")

	var st fuse.Stat_t
	if code := w.Getattr("/notes.txt", &st, 0); code != 0 {
		t.Fatalf("Getattr: %d", code)
	}
	if st.Mode&fuse.S_IFREG == 0 || st.Size != 5 {
		t.Errorf("file stat %+v", st)
	}
	if st.Mtim.Sec == 0 {
		t.Errorf("no modification time: %+v", st)
	}

	if code := w.Getattr("/docs", &st, 0); code != 0 {
		t.Fatalf("Getattr of a directory: %d", code)
	}
	if st.Mode&fuse.S_IFDIR == 0 {
		t.Errorf("directory stat %+v", st)
	}

	if code := w.Getattr("/missing", &st, 0); code != -fuse.ENOENT {
		t.Errorf("a missing file is %d, want ENOENT", code)
	}
}

func TestWinFSReaddir(t *testing.T) {
	w, r := testWinFS(t, false)
	r.putFile("/home/ada/a.txt", "a")
	r.putFile("/home/ada/b.txt", "bb")
	r.putDir("/home/ada/sub")

	if code, fh := w.Opendir("/"); code != 0 {
		t.Fatalf("Opendir: %d", code)
	} else if code := w.Readdir("/", func(name string, st *fuse.Stat_t, ofst int64) bool {
		return true
	}, 0, fh); code != 0 {
		t.Fatalf("Readdir: %d", code)
	}

	var names []string
	sizes := map[string]int64{}
	w.Readdir("/", func(name string, st *fuse.Stat_t, ofst int64) bool {
		names = append(names, name)
		if st != nil {
			sizes[name] = st.Size
		}
		return true
	}, 0, 0)

	want := []string{".", "..", "a.txt", "b.txt", "sub"}
	if len(names) != len(want) {
		t.Fatalf("entries %q, want %q", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries %q, want %q", names, want)
		}
	}
	if sizes["b.txt"] != 2 {
		t.Errorf("b.txt is %d bytes in the listing", sizes["b.txt"])
	}
}

// Opening a folder is how Windows reads a folder's attributes: every
// CreateFile on a directory arrives here. Only a listing should list, or
// showing a tree of folders costs a full readdir for every one of them.
func TestWinFSOpeningAFolderDoesNotListIt(t *testing.T) {
	w, r := testWinFS(t, false)
	r.putDir("/home/ada/docs")

	if code, _ := w.Opendir("/docs"); code != 0 {
		t.Fatalf("Opendir: %d", code)
	}
	if r.dirCalls != 0 {
		t.Errorf("opening a folder listed it %d times", r.dirCalls)
	}
	if code := w.Readdir("/docs", func(string, *fuse.Stat_t, int64) bool { return true }, 0, 0); code != 0 {
		t.Fatalf("Readdir: %d", code)
	}
	if r.dirCalls != 1 {
		t.Errorf("reading listed the folder %d times, want 1", r.dirCalls)
	}
}

func TestWinFSFileLifecycle(t *testing.T) {
	w, r := testWinFS(t, false)

	code, fh := w.Create("/new.txt", fuse.O_CREAT|fuse.O_RDWR, 0o644)
	if code != 0 {
		t.Fatalf("Create: %d", code)
	}
	if n := w.Write("/new.txt", []byte("hello"), 0, fh); n != 5 {
		t.Fatalf("Write: %d", n)
	}
	if n := w.Read("/new.txt", make([]byte, 5), 0, fh); n != 5 {
		t.Fatalf("Read on the writing handle: %d", n)
	}
	if code := w.Fsync("/new.txt", false, fh); code != 0 {
		t.Errorf("Fsync: %d", code)
	}
	if code := w.Flush("/new.txt", fh); code != 0 {
		t.Errorf("Flush: %d", code)
	}
	if code := w.Release("/new.txt", fh); code != 0 {
		t.Fatalf("Release: %d", code)
	}
	if got := string(r.files["/home/ada/new.txt"]); got != "hello" {
		t.Fatalf("the server holds %q", got)
	}

	code, fh = w.Open("/new.txt", fuse.O_RDONLY)
	if code != 0 {
		t.Fatalf("Open: %d", code)
	}
	buf := make([]byte, 3)
	if n := w.Read("/new.txt", buf, 1, fh); n != 3 || string(buf) != "ell" {
		t.Errorf("read at an offset gave %d %q", n, buf)
	}
	w.Release("/new.txt", fh)

	var st fuse.Stat_t
	w.Getattr("/new.txt", &st, 0)
	if code := w.Truncate("/new.txt", 2, 0); code != 0 {
		t.Fatalf("Truncate: %d", code)
	}
	w.Getattr("/new.txt", &st, 0)
	if st.Size != 2 {
		t.Errorf("size after truncate is %d", st.Size)
	}
}

func TestWinFSDirectoryChanges(t *testing.T) {
	w, r := testWinFS(t, false)

	if code := w.Mkdir("/d", 0o755); code != 0 {
		t.Fatalf("Mkdir: %d", code)
	}
	if !r.dirs["/home/ada/d"] {
		t.Fatal("the directory was not made")
	}
	if code := w.Rmdir("/d"); code != 0 {
		t.Fatalf("Rmdir: %d", code)
	}

	r.putFile("/home/ada/a.txt", "a")
	if code := w.Rename("/a.txt", "/b.txt"); code != 0 {
		t.Fatalf("Rename: %d", code)
	}
	if _, ok := r.files["/home/ada/b.txt"]; !ok {
		t.Fatal("the rename did not reach the server")
	}
	if code := w.Unlink("/b.txt"); code != 0 {
		t.Fatalf("Unlink: %d", code)
	}
	if _, ok := r.files["/home/ada/b.txt"]; ok {
		t.Fatal("the file is still there")
	}
}

func TestWinFSReadOnlyMount(t *testing.T) {
	w, r := testWinFS(t, true)
	r.putFile("/home/ada/a.txt", "a")

	if code := w.Access("/a.txt", fuse.W_OK); code != -fuse.EROFS {
		t.Errorf("write access on a read-only mount is %d, want EROFS", code)
	}
	if code := w.Access("/a.txt", fuse.R_OK); code != 0 {
		t.Errorf("read access on a read-only mount is %d", code)
	}
	if code, _ := w.Create("/b.txt", fuse.O_CREAT, 0o644); code != -fuse.EROFS {
		t.Errorf("Create on a read-only mount is %d, want EROFS", code)
	}
	if code := w.Unlink("/a.txt"); code != -fuse.EROFS {
		t.Errorf("Unlink on a read-only mount is %d, want EROFS", code)
	}
}

func TestWinFSIgnoresChmodAndChown(t *testing.T) {
	w, _ := testWinFS(t, false)
	if code := w.Chmod("/", 0o777); code != 0 {
		t.Errorf("Chmod: %d", code)
	}
	if code := w.Chown("/", 0, 0); code != 0 {
		t.Errorf("Chown: %d", code)
	}
}

func TestWinFSStatfsAlwaysHasNumbers(t *testing.T) {
	w, r := testWinFS(t, false)
	var st fuse.Statfs_t
	if code := w.Statfs("/", &st); code != 0 {
		t.Fatalf("Statfs: %d", code)
	}
	if st.Bsize == 0 || st.Namemax == 0 {
		t.Errorf("statfs %+v", st)
	}

	r.statvfs = nil
	w.Statfs("/", &st)
	if st.Bsize != 4096 {
		t.Errorf("no fallback block size: %+v", st)
	}
}

func TestWinFSUtimens(t *testing.T) {
	w, r := testWinFS(t, false)
	r.putFile("/home/ada/a.txt", "a")
	when := time.Unix(1600000000, 0)

	code := w.Utimens("/a.txt", []fuse.Timespec{
		fuse.NewTimespec(when),
		fuse.NewTimespec(when),
	})
	if code != 0 {
		t.Fatalf("Utimens: %d", code)
	}
	if got := r.mtime["/home/ada/a.txt"].Unix(); got != when.Unix() {
		t.Errorf("mtime is %d, want %d", got, when.Unix())
	}
}

func TestErrnoNamesWhatWentWrong(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{os.ErrNotExist, -fuse.ENOENT},
		{os.ErrPermission, -fuse.EACCES},
		{os.ErrExist, -fuse.EEXIST},
		{errReadOnly, -fuse.EROFS},
		{errIsDir, -fuse.EISDIR},
		{errNoHandle, -fuse.EBADF},
		{nil, 0},
	}
	for _, c := range cases {
		if got := errno(c.err); got != c.want {
			t.Errorf("errno(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
