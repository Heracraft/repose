// Package snapshot streams thin volumes to and from the snapshot store.
// Blob is the store (Azure Blob in production, a directory in tests and
// on a dev host); Streamer is the dd|zstd pipeline (or a byte copy against
// the fake LVM).
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

// Blob is a snapshot store.
type Blob interface {
	// Upload stores r at path with metadata and returns the bytes written.
	Upload(ctx context.Context, path string, r io.Reader, meta map[string]string) (uint64, error)
	// Stat reports what is stored at path; ok is false when nothing is.
	Stat(ctx context.Context, path string) (info Info, ok bool, err error)
	// Download streams path into w. A non-empty version fails the
	// download with ErrVersionChanged unless the stored bytes are still
	// that version, so two downloads of one version read the same bytes
	// (DECISIONS I-462).
	Download(ctx context.Context, path, version string, w io.Writer) error
}

// Info is what a store holds at a path.
type Info struct {
	Size int64
	// Version changes whenever the stored bytes do: the ETag on Azure.
	Version string
}

// ErrVersionChanged is a download of a version the store no longer holds.
var ErrVersionChanged = errors.New("blob changed since it was checked")

type countingReader struct {
	r io.Reader
	n uint64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += uint64(n)
	return n, err
}

// FileBlob stores blobs under a directory. It is the test backend and the
// break-glass target on a host without Blob credentials.
type FileBlob struct {
	Dir string
}

func (f *FileBlob) full(path string) (string, error) {
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("blob path %q not allowed", path)
	}
	return filepath.Join(f.Dir, filepath.FromSlash(path)), nil
}

// Upload implements Blob.
func (f *FileBlob) Upload(_ context.Context, path string, r io.Reader, meta map[string]string) (uint64, error) {
	p, err := f.full(path)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	tmp := p + ".part"
	w, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	cr := &countingReader{r: r}
	if _, err := io.Copy(w, cr); err != nil {
		_ = w.Close()      // the copy error is what matters
		_ = os.Remove(tmp) // partial upload is discarded
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return 0, err
	}
	var sb strings.Builder
	for k, v := range meta {
		fmt.Fprintf(&sb, "%s=%s\n", k, v)
	}
	if err := os.WriteFile(p+".meta", []byte(sb.String()), 0o644); err != nil {
		return 0, err
	}
	return cr.n, nil
}

// fileVersion is a FileBlob's version: an upload renames a new file into
// place, so its modification time and size change with the bytes.
func fileVersion(fi os.FileInfo) string {
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

// Download implements Blob. The open file keeps the bytes it was opened
// with when an upload renames another over it.
func (f *FileBlob) Download(_ context.Context, path, version string, w io.Writer) error {
	p, err := f.full(path)
	if err != nil {
		return err
	}
	r, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }() // read only
	if version != "" {
		fi, err := r.Stat()
		if err != nil {
			return err
		}
		if fileVersion(fi) != version {
			return ErrVersionChanged
		}
	}
	_, err = io.Copy(w, r)
	return err
}

// Stat implements Blob.
func (f *FileBlob) Stat(_ context.Context, path string) (Info, bool, error) {
	p, err := f.full(path)
	if err != nil {
		return Info{}, false, err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, false, nil
	}
	if err != nil {
		return Info{}, false, err
	}
	return Info{Size: fi.Size(), Version: fileVersion(fi)}, true, nil
}

// AzureBlob uploads to a container with the host's managed identity.
type AzureBlob struct {
	client    *azblob.Client
	container string
}

// NewAzureBlob authenticates with the managed identity (or the default
// credential chain when clientID is empty and no identity is present) and
// targets serviceURL/container.
func NewAzureBlob(serviceURL, container, clientID string) (*AzureBlob, error) {
	var cred azcore.TokenCredential
	var err error
	if clientID != "" {
		cred, err = azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{ID: azidentity.ClientID(clientID)})
	} else {
		cred, err = azidentity.NewDefaultAzureCredential(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("blob credential: %w", err)
	}
	c, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("blob client: %w", err)
	}
	return &AzureBlob{client: c, container: container}, nil
}

