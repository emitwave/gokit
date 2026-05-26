package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// GCSDisk is a Disk backed by Google Cloud Storage. All paths are
// object keys within a single bucket.
type GCSDisk struct {
	client *storage.Client
	bucket *storage.BucketHandle
	name   string // bucket name, for URL generation
}

// NewGCSDisk creates a GCS-backed Disk for the given bucket.
// Pass option.ClientOption values for credentials, project, etc.
func NewGCSDisk(ctx context.Context, bucket string, opts ...option.ClientOption) (*GCSDisk, error) {
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("filesystem/gcs: create client: %w", err)
	}
	return &GCSDisk{
		client: client,
		bucket: client.Bucket(bucket),
		name:   bucket,
	}, nil
}

// ContentTypeSetter is implemented by writers that allow setting a
// content-type before writing. GCS writers implement this so callers
// can set the MIME type for uploads.
type ContentTypeSetter interface {
	SetContentType(ct string)
}

// ---------- read --------------------------------------------------------

func (d *GCSDisk) Get(ctx context.Context, p string) ([]byte, error) {
	rc, err := d.Reader(ctx, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (d *GCSDisk) Reader(ctx context.Context, p string) (io.ReadCloser, error) {
	obj := d.bucket.Object(norm(p))
	r, err := obj.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

func (d *GCSDisk) Exists(ctx context.Context, p string) (bool, error) {
	obj := d.bucket.Object(norm(p))
	_, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *GCSDisk) Stat(ctx context.Context, p string) (FileInfo, error) {
	obj := d.bucket.Object(norm(p))
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return FileInfo{}, ErrNotFound
		}
		return FileInfo{}, err
	}
	return FileInfo{
		Path:     p,
		Size:     attrs.Size,
		ModTime:  attrs.Updated,
		IsDir:    false,
		MimeType: attrs.ContentType,
	}, nil
}

// ---------- write -------------------------------------------------------

func (d *GCSDisk) Put(ctx context.Context, p string, data []byte) error {
	w := d.bucket.Object(norm(p)).NewWriter(ctx)
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// gcsWriter wraps a GCS object writer and implements ContentTypeSetter.
type gcsWriter struct {
	w *storage.Writer
}

func (g *gcsWriter) Write(p []byte) (int, error) { return g.w.Write(p) }
func (g *gcsWriter) Close() error                 { return g.w.Close() }

// SetContentType sets the content-type on the GCS object before data is
// written. Must be called before the first Write.
func (g *gcsWriter) SetContentType(ct string) {
	g.w.ObjectAttrs.ContentType = ct
}

func (d *GCSDisk) Writer(ctx context.Context, p string) (io.WriteCloser, error) {
	w := d.bucket.Object(norm(p)).NewWriter(ctx)
	return &gcsWriter{w: w}, nil
}

func (d *GCSDisk) Append(ctx context.Context, p string, data []byte) error {
	existing, err := d.Get(ctx, p)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return d.Put(ctx, p, append(existing, data...))
}

func (d *GCSDisk) Prepend(ctx context.Context, p string, data []byte) error {
	existing, err := d.Get(ctx, p)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	combined := make([]byte, 0, len(data)+len(existing))
	combined = append(combined, data...)
	combined = append(combined, existing...)
	return d.Put(ctx, p, combined)
}

// ---------- mutation ----------------------------------------------------

func (d *GCSDisk) Delete(ctx context.Context, p string) error {
	err := d.bucket.Object(norm(p)).Delete(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *GCSDisk) Copy(ctx context.Context, src, dst string) error {
	s := d.bucket.Object(norm(src))
	dd := d.bucket.Object(norm(dst))
	_, err := dd.CopierFrom(s).Run(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (d *GCSDisk) Move(ctx context.Context, src, dst string) error {
	if err := d.Copy(ctx, src, dst); err != nil {
		return err
	}
	return d.Delete(ctx, src)
}

// ---------- directories -------------------------------------------------
// GCS is a flat object store — "directories" are simulated via key
// prefixes. MakeDirectory is a no-op; DeleteDirectory deletes all
// objects with the given prefix.

func (d *GCSDisk) MakeDirectory(_ context.Context, _ string) error {
	return nil // no-op for object stores
}

func (d *GCSDisk) DeleteDirectory(ctx context.Context, p string) error {
	prefix := norm(p)
	if prefix != "" {
		prefix += "/"
	}
	it := d.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return err
		}
		if err := d.bucket.Object(attrs.Name).Delete(ctx); err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
			return err
		}
	}
	return nil
}

func (d *GCSDisk) Files(ctx context.Context, dir string) ([]string, error) {
	prefix := norm(dir)
	if prefix != "" {
		prefix += "/"
	}
	it := d.bucket.Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	})
	var out []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			continue // skip directory markers
		}
		out = append(out, attrs.Name)
	}
	return out, nil
}

func (d *GCSDisk) AllFiles(ctx context.Context, dir string) ([]string, error) {
	prefix := norm(dir)
	if prefix != "" {
		prefix += "/"
	}
	it := d.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	var out []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, attrs.Name)
	}
	return out, nil
}

func (d *GCSDisk) Directories(ctx context.Context, dir string) ([]string, error) {
	prefix := norm(dir)
	if prefix != "" {
		prefix += "/"
	}
	it := d.bucket.Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: "/",
	})
	var out []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			name := strings.TrimSuffix(attrs.Prefix, "/")
			out = append(out, name)
		}
	}
	return out, nil
}

// ---------- capability interfaces ---------------------------------------

// URL returns the public GCS URL for the object.
func (d *GCSDisk) URL(p string) (string, error) {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", d.name, norm(p)), nil
}

// SignedURL returns a time-limited signed URL for direct client access.
func (d *GCSDisk) SignedURL(ctx context.Context, p string, expiresAt time.Time) (string, error) {
	url, err := d.bucket.SignedURL(norm(p), &storage.SignedURLOptions{
		Method:  "GET",
		Expires: expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("filesystem/gcs: signed url: %w", err)
	}
	return url, nil
}

// BucketName returns the configured bucket name.
func (d *GCSDisk) BucketName() string { return d.name }

// Close closes the underlying GCS client.
func (d *GCSDisk) Close() error { return d.client.Close() }

// norm normalizes a path for use as a GCS object key.
func norm(p string) string {
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// Compile-time assertions.
var _ Disk = (*GCSDisk)(nil)
var _ URLProvider = (*GCSDisk)(nil)
var _ SignedURLProvider = (*GCSDisk)(nil)

