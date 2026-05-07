// Package filesystem provides a backend-agnostic file storage abstraction
// modeled loosely on Laravel's Storage facade. The shape:
//
//   - A Disk interface describing the common operations
//   - LocalDisk — backed by the host filesystem, with path-traversal protection
//   - MemoryDisk — in-memory, for tests
//   - Manager — registers multiple named disks with a default
//
// To plug in cloud storage (S3, R2, GCS, MinIO), implement the Disk
// interface against your client SDK. The interface is intentionally
// kept small enough that doing so is a few hundred lines, not a porting
// project.
//
// Wire-up:
//
//	local := filesystem.NewLocalDisk("/var/app/storage")
//	mgr := filesystem.NewManager("local", local)
//	mgr.Register("uploads", filesystem.NewLocalDisk("/var/app/uploads"))
//
//	// Use a specific disk
//	if err := mgr.Disk("uploads").Put(ctx, "avatars/u1.jpg", data); err != nil { ... }
//
//	// Use the default
//	contents, err := mgr.Default().Get(ctx, "config/app.json")
//
// Backends with extra capabilities (filesystem path, public URL, signed
// URL) implement the optional capability interfaces — PathProvider,
// URLProvider, SignedURLProvider — so callers can opportunistically
// detect them via type assertion without forcing every disk to support
// every operation.
package filesystem

import (
	"context"
	"errors"
	"io"
	"time"
)

// Disk is the core file storage contract. All operations take a context
// for cancellation; backends that don't naturally honor ctx (like local
// disk operations on small files) should still accept and ignore it for
// API consistency.
//
// Paths are forward-slash separated regardless of OS. Backend
// implementations translate to native paths internally — never expose
// OS-specific separators in returned paths.
type Disk interface {
	// ---------- read ----------

	// Get returns the entire file contents. For very large files prefer
	// Reader to avoid pulling everything into memory.
	Get(ctx context.Context, path string) ([]byte, error)

	// Reader returns a streaming reader. Caller must Close.
	Reader(ctx context.Context, path string) (io.ReadCloser, error)

	// Exists reports whether path exists. A non-existent path is NOT an
	// error — it returns (false, nil). Real backend errors (network,
	// permissions) come back as (false, err).
	Exists(ctx context.Context, path string) (bool, error)

	// Stat returns metadata. ErrNotFound for missing paths.
	Stat(ctx context.Context, path string) (FileInfo, error)

	// ---------- write ----------

	// Put writes data, replacing any existing file. Creates parent
	// directories as needed. Atomic on backends that support atomic
	// writes (LocalDisk uses write-to-temp-and-rename).
	Put(ctx context.Context, path string, data []byte) error

	// Writer returns a streaming writer. Caller must Close. The file
	// is not visible until Close returns successfully (atomic).
	Writer(ctx context.Context, path string) (io.WriteCloser, error)

	// Append adds data to the end of the file, creating it if missing.
	// Not all backends can be efficient about this — cloud blob stores
	// often implement append as Get+modify+Put.
	Append(ctx context.Context, path string, data []byte) error

	// Prepend adds data to the start of the file, creating it if
	// missing. Implemented as Get+modify+Put on most backends.
	Prepend(ctx context.Context, path string, data []byte) error

	// ---------- mutation ----------

	// Delete removes a file. ErrNotFound if it doesn't exist.
	Delete(ctx context.Context, path string) error

	// Copy duplicates a file. Backends optimize by avoiding round-trip
	// through memory where possible (LocalDisk uses io.Copy on opened
	// files; cloud backends use server-side copy operations).
	Copy(ctx context.Context, src, dst string) error

	// Move renames/moves a file. Atomic where the backend supports it.
	Move(ctx context.Context, src, dst string) error

	// ---------- directories ----------

	// MakeDirectory creates a directory and any missing parents.
	// Idempotent — already-exists is not an error.
	MakeDirectory(ctx context.Context, path string) error

	// DeleteDirectory removes a directory and all its contents. Use
	// with care — this is rm -rf semantics.
	DeleteDirectory(ctx context.Context, path string) error

	// Files lists files (not directories) directly inside dir, with
	// paths returned relative to the disk root. Use AllFiles for
	// recursive listing.
	Files(ctx context.Context, dir string) ([]string, error)

	// AllFiles lists all files recursively under dir.
	AllFiles(ctx context.Context, dir string) ([]string, error)

	// Directories lists immediate child directories of dir.
	Directories(ctx context.Context, dir string) ([]string, error)
}

// ---------- FileInfo ----------------------------------------------------

// FileInfo is metadata about a file. Backend implementations populate
// what they have — fields they can't determine cheaply may be zero.
type FileInfo struct {
	Path     string    // path relative to the disk root
	Size     int64     // size in bytes
	ModTime  time.Time // last modification time (zero for backends that don't track this)
	IsDir    bool      // true for directories
	MimeType string    // detected mime type, if the backend chose to compute it
}

// ---------- Errors ------------------------------------------------------

// ErrNotFound is returned for read operations on missing paths.
var ErrNotFound = errors.New("filesystem: not found")

// ErrInvalidPath is returned when a path attempts traversal or contains
// invalid characters. LocalDisk uses this to defend against `..` segments
// that would escape the disk root.
var ErrInvalidPath = errors.New("filesystem: invalid path")

// ErrUnsupported is returned by backends for operations they can't or
// don't implement. Use it sparingly — most callers won't check for it.
var ErrUnsupported = errors.New("filesystem: operation not supported on this backend")

// ---------- Capability interfaces ---------------------------------------

// PathProvider is implemented by backends that have a meaningful
// filesystem path for stored files (LocalDisk does; cloud backends
// usually don't). Detect via type assertion:
//
//	if pp, ok := disk.(filesystem.PathProvider); ok {
//	    p, _ := pp.Path("uploads/file.jpg")
//	    // ... shell out to a local tool, etc.
//	}
type PathProvider interface {
	Path(path string) (string, error)
}

// URLProvider is implemented by backends that can produce a public URL
// for stored files (cloud backends with public buckets, CDN-fronted
// disks). LocalDisk does NOT implement this by default — public URLs
// require an HTTP serving layer outside the disk's scope.
type URLProvider interface {
	URL(path string) (string, error)
}

// SignedURLProvider is implemented by backends that can produce a
// time-limited signed URL for direct client access (S3 presigned URLs,
// GCS signed URLs). The expiry parameter is when the URL stops working.
type SignedURLProvider interface {
	SignedURL(ctx context.Context, path string, expiresAt time.Time) (string, error)
}