// Upload implements Blob with block-blob UploadStream, 8 MiB blocks, 4 in
// flight; azcopy is not used because it cannot read from a pipe.
func (a *AzureBlob) Upload(ctx context.Context, path string, r io.Reader, meta map[string]string) (uint64, error) {
	md := map[string]*string{}
	for k, v := range meta {
		v := v
		md[k] = &v
	}
	cr := &countingReader{r: r}
	_, err := a.client.UploadStream(ctx, a.container, path, cr, &azblob.UploadStreamOptions{BlockSize: 8 << 20, Concurrency: 4, Metadata: md})
	if err != nil {
		return cr.n, fmt.Errorf("blob upload: %w", err)
	}
	return cr.n, nil
}

// Download implements Blob with ranged GETs, downloadBlock bytes each,
// downloadParallel at once, written to w in order (parallelRanges). One
// GET of a 902 MB snapshot from host-01 ran at 104 MB/s (8.7 s); eight
// ranges at once took 1.4 s, and once a restore's writes went direct the
// single stream was what a restore waited on (DECISIONS I-403). Every
// range is pinned to the ETag the size came from (the version asked for,
// when one is), so a blob replaced mid-restore fails it instead of mixing
// two snapshots.
func (a *AzureBlob) Download(ctx context.Context, path, version string, w io.Writer) error {
	bc := a.client.ServiceClient().NewContainerClient(a.container).NewBlobClient(path)
	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		return fmt.Errorf("blob download: %w", err)
	}
	if props.ContentLength == nil || props.ETag == nil {
		return errors.New("blob download: no length or ETag")
	}
	if version != "" && string(*props.ETag) != version {
		return fmt.Errorf("blob download: %w", ErrVersionChanged)
	}
	cond := &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: props.ETag}}
	fetch := func(ctx context.Context, off int64, dst []byte) error {
		resp, err := bc.DownloadStream(ctx, &blob.DownloadStreamOptions{Range: blob.HTTPRange{Offset: off, Count: int64(len(dst))}, AccessConditions: cond})
		if err != nil {
			return err
		}
		body := resp.NewRetryReader(ctx, &blob.RetryReaderOptions{MaxRetries: 3})
		defer func() { _ = body.Close() }() // read to the end below
		if _, err := io.ReadFull(body, dst); err != nil {
			return fmt.Errorf("range at %d: %w", off, err)
		}
		return nil
	}
	if err := parallelRanges(ctx, *props.ContentLength, downloadBlock, downloadParallel, fetch, w); err != nil {
		return fmt.Errorf("blob download: %w", err)
	}
	return nil
}

const (
	// downloadBlock is one ranged GET; downloadParallel of them are in
	// flight, so a download holds downloadBlock*(downloadParallel+1) of
	// memory, 72 MiB.
	downloadBlock    = 8 << 20
	downloadParallel = 8
)

