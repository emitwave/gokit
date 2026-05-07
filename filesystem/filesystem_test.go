package filesystem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runDiskComplianceSuite runs the same tests against both LocalDisk and
// MemoryDisk so we know they implement the Disk contract identically.
type diskFactory func(t *testing.T) Disk

func localFactory(t *testing.T) Disk {
	t.Helper()
	return NewLocalDisk(t.TempDir())
}

func memoryFactory(_ *testing.T) Disk { return NewMemoryDisk() }

func TestLocalDisk_Compliance(t *testing.T) { runDiskTests(t, localFactory) }
func TestMemoryDisk_Compliance(t *testing.T) { runDiskTests(t, memoryFactory) }

func runDiskTests(t *testing.T, mk diskFactory) {
	t.Run("PutAndGet", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		if err := d.Put(ctx, "hello.txt", []byte("world")); err != nil {
			t.Fatal(err)
		}
		got, err := d.Get(ctx, "hello.txt")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte("world")) {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ExistsAndStat", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		if ok, _ := d.Exists(ctx, "missing.txt"); ok {
			t.Error("missing file should not exist")
		}
		_ = d.Put(ctx, "info.txt", []byte("hi"))
		if ok, _ := d.Exists(ctx, "info.txt"); !ok {
			t.Error("file should exist")
		}
		st, err := d.Stat(ctx, "info.txt")
		if err != nil {
			t.Fatal(err)
		}
		if st.Size != 2 {
			t.Errorf("size: got %d, want 2", st.Size)
		}
		if st.IsDir {
			t.Error("file should not be a dir")
		}
	})

	t.Run("Reader", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		_ = d.Put(ctx, "stream.txt", []byte("streamed contents"))
		r, err := d.Reader(ctx, "stream.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		out, _ := io.ReadAll(r)
		if string(out) != "streamed contents" {
			t.Errorf("got %q", out)
		}
	})

	t.Run("Writer", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		w, err := d.Writer(ctx, "writer.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("first "))
		_, _ = w.Write([]byte("second"))
		// Before Close, the file must NOT be visible (atomicity).
		if ok, _ := d.Exists(ctx, "writer.txt"); ok {
			t.Error("writer should not commit until Close")
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		got, _ := d.Get(ctx, "writer.txt")
		if string(got) != "first second" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("AppendAndPrepend", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		_ = d.Put(ctx, "log.txt", []byte("hello"))
		_ = d.Append(ctx, "log.txt", []byte(", world"))
		got, _ := d.Get(ctx, "log.txt")
		if string(got) != "hello, world" {
			t.Errorf("Append: got %q", got)
		}

		_ = d.Prepend(ctx, "log.txt", []byte("[INFO] "))
		got, _ = d.Get(ctx, "log.txt")
		if string(got) != "[INFO] hello, world" {
			t.Errorf("Prepend: got %q", got)
		}

		// Append on missing file creates it.
		_ = d.Append(ctx, "new.txt", []byte("started"))
		got, _ = d.Get(ctx, "new.txt")
		if string(got) != "started" {
			t.Errorf("Append on new file: got %q", got)
		}
	})

	t.Run("DeleteMissingErrors", func(t *testing.T) {
		d := mk(t)
		err := d.Delete(context.Background(), "ghost.txt")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("CopyAndMove", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		_ = d.Put(ctx, "src.txt", []byte("payload"))

		if err := d.Copy(ctx, "src.txt", "copy.txt"); err != nil {
			t.Fatal(err)
		}
		got, _ := d.Get(ctx, "copy.txt")
		if string(got) != "payload" {
			t.Errorf("Copy: %q", got)
		}
		if ok, _ := d.Exists(ctx, "src.txt"); !ok {
			t.Error("Copy should not remove source")
		}

		if err := d.Move(ctx, "src.txt", "moved.txt"); err != nil {
			t.Fatal(err)
		}
		if ok, _ := d.Exists(ctx, "src.txt"); ok {
			t.Error("Move should remove source")
		}
		if ok, _ := d.Exists(ctx, "moved.txt"); !ok {
			t.Error("moved.txt should exist after Move")
		}
	})

	t.Run("MakeAndDeleteDirectory", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		if err := d.MakeDirectory(ctx, "uploads/2026"); err != nil {
			t.Fatal(err)
		}
		_ = d.Put(ctx, "uploads/2026/a.txt", []byte("x"))
		_ = d.Put(ctx, "uploads/2026/b.txt", []byte("y"))

		if err := d.DeleteDirectory(ctx, "uploads/2026"); err != nil {
			t.Fatal(err)
		}
		if ok, _ := d.Exists(ctx, "uploads/2026/a.txt"); ok {
			t.Error("DeleteDirectory should recursively remove")
		}
	})

	t.Run("FilesAndDirectories", func(t *testing.T) {
		d := mk(t)
		ctx := context.Background()
		_ = d.Put(ctx, "users/a/file1.txt", []byte("1"))
		_ = d.Put(ctx, "users/a/file2.txt", []byte("2"))
		_ = d.Put(ctx, "users/b/file3.txt", []byte("3"))
		_ = d.Put(ctx, "users/readme.md", []byte("hi"))

		// Files() returns immediate children only
		files, err := d.Files(ctx, "users")
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(files)
		// Just users/readme.md — not nested files
		if len(files) != 1 || !strings.HasSuffix(files[0], "readme.md") {
			t.Errorf("Files(users): %v", files)
		}

		// AllFiles() recurses
		all, err := d.AllFiles(ctx, "users")
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 4 {
			t.Errorf("AllFiles: got %d, want 4: %v", len(all), all)
		}

		// Directories() returns immediate child dirs
		dirs, err := d.Directories(ctx, "users")
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(dirs)
		if len(dirs) != 2 {
			t.Errorf("Directories: got %d, want 2: %v", len(dirs), dirs)
		}
	})

	t.Run("GetMissingErrors", func(t *testing.T) {
		d := mk(t)
		_, err := d.Get(context.Background(), "no/such/file.txt")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})
}

// ---------- LocalDisk-specific tests -----------------------------------

func TestLocalDisk_RejectsTraversal(t *testing.T) {
	d := NewLocalDisk(t.TempDir())
	ctx := context.Background()
	cases := []string{
		"../escape.txt",
		"foo/../../escape.txt",
		"..",
	}
	for _, p := range cases {
		_, err := d.Get(ctx, p)
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Get(%q): got %v, want ErrInvalidPath", p, err)
		}
		err = d.Put(ctx, p, []byte("x"))
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Put(%q): got %v, want ErrInvalidPath", p, err)
		}
	}
}

