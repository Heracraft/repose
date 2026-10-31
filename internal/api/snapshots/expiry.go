// Package snapshots owns retention (05-control-plane-api.md §5.8,
// docs/features/snapshots.md): live projects keep seven days of
// snapshots and always their newest one; destroyed projects keep the
// last snapshot until expires_at; a snapshot a restore is reading is
// never touched. Blobs are deleted through the BlobStore, then the row
// gets deleted_at.
package snapshots

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/db"
)

// BlobStore deletes snapshot blobs. Deleting a missing blob is not an
// error.
type BlobStore interface {
	Delete(ctx context.Context, path string) error
}

// Retention is the live-project window.
const Retention = 7 * 24 * time.Hour

// MaxPerProject is how many snapshots a live project keeps, which is
// what R4-11, features/snapshots.md and PRICING.md have always said
// ("Seven daily snapshots per project ... Manual snapshots count toward
// the seven"). Only the window was enforced, with no count and no
// LIMIT anywhere, so the number a project held was however many times
// it had been stopped in seven days: `stop` is the one snapshot a user
// can fire at will, and ten stops a day kept seventy full images
// instead of seven (I-900). I-809 found the same missing cap on the
// destroy path, where a destroy kept up to seven snapshots against a
// promise of one; this is that bug on the hot path.
//
// Fork snapshots are not counted: they expire on their own expires_at
// (I-703) and are not a project's dailies.
const MaxPerProject = 7

// Expiry is the job.
type Expiry struct {
	pool *db.Pool
	blob BlobStore
	m    *metrics.M
	log  *slog.Logger
	Now  func() time.Time
}

// New builds the job.
func New(pool *db.Pool, blob BlobStore, m *metrics.M, log *slog.Logger) *Expiry {
	return &Expiry{pool: pool, blob: blob, m: m, log: log.With("component", "api"), Now: time.Now}
}

