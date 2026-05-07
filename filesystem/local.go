package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalDisk is a Disk backed by the host filesystem, rooted at a
// configured directory. All paths in the public API are interpreted
// relative to this root; absolute paths and `..` traversal are rejected.
type LocalDisk struct {
	root string
	// dirMode and fileMode control the permissions on created entries.
	// Defaults: 0755 for dirs, 0644 for files. Override via SetMode for
	// servers that need more restrictive (e.g. multi-tenant) defaults.
	dirMode  os.FileMode
	fileMode os.FileMode
}

// NewLocalDisk constructs a LocalDisk rooted at the given directory.
// The directory is created if it doesn't exist. Panics if the directory
// can't be created — that's a programming/deployment error worth
// catching loud rather than failing per-request later.
func NewLocalDisk(root string) *LocalDisk {
	abs, err := filepath.Abs(root)
	if err != nil {
		panic(fmt.Sprintf("filesystem: invalid root %q: %v", root, err))
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		panic(fmt.Sprintf("filesystem: failed to create root %q: %v", abs, err))
	}
	return &LocalDisk{
		root:     abs,
		dirMode:  0o755,
		fileMode: 0o644,
	}
}

// SetMode overrides the file/dir permission masks. Use during setup,
// before the disk handles requests.
func (d *LocalDisk) SetMode(fileMode, dirMode os.FileMode) {
	d.fileMode = fileMode
	d.dirMode = dirMode
}

// resolve translates a public path to an absolute filesystem path,
// rejecting traversal attempts. The returned path is guaranteed to be
// inside d.root.
//
// Rejection rules: absolute paths, any segment equal to "..", and paths
// that would resolve outside the root after Join. We reject `..`
// segments rather than silently normalizing them — explicit failure
// catches misuse early; silent normalization can hide bugs.
func (d *LocalDisk) resolve(path string) (string, error) {
	// Reject absolute paths upfront.
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return "", ErrInvalidPath
	}
	// Normalize forward slashes (the public API uses them regardless of OS).
	osPath := filepath.FromSlash(path)
	// Reject any `..` segment to prevent traversal.
	for _, seg := range strings.Split(osPath, string(filepath.Separator)) {
		if seg == ".." {
			return "", ErrInvalidPath
		}
	}
	cleaned := filepath.Clean(osPath)
	if cleaned == "." || cleaned == "" {
		return d.root, nil
	}
	abs := filepath.Join(d.root, cleaned)
	// Defense in depth: ensure resolved path is still under root.
	rel, err := filepath.Rel(d.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", ErrInvalidPath
	}
	return abs, nil
}

// ---------- read --------------------------------------------------------

