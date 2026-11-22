package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/heracraft/repose/internal/api/store"
)

// An erofs base row names files every host fetches anonymously (DECISIONS
// I-1464), so `base publish --format erofs` refuses one that no host
// could fetch, or whose digest the release itself contradicts, before the
// row is written: the 05/12 class of bug, a base row naming something
// that does not exist, found by the first host instead of the operator.

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// errNotFound is a file URL that answered 404, which for a part-published
// file (I-1504) sends the check to <url>.part0.
var errNotFound = errors.New("HTTP 404, not 200")

// erofsFile is one file of an erofs base: its column label, URL and the
// SHA-256 the operator gave.
type erofsFile struct {
	label, url, sha256 string
}

// baseHTTPClient fetches release files; tests replace it. It sends no
// credentials, which is what a host does.
var baseHTTPClient = &http.Client{Timeout: 30 * time.Second}

// checkErofsFiles verifies each file is reachable anonymously (HTTP 200
// to a HEAD, or 200/206 to a ranged GET when HEAD is refused) and that
// its SHA-256 matches the release's SHA256SUMS beside it and the
// manifest, whichever are reachable. version, when the manifest names
// one, must match.
func checkErofsFiles(ctx context.Context, files []erofsFile, manifestURL, version string) error {
	for _, f := range files {
		if f.url == "" || f.sha256 == "" {
			return fmt.Errorf("%w: --%s-url and --%s-sha256 are both required", ErrUsage, f.label, f.label)
		}
		u, err := url.Parse(f.url)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%w: --%s-url %q is not an http(s) URL", ErrUsage, f.label, f.url)
		}
		if !hexSHA256.MatchString(f.sha256) {
			return fmt.Errorf("%w: --%s-sha256 is not 64 lower-case hex digits", ErrUsage, f.label)
		}
	}
	// SHA256SUMS beside each file: read once per directory.
	sums := map[string]map[string]string{}
	sumsOf := func(u string) map[string]string {
		dir := dirOf(u)
		if _, ok := sums[dir]; !ok {
			m, err := fetchSums(ctx, dir+"SHA256SUMS")
			if err != nil {
				m = nil // not reachable: nothing to compare with
			}
			sums[dir] = m
		}
		return sums[dir]
	}
	for _, f := range files {
		name := path.Base(urlPath(f.url))
		err := reachable(ctx, f.url)
		if errors.Is(err, errNotFound) {
			// A file over GitHub's asset limit is published as parts,
			// <name>.part0, .part1, ... concatenated in order (DECISIONS
			// I-1504), and hostd fetches the parts when the whole-file
			// URL answers 404. The row keeps the whole-file URL and its
			// SHA-256, which hostd checks over the joined parts; here the
			// first part must answer and SHA256SUMS must list it.
			if perr := reachable(ctx, f.url+".part0"); perr == nil {
				if _, listed := sumsOf(f.url)[name+".part0"]; !listed {
					return fmt.Errorf("%s: %s answers 404 and %s.part0 is not listed in SHA256SUMS", f.label, f.url, name)
				}
				continue
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %s: %w", f.label, f.url, err)
		}
		// A listing that names the parts and not the whole file has
		// nothing to compare the whole file's digest with (I-1504); the
		// manifest, below, carries it.
		if want, ok := sumsOf(f.url)[name]; ok && want != f.sha256 {
			return fmt.Errorf("%s: SHA256SUMS says %s, --%s-sha256 says %s", f.label, want, f.label, f.sha256)
		}
	}
	if manifestURL == "" {
		return nil
	}
	if err := reachable(ctx, manifestURL); err != nil {
		return fmt.Errorf("manifest: %s: %w", manifestURL, err)
	}
	m, err := fetchManifest(ctx, manifestURL)
	if err != nil {
		return nil // reachable but not JSON we read: nothing to compare with
	}
	if version != "" && m.version != "" && m.version != version {
		return fmt.Errorf("manifest: it is for version %s, not %s", m.version, version)
	}
	for _, f := range files {
		if want, ok := m.sums[path.Base(urlPath(f.url))]; ok && want != f.sha256 {
			return fmt.Errorf("%s: the manifest says %s, --%s-sha256 says %s", f.label, want, f.label, f.sha256)
		}
	}
	return nil
}

// reachable asks for url anonymously: a HEAD, following redirects, must
// answer 200; a server that refuses HEAD gets a GET of the first byte,
// which must answer 200 or 206.
func reachable(ctx context.Context, u string) error {
	code, err := ask(ctx, http.MethodHead, u)
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	if code == http.StatusNotFound {
		return errNotFound
	}
	if code == http.StatusMethodNotAllowed || code == http.StatusForbidden || code == http.StatusNotImplemented {
		code, err = ask(ctx, http.MethodGet, u)
		if err != nil {
			return err
		}
		if code == http.StatusOK || code == http.StatusPartialContent {
			return nil
		}
		if code == http.StatusNotFound {
			return errNotFound
		}
	}
	return fmt.Errorf("HTTP %d, not 200", code)
}