// parallelRanges reads size bytes as block-sized ranges, up to parallel
// at once, and writes them to w in order. The first error (a fetch, or
// w) cancels the fetches still running and is returned.
func parallelRanges(ctx context.Context, size, block int64, parallel int, fetch func(ctx context.Context, off int64, dst []byte) error, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type part struct {
		buf  []byte
		done chan error
	}
	// order holds the parts in offset order; slots holds one token per
	// fetch running; the pool's spare buffer is the one being written.
	order := make(chan part, parallel)
	slots := make(chan struct{}, parallel)
	pool := make(chan []byte, parallel+1)
	for range parallel + 1 {
		pool <- make([]byte, block)
	}
	go func() {
		defer close(order)
		for off := int64(0); off < size; off += block {
			var buf []byte
			select {
			case buf = <-pool:
			case <-ctx.Done():
				return
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				pool <- buf
				return
			}
			p := part{buf: buf[:min(block, size-off)], done: make(chan error, 1)}
			select {
			case order <- p:
			case <-ctx.Done():
				<-slots
				pool <- buf
				return
			}
			go func(off int64) {
				err := fetch(ctx, off, p.buf)
				<-slots
				p.done <- err
			}(off)
		}
	}()
	var first error
	var written int64
	for p := range order {
		err := <-p.done
		if first == nil && err != nil {
			first = err
		}
		if first == nil {
			if _, err := w.Write(p.buf); err != nil {
				first = err
			}
			written += int64(len(p.buf))
		}
		if first != nil {
			cancel()
		}
		pool <- p.buf[:cap(p.buf)]
	}
	if first == nil && written != size {
		first = ctx.Err()
		if first == nil {
			first = fmt.Errorf("read %d of %d bytes", written, size)
		}
	}
	return first
}

// Stat implements Blob.
func (a *AzureBlob) Stat(ctx context.Context, path string) (Info, bool, error) {
	props, err := a.client.ServiceClient().NewContainerClient(a.container).NewBlobClient(path).GetProperties(ctx, &blob.GetPropertiesOptions{})
	if err != nil {
		var re *azcore.ResponseError
		if errors.As(err, &re) && re.StatusCode == 404 {
			return Info{}, false, nil
		}
		return Info{}, false, err
	}
	if props.ContentLength == nil || props.ETag == nil {
		return Info{}, false, errors.New("blob stat: no length or ETag")
	}
	return Info{Size: *props.ContentLength, Version: string(*props.ETag)}, true, nil
}

// MemBlob is an in-memory store for tests.
type MemBlob struct {
	mu    sync.Mutex
	Blobs map[string][]byte
	Meta  map[string]map[string]string
	Fail  error
	// FailNext fails that many uploads with "injected upload failure",
	// then lets them through.
	FailNext int
	// BeforeUpload, when set, runs at the start of every Upload.
	BeforeUpload func(path string)
	// BeforeDownload, when set, runs at the start of every Download.
	BeforeDownload func(path string)
	// gen counts the uploads to each path: the version Stat reports.
	gen map[string]int
}

// NewMemBlob returns an empty store.
func NewMemBlob() *MemBlob {
	return &MemBlob{Blobs: map[string][]byte{}, Meta: map[string]map[string]string{}}
}

func (m *MemBlob) Upload(_ context.Context, path string, r io.Reader, meta map[string]string) (uint64, error) {
	if m.BeforeUpload != nil {
		m.BeforeUpload(path)
	}
	m.mu.Lock()
	if m.FailNext > 0 {
		m.FailNext--
		m.mu.Unlock()
		_, _ = io.Copy(io.Discard, r) // drain as a failed upload would
		return 0, errors.New("injected upload failure")
	}
	m.mu.Unlock()
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return 0, m.Fail
	}
	m.Blobs[path] = b
	m.Meta[path] = meta
	if m.gen == nil {
		m.gen = map[string]int{}
	}
	m.gen[path]++
	return uint64(len(b)), nil
}

func (m *MemBlob) Download(_ context.Context, path, version string, w io.Writer) error {
	if m.BeforeDownload != nil {
		m.BeforeDownload(path)
	}
	m.mu.Lock()
	b, ok := m.Blobs[path]
	cur := strconv.Itoa(m.gen[path])
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("blob %s not found", path)
	}
	if version != "" && version != cur {
		return ErrVersionChanged
	}
	_, err := w.Write(b)
	return err
}

// Stat implements Blob. A test that writes Blobs directly changes the
// bytes without changing the version, as a store that broke its promise
// would.
func (m *MemBlob) Stat(_ context.Context, path string) (Info, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Blobs[path]
	if !ok {
		return Info{}, false, nil
	}
	return Info{Size: int64(len(b)), Version: strconv.Itoa(m.gen[path])}, true, nil
}