func (d *LocalDisk) Get(_ context.Context, path string) ([]byte, error) {
	abs, err := d.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

func (d *LocalDisk) Reader(_ context.Context, path string) (io.ReadCloser, error) {
	abs, err := d.resolve(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (d *LocalDisk) Exists(_ context.Context, path string) (bool, error) {
	abs, err := d.resolve(path)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(abs)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (d *LocalDisk) Stat(_ context.Context, path string) (FileInfo, error) {
	abs, err := d.resolve(path)
	if err != nil {
		return FileInfo{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FileInfo{}, ErrNotFound
		}
		return FileInfo{}, err
	}
	info := FileInfo{
		Path:    path,
		Size:    st.Size(),
		ModTime: st.ModTime(),
		IsDir:   st.IsDir(),
	}
	// Cheap mime sniff for files — only read 512 bytes (max needed by
	// http.DetectContentType). Skip for directories and fail-soft if
	// we can't open the file (Stat already succeeded but a race could
	// race-delete between the calls).
	if !info.IsDir {
		if mime := sniffMime(abs); mime != "" {
			info.MimeType = mime
		}
	}
	return info, nil
}

func sniffMime(abs string) string {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return ""
	}
	return http.DetectContentType(buf[:n])
}

// ---------- write -------------------------------------------------------

// Put atomically writes data: writes to a sibling temp file, then
// renames into place. Crash-safe — readers either see the old contents
// or the new ones, never a partial write.
func (d *LocalDisk) Put(_ context.Context, path string, data []byte) error {
	abs, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), d.dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything fails before rename.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(d.fileMode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// Writer returns a buffered writer that atomically commits on Close.
type localWriter struct {
	tmp     *os.File
	tmpName string
	target  string
	mode    os.FileMode
	closed  bool
}

func (w *localWriter) Write(p []byte) (int, error) {
	return w.tmp.Write(p)
}

func (w *localWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if err := w.tmp.Chmod(w.mode); err != nil {
		_ = w.tmp.Close()
		_ = os.Remove(w.tmpName)
		return err
	}
	if err := w.tmp.Close(); err != nil {
		_ = os.Remove(w.tmpName)
		return err
	}
	return os.Rename(w.tmpName, w.target)
}

func (d *LocalDisk) Writer(_ context.Context, path string) (io.WriteCloser, error) {
	abs, err := d.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), d.dirMode); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &localWriter{
		tmp:     tmp,
		tmpName: tmp.Name(),
		target:  abs,
		mode:    d.fileMode,
	}, nil
}

func (d *LocalDisk) Append(ctx context.Context, path string, data []byte) error {
	abs, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), d.dirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, d.fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func (d *LocalDisk) Prepend(ctx context.Context, path string, data []byte) error {
	// No native prepend on the OS — read existing, prepend, atomic write.
	existing, err := d.Get(ctx, path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	combined := make([]byte, 0, len(data)+len(existing))
	combined = append(combined, data...)
	combined = append(combined, existing...)
	return d.Put(ctx, path, combined)
}

// ---------- mutation ----------------------------------------------------

func (d *LocalDisk) Delete(_ context.Context, path string) error {
	abs, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *LocalDisk) Copy(_ context.Context, src, dst string) error {
	srcAbs, err := d.resolve(src)
	if err != nil {
		return err
	}
	dstAbs, err := d.resolve(dst)
	if err != nil {
		return err
	}
	in, err := os.Open(srcAbs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dstAbs), d.dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstAbs), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(d.fileMode); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dstAbs)
}

func (d *LocalDisk) Move(_ context.Context, src, dst string) error {
	srcAbs, err := d.resolve(src)
	if err != nil {
		return err
	}
	dstAbs, err := d.resolve(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), d.dirMode); err != nil {
		return err
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ---------- directories -------------------------------------------------

func (d *LocalDisk) MakeDirectory(_ context.Context, path string) error {
	abs, err := d.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, d.dirMode)
}

func (d *LocalDisk) DeleteDirectory(_ context.Context, path string) error {
	abs, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	return nil
}

func (d *LocalDisk) Files(_ context.Context, dir string) ([]string, error) {
	abs, err := d.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Return paths relative to the disk root using forward slashes.
		out = append(out, joinSlash(dir, e.Name()))
	}
	return out, nil
}

func (d *LocalDisk) AllFiles(_ context.Context, dir string) ([]string, error) {
	abs, err := d.resolve(dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	err = filepath.WalkDir(abs, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *LocalDisk) Directories(_ context.Context, dir string) ([]string, error) {
	abs, err := d.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, joinSlash(dir, e.Name()))
	}
	return out, nil
}

// ---------- capability interfaces ---------------------------------------

// Path returns the absolute filesystem path for path. Implements
// PathProvider, so callers can detect this capability via type assertion.
func (d *LocalDisk) Path(path string) (string, error) {
	return d.resolve(path)
}

// Root returns the configured root directory (absolute).
func (d *LocalDisk) Root() string { return d.root }

// joinSlash joins two path components with forward slashes, regardless
// of OS. We keep the public API forward-slash-only for portability.
func joinSlash(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimPrefix(b, "/")
	if a == "" {
		return b
	}
	if a == "." {
		return b
	}
	return a + "/" + b
}

// Compile-time assertion that LocalDisk satisfies Disk and the
// capability interfaces it claims.
var _ Disk = (*LocalDisk)(nil)
var _ PathProvider = (*LocalDisk)(nil)

// silence the unused-import warning if we don't end up using time after
// future trimming; harmless.
var _ = time.Now