func ask(ctx context.Context, method, u string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return 0, err
	}
	if method == http.MethodGet {
		req.Header.Set("Range", "bytes=0-0")
	}
	resp, err := baseHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func urlPath(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Path
}

// dirOf is u up to and including its last slash, without a query.
func dirOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.RawQuery, p.Fragment = "", ""
	s := p.String()
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[:i+1]
	}
	return s
}

// fetchSums reads a SHA256SUMS file: "<hex>  <name>" (or "<hex> *<name>")
// per line, into name -> hex.
func fetchSums(ctx context.Context, u string) (map[string]string, error) {
	body, err := fetchSmall(ctx, u)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || !hexSHA256.MatchString(strings.ToLower(f[0])) {
			continue
		}
		out[path.Base(strings.TrimPrefix(f[1], "*"))] = strings.ToLower(f[0])
	}
	return out, nil
}

// manifest is what checkErofsFiles reads from a release manifest: its
// version and every file digest it names, by base name.
type manifest struct {
	version string
	sums    map[string]string
}

// fetchManifest reads the release's manifest.json. The shape is the image
// pipeline's (images/guest), so it is read loosely: a top-level
// "version", and any object carrying "sha256" beside "name", "file" or
// "path", or keyed by a file name.
func fetchManifest(ctx context.Context, u string) (manifest, error) {
	body, err := fetchSmall(ctx, u)
	if err != nil {
		return manifest{}, err
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return manifest{}, err
	}
	m := manifest{sums: map[string]string{}}
	if top, ok := v.(map[string]any); ok {
		m.version, _ = top["version"].(string)
	}
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if sum, ok := x["sha256"].(string); ok && hexSHA256.MatchString(strings.ToLower(sum)) {
				name := key
				for _, k := range []string{"name", "file", "path"} {
					if n, ok := x[k].(string); ok && n != "" {
						name = n
					}
				}
				if name != "" {
					m.sums[path.Base(name)] = strings.ToLower(sum)
				}
			}
			for k, c := range x {
				walk(k, c)
			}
		case []any:
			for _, c := range x {
				walk("", c)
			}
		}
	}
	walk("", v)
	return m, nil
}

// fetchSmall GETs u, following redirects, and returns at most 1 MiB of
// its body; anything but 200 is an error.
func fetchSmall(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := baseHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// settableSettings are the keys `settings set` writes, each with the
// values it takes (nil: any). Other keys have their own commands (edge
// init, edge loki, hosts install-command), which validate what they write.
var settableSettings = map[string][]string{
	store.SettingDefaultGuestRuntime: {store.RuntimeNix, store.RuntimeErofs},
}

// settingsCmd is `repose-admin settings get KEY | set KEY VALUE | list`
// (DECISIONS I-1462): the flip of new projects to erofs is
// `settings set default_guest_runtime erofs` on an environment.
func (e *Env) settingsCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if err := e.connect(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "get":
		if len(args) != 2 {
			return ErrUsage
		}
		v, err := store.Setting(ctx, e.pool, args[1])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(e.Stdout, v)
		return nil
	case "set":
		if len(args) != 3 {
			return ErrUsage
		}
		key, val := args[1], args[2]
		allowed, ok := settableSettings[key]
		if !ok {
			return fmt.Errorf("%w: settings set takes %s", ErrUsage, strings.Join(settableKeys(), ", "))
		}
		if allowed != nil && !slices.Contains(allowed, val) {
			return fmt.Errorf("%w: %s is one of %s", ErrUsage, key, strings.Join(allowed, ", "))
		}
		old, err := store.Setting(ctx, e.pool, key)
		if err != nil {
			return err
		}
		if err := store.SetSetting(ctx, e.pool, key, val); err != nil {
			return err
		}
		if _, err := e.audited(ctx, "settings_set", key, map[string]any{"old": old, "new": val}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.Stdout, "%s = %s\n", key, val)
		return nil
	case "list":
		rows := [][]string{{"KEY", "VALUE"}}
		for _, k := range settableKeys() {
			v, err := store.Setting(ctx, e.pool, k)
			if err != nil {
				return err
			}
			rows = append(rows, []string{k, v})
		}
		e.table(rows)
		return nil
	}
	return fmt.Errorf("%w: settings %s", ErrUsage, args[0])
}

func settableKeys() []string {
	out := make([]string, 0, len(settableSettings))
	for k := range settableSettings {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
