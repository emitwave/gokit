package filesystem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryDisk is an in-memory Disk for tests. Files are stored as byte
// slices keyed by path; directories are tracked separately so
// MakeDirectory / Files / Directories produce the right semantics
// without scanning every key.
//
// Safe for concurrent use. NOT durable — contents vanish when the
// process exits.
type MemoryDisk struct {
	mu      sync.RWMutex
	files   map[string]*memFile
	dirs    map[string]struct{} // explicit MakeDirectory calls
}

type memFile struct {
	data    []byte
	modTime time.Time
}

// NewMemoryDisk returns an empty in-memory disk.
func NewMemoryDisk() *MemoryDisk {
	return &MemoryDisk{
		files: make(map[string]*memFile),
		dirs:  make(map[string]struct{}),
	}
}

// normalize converts the input path to a canonical form: leading /
// removed, trailing / removed, dots cleaned. Empty result means root.
func normalize(p string) string {
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// ---------- read --------------------------------------------------------

func (m *MemoryDisk) Get(_ context.Context, p string) ([]byte, error) {
	key := normalize(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, nil
}

func (m *MemoryDisk) Reader(_ context.Context, p string) (io.ReadCloser, error) {
	data, err := m.Get(context.Background(), p)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *MemoryDisk) Exists(_ context.Context, p string) (bool, error) {
	key := normalize(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.files[key]; ok {
		return true, nil
	}
	if _, ok := m.dirs[key]; ok {
		return true, nil
	}
	// A file inside an implicit directory means the directory "exists" too.
	prefix := key + "/"
	for k := range m.files {
		if strings.HasPrefix(k, prefix) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryDisk) Stat(_ context.Context, p string) (FileInfo, error) {
	key := normalize(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if f, ok := m.files[key]; ok {
		mime := ""
		if len(f.data) > 0 {
			n := len(f.data)
			if n > 512 {
				n = 512
			}
			mime = http.DetectContentType(f.data[:n])
		}
		return FileInfo{
			Path:     p,
			Size:     int64(len(f.data)),
			ModTime:  f.modTime,
			IsDir:    false,
			MimeType: mime,
		}, nil
	}
	if _, ok := m.dirs[key]; ok {
		return FileInfo{Path: p, IsDir: true}, nil
	}
	return FileInfo{}, ErrNotFound
}

// ---------- write -------------------------------------------------------

func (m *MemoryDisk) Put(_ context.Context, p string, data []byte) error {
	key := normalize(p)
	if key == "" {
		return ErrInvalidPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[key] = &memFile{data: cp, modTime: time.Now()}
	return nil
}

// memWriter buffers writes and commits on Close. Same semantics as
// LocalDisk's atomic writer — readers don't see partial writes.
type memWriter struct {
	disk   *MemoryDisk
	path   string
	buf    bytes.Buffer
	closed bool
}

func (w *memWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *memWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.disk.Put(context.Background(), w.path, w.buf.Bytes())
}

func (m *MemoryDisk) Writer(_ context.Context, p string) (io.WriteCloser, error) {
	return &memWriter{disk: m, path: p}, nil
}

func (m *MemoryDisk) Append(_ context.Context, p string, data []byte) error {
	key := normalize(p)
	if key == "" {
		return ErrInvalidPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[key]
	if !ok {
		f = &memFile{data: make([]byte, 0, len(data))}
		m.files[key] = f
	}
	f.data = append(f.data, data...)
	f.modTime = time.Now()
	return nil
}

func (m *MemoryDisk) Prepend(_ context.Context, p string, data []byte) error {
	key := normalize(p)
	if key == "" {
		return ErrInvalidPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[key]
	if !ok {
		f = &memFile{}
		m.files[key] = f
	}
	combined := make([]byte, 0, len(data)+len(f.data))
	combined = append(combined, data...)
	combined = append(combined, f.data...)
	f.data = combined
	f.modTime = time.Now()
	return nil
}

// ---------- mutation ----------------------------------------------------

func (m *MemoryDisk) Delete(_ context.Context, p string) error {
	key := normalize(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[key]; !ok {
		return ErrNotFound
	}
	delete(m.files, key)
	return nil
}

func (m *MemoryDisk) Copy(ctx context.Context, src, dst string) error {
	data, err := m.Get(ctx, src)
	if err != nil {
		return err
	}
	return m.Put(ctx, dst, data)
}

func (m *MemoryDisk) Move(ctx context.Context, src, dst string) error {
	data, err := m.Get(ctx, src)
	if err != nil {
		return err
	}
	if err := m.Put(ctx, dst, data); err != nil {
		return err
	}
	return m.Delete(ctx, src)
}

// ---------- directories -------------------------------------------------

func (m *MemoryDisk) MakeDirectory(_ context.Context, p string) error {
	key := normalize(p)
	if key == "" {
		return nil // root always exists
	}
	m.mu.Lock()
	m.dirs[key] = struct{}{}
	m.mu.Unlock()
	return nil
}

func (m *MemoryDisk) DeleteDirectory(_ context.Context, p string) error {
	key := normalize(p)
	prefix := key + "/"
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dirs, key)
	for k := range m.files {
		if k == key || strings.HasPrefix(k, prefix) {
			delete(m.files, k)
		}
	}
	for k := range m.dirs {
		if k == key || strings.HasPrefix(k, prefix) {
			delete(m.dirs, k)
		}
	}
	return nil
}

// Files returns immediate-child files.
func (m *MemoryDisk) Files(_ context.Context, dir string) ([]string, error) {
	key := normalize(dir)
	prefix := key
	if prefix != "" {
		prefix += "/"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []string{}
	for k := range m.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		// Immediate child only — no slashes in `rest`.
		if strings.Contains(rest, "/") {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemoryDisk) AllFiles(_ context.Context, dir string) ([]string, error) {
	key := normalize(dir)
	prefix := key
	if prefix != "" {
		prefix += "/"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []string{}
	for k := range m.files {
		if key == "" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemoryDisk) Directories(_ context.Context, dir string) ([]string, error) {
	key := normalize(dir)
	prefix := key
	if prefix != "" {
		prefix += "/"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]struct{}{}
	// Explicit dirs at this level
	for d := range m.dirs {
		if !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := strings.TrimPrefix(d, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		seen[joinSlash(dir, rest)] = struct{}{}
	}
	// Implicit dirs (file paths reveal their parent dirs)
	for f := range m.files {
		if !strings.HasPrefix(f, prefix) {
			continue
		}
		rest := strings.TrimPrefix(f, prefix)
		idx := strings.Index(rest, "/")
		if idx <= 0 {
			continue
		}
		seen[joinSlash(dir, rest[:idx])] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// Compile-time assertion.
var _ Disk = (*MemoryDisk)(nil)

// suppressUnused — keep the errors import in case future code uses
// errors.Is (helpful when extending without re-importing).
var _ = errors.Is