// Run executes the job daily until ctx ends.
func (e *Expiry) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		release, ok, err := db.TryLock(ctx, e.pool, db.LockSnapshots)
		if err == nil && ok {
			if _, err := e.Once(ctx); err != nil && ctx.Err() == nil {
				e.log.Error("snapshot expiry", "event", "snapshot_expiry_fail", "err", err.Error())
			}
			release()
		}
		e.UpdateAgeGauge(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once deletes every expired snapshot and returns the ids deleted. A blob
// that cannot be deleted is logged and skipped so the rest of the sweep
// goes on (I-130); the error it returns then names the count, so the job
// still reports the run as failed.
func (e *Expiry) Once(ctx context.Context) ([]uuid.UUID, error) {
	deleted, failed, err := e.Sweep(ctx)
	if err != nil {
		return deleted, err
	}
	if failed > 0 {
		return deleted, fmt.Errorf("%d snapshot(s) kept: blob delete failed (snapshot_expiry_fail)", failed)
	}
	return deleted, nil
}

// expiredWhere selects the rows the retention rule retires: a destroyed
// project's past expires_at, a live project's older than Retention or
// beyond MaxPerProject except its newest, and a fork's own snapshot past
// its expires_at, which is never a project's newest (DECISIONS I-703);
// never one a restore holds (restoring_op_id).
//
// The count is correlated rather than a window function so the fragment
// stays a bare WHERE both queries below can embed: a row is beyond the
// cap when MaxPerProject newer non-fork snapshots of its project are
// still live. The tuple compares in the same order the newest-snapshot
// subquery sorts by.
const expiredWhere = `s.deleted_at is null and s.restoring_op_id is null
	and (
	  (p.destroyed_at is not null and s.expires_at is not null and s.expires_at < $1)
	  or (s.reason = 'fork' and s.expires_at is not null and s.expires_at < $1)
	  or (p.destroyed_at is null
	      and (
	        s.taken_at < $2
	        or (s.reason <> 'fork' and (select count(*) from snapshots n
	              where n.project_id = s.project_id and n.deleted_at is null and n.reason <> 'fork'
	                and (n.taken_at, n.created_at) > (s.taken_at, s.created_at)) >= $3)
	      )
	      and s.id <> (select id from snapshots n where n.project_id = s.project_id and n.deleted_at is null and n.reason <> 'fork' order by n.taken_at desc, n.created_at desc limit 1))
	)`

// Sweep is one run: the candidates are read once, then each is locked,
// re-checked, deleted from Blob and marked in its own transaction. failed
// counts rows whose blob delete failed; they stay for the next run.
func (e *Expiry) Sweep(ctx context.Context) (deleted []uuid.UUID, failed int, err error) {
	now := e.Now()
	rows, err := e.pool.Query(ctx, `select s.id from snapshots s join projects p on p.id = s.project_id where `+expiredWhere+` order by s.taken_at`, now, now.Add(-Retention), MaxPerProject)
	if err != nil {
		return nil, 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var path string
		err := db.InTx(ctx, e.pool, func(tx db.Tx) error {
			// Still expired, not taken by a restore since the candidate list,
			// and not locked by another replica's sweep.
			err := tx.QueryRow(ctx, `select s.blob_path from snapshots s join projects p on p.id = s.project_id where s.id = $4 and `+expiredWhere+` for update of s skip locked`, now, now.Add(-Retention), MaxPerProject, id).Scan(&path)
			if err != nil {
				if db.IsNoRows(err) {
					return errSkip
				}
				return err
			}
			if err := e.blob.Delete(ctx, path); err != nil {
				return fmt.Errorf("delete blob: %w", err)
			}
			_, err = tx.Exec(ctx, "update snapshots set deleted_at = $2 where id = $1", id, now)
			return err
		})
		switch {
		case errors.Is(err, errSkip):
			continue
		case err != nil:
			failed++
			// The reason is bounded on purpose: the error text carries a
			// path and a provider message, which is not a metric label
			// (I-800).
			reason := "db"
			if strings.Contains(err.Error(), "delete blob") {
				reason = "blob"
			}
			if e.m != nil {
				e.m.SnapshotExpiryFailTotal.WithLabelValues(reason).Inc()
			}
			e.log.Error("snapshot not expired", "event", "snapshot_expiry_fail", "snapshot_id", id.String(), "err", err.Error())
			continue
		}
		deleted = append(deleted, id)
		e.log.Info("snapshot expired", "event", "snapshot_expired", "snapshot_id", id.String())
	}
	return deleted, failed, nil
}

var errSkip = errors.New("snapshot no longer expired")

// UpdateAgeGauge sets repose_api_snapshot_age_seconds to the oldest
// newest-snapshot age over running projects (the SnapshotStale input).
func (e *Expiry) UpdateAgeGauge(ctx context.Context) {
	var oldest *time.Time
	err := e.pool.QueryRow(ctx, `select min(newest) from (select (select max(taken_at) from snapshots s where s.project_id = p.id and s.deleted_at is null) as newest from projects p where p.state = 'running' and p.destroyed_at is null) t`).Scan(&oldest)
	if err != nil || oldest == nil {
		e.m.SnapshotAgeSeconds.Set(0)
		return
	}
	e.m.SnapshotAgeSeconds.Set(e.Now().Sub(*oldest).Seconds())
}

// AzureBlob deletes from the repose-snapshots container.
type AzureBlob struct {
	client    *azblob.Client
	container string
}

// NewAzureBlob connects with the environment's identity; accountURL is
// https://<account>.blob.core.windows.net.
func NewAzureBlob(accountURL, container string, cred azcore.TokenCredential) (*AzureBlob, error) {
	if _, err := url.Parse(accountURL); err != nil || !strings.HasPrefix(accountURL, "https://") {
		return nil, fmt.Errorf("blob account url %q", accountURL)
	}
	if cred == nil {
		c, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential: %w", err)
		}
		cred = c
	}
	client, err := azblob.NewClient(accountURL, cred, nil)
	if err != nil {
		return nil, err
	}
	if container == "" {
		container = "repose-snapshots"
	}
	return &AzureBlob{client: client, container: container}, nil
}

// Delete implements BlobStore.
func (a *AzureBlob) Delete(ctx context.Context, path string) error {
	_, err := a.client.DeleteBlob(ctx, a.container, path, nil)
	if err != nil && bloberror.HasCode(err, bloberror.BlobNotFound) {
		return nil
	}
	return err
}