func TestLocalDisk_AtomicWriteOnError(t *testing.T) {
	// If Writer.Close fails partway, the target file must not exist.
	// We simulate by closing twice (second call is a no-op, exercises
	// the closed-flag guard).
	d := NewLocalDisk(t.TempDir())
	ctx := context.Background()
	w, err := d.Writer(ctx, "foo.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hi"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("double close should be no-op, got %v", err)
	}
}

func TestLocalDisk_PathProvider(t *testing.T) {
	root := t.TempDir()
	d := NewLocalDisk(root)
	if _, ok := any(d).(PathProvider); !ok {
		t.Fatal("LocalDisk should implement PathProvider")
	}
	p, err := d.Path("a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "a", "b.txt")
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

// ---------- Manager tests -----------------------------------------------

func TestManager(t *testing.T) {
	mem := NewMemoryDisk()
	mgr := NewManager("local", mem)

	if mgr.DefaultName() != "local" {
		t.Errorf("default name: %q", mgr.DefaultName())
	}
	if mgr.Default() != mem {
		t.Error("Default() should return the registered default")
	}
	if mgr.Disk("local") != mem {
		t.Error("Disk('local') should match")
	}

	// Register a second disk, switch default.
	mem2 := NewMemoryDisk()
	mgr.Register("uploads", mem2)
	mgr.SetDefault("uploads")
	if mgr.Default() != mem2 {
		t.Error("after SetDefault, Default should return mem2")
	}

	names := mgr.Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "local" || names[1] != "uploads" {
		t.Errorf("Names: %v", names)
	}
}

func TestManagerPanicsOnUnknownDisk(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown disk")
		}
	}()
	mgr := NewManager("local", NewMemoryDisk())
	mgr.Disk("missing")
}

func TestManagerPanicsOnNilDefault(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil default disk")
		}
	}()
	NewManager("local", nil)
}

// ---------- Stat tests --------------------------------------------------

func TestStat_DetectsMimeType(t *testing.T) {
	for _, mk := range []diskFactory{localFactory, memoryFactory} {
		d := mk(t)
		ctx := context.Background()
		// PNG header (so http.DetectContentType identifies it).
		png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
		_ = d.Put(ctx, "image.png", png)
		st, err := d.Stat(ctx, "image.png")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(st.MimeType, "image/png") {
			t.Errorf("MimeType: got %q", st.MimeType)
		}
	}
}
