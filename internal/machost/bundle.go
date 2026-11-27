package machost

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// bundleTime is every entry's mtime, so the same files make the same
// bytes wherever the bundle is packed (the reproducible-build check).
var bundleTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// WriteBundle packs dir/bin/<BundleFiles> and a VERSION file into w as a
// gzip'd tar whose bytes depend only on the files: entries in
// BundleFiles' order, owner root:wheel (0:0), mode 0755, one mtime, and
// a gzip header with no name or time.
func WriteBundle(w io.Writer, dir, version string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("bundle: version %q", version)
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	hdr := func(name string, mode int64, size int64, typ byte) *tar.Header {
		return &tar.Header{Name: name, Mode: mode, Size: size, Typeflag: typ, ModTime: bundleTime,
			Uname: "root", Gname: "wheel", Format: tar.FormatUSTAR}
	}
	if err := tw.WriteHeader(hdr("bin/", 0o755, 0, tar.TypeDir)); err != nil {
		return err
	}
	for _, name := range BundleFiles {
		p := filepath.Join(dir, "bin", name)
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("bundle: %w", err)
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			_ = f.Close()
			return fmt.Errorf("bundle: %s is not a regular file", p)
		}
		if err := tw.WriteHeader(hdr("bin/"+name, 0o755, st.Size(), tar.TypeReg)); err != nil {
			_ = f.Close()
			return err
		}
		_, err = io.Copy(tw, f)
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	v := []byte(version + "\n")
	if err := tw.WriteHeader(hdr("VERSION", 0o644, int64(len(v)), tar.TypeReg)); err != nil {
		return err
	}
	if _, err := tw.Write(v); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// maxBundleFile bounds one file of a bundle (hostd is about 30 MB).
const maxBundleFile = 256 << 20

// ExtractBundle unpacks a bundle into dest, which must not exist, and
// returns its VERSION. It takes only bin/<one of BundleFiles> and VERSION,
// regular files, and refuses anything else (a path that leaves dest, a
// link, a device), so a bundle can never write outside dest or make a
// file hostd would run that is not one it knows. Every BundleFiles entry
// must be there. On any error dest is removed.
func ExtractBundle(r io.Reader, dest string) (version string, err error) {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err := os.Mkdir(filepath.Join(dest, "bin"), 0o755); err != nil {
		return "", err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("bundle: %w", err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("bundle: %w", err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir && strings.TrimSuffix(name, "/") == "bin" {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("bundle: %s is not a regular file", h.Name)
		}
		if h.Size > maxBundleFile {
			return "", fmt.Errorf("bundle: %s is %d bytes", h.Name, h.Size)
		}
		switch {
		case name == "VERSION":
			b, err := io.ReadAll(io.LimitReader(tr, 128))
			if err != nil {
				return "", err
			}
			version = strings.TrimSpace(string(b))
			if !ValidVersion(version) {
				return "", fmt.Errorf("bundle: VERSION %q", version)
			}
			if err := os.WriteFile(filepath.Join(dest, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
				return "", err
			}
		case strings.HasPrefix(name, "bin/") && slices.Contains(BundleFiles, strings.TrimPrefix(name, "bin/")):
			base := strings.TrimPrefix(name, "bin/")
			if seen[base] {
				return "", fmt.Errorf("bundle: %s twice", h.Name)
			}
			seen[base] = true
			f, err := os.OpenFile(filepath.Join(dest, "bin", base), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(f, io.LimitReader(tr, maxBundleFile))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("bundle: unexpected entry %s", h.Name)
		}
	}
	for _, n := range BundleFiles {
		if !seen[n] {
			return "", fmt.Errorf("bundle: bin/%s is missing", n)
		}
	}
	if version == "" {
		return "", errors.New("bundle: VERSION is missing")
	}
	return version, nil
}

// FileSHA256 is a file's lower-case hex SHA-256.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
