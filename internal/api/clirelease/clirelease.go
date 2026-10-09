// Package clirelease keeps the newest published CLI release, which the
// api names in every answer's X-Repose-CLI-Latest header so an old CLI
// can say it is old (DECISIONS I-626). The release is read from the
// GitHub releases/latest redirect that install.sh follows, once an hour;
// a failed read keeps the last good one, and none means no header.
package clirelease

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/heracraft/repose/internal/obs"
)

// DefaultURL is the redirect install.sh follows for "latest".
const DefaultURL = "https://github.com/heracraft/repose/releases/latest"

// Every is how often the release is read again.
var Every = time.Hour

var tagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// Latest is the newest release; the zero value knows none.
type Latest struct {
	url    string
	client *http.Client
	log    *slog.Logger
	v      atomic.Value // string
}

// New reads url (DefaultURL when empty) with its own client, which
// follows no redirect: the Location of the first answer is the tag.
func New(url string, log *slog.Logger) *Latest {
	if url == "" {
		url = DefaultURL
	}
	if log == nil {
		log = obs.NewLogger(obs.LogOptions{Component: obs.ComponentAPI, Writer: io.Discard})
	}
	return &Latest{url: url, log: log, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Version is the newest release ("v0.1.32"), "" when none was read.
func (l *Latest) Version() string {
	if l == nil {
		return ""
	}
	s, _ := l.v.Load().(string)
	return s
}

// Run reads the release now and every Every until ctx ends.
func (l *Latest) Run(ctx context.Context) {
	for {
		if err := l.Fetch(ctx); err != nil && ctx.Err() == nil {
			l.log.Warn("could not read the latest CLI release", "event", "cli_release_read_failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(Every):
		}
	}
}

// Fetch reads the release once.
func (l *Latest) Fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url, nil)
	if err != nil {
		return err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
		return fmt.Errorf("answered %d with no redirect", resp.StatusCode)
	}
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return errors.New("the redirect names no tag")
	}
	tag := loc[i+len("/tag/"):]
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("tag %q is not vX.Y.Z", tag)
	}
	l.v.Store(tag)
	return nil
}
