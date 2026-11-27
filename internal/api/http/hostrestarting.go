package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/ops"
)

// restartingWindow is how long after a restart through repose the api
// still reads a host as restarting when it has not been heard from since
// (DECISIONS I-1690 amendment): past it, the host's own state says what is
// true (offline after 90 s of silence, or online with its machines back).
const restartingWindow = 15 * time.Minute

// hostRestartingSince is when a restart through repose of this host went
// ahead, while the host's machines have not reported since: between the
// park and the restores after the Mac's boot, hostd is down and its last
// samples still read "running", so without this the dashboard showed a
// parked machine as running with a Stop (review-dash P2 #2). nil when the
// host is not restarting.
func (s *Server) hostRestartingSince(ctx context.Context, hostID uuid.UUID) (*time.Time, error) {
	var at time.Time
	err := s.d.Pool.QueryRow(ctx, `select finished_at from ops
		 where host_id = $1 and kind = $2 and state = 'done' and coalesce((result->>'restarting')::boolean, false)
		   and finished_at > now() - $3::interval
		 order by finished_at desc limit 1`, hostID, ops.KindHostRestart, restartingWindow.String()).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Back once any of its machines has reported after the restart's
	// answer and the few seconds before the Mac went down.
	var back bool
	if err := s.d.Pool.QueryRow(ctx, `select exists (select 1 from meter_samples
		 where project_id in (select id from projects where host_id = $1 and destroyed_at is null)
		   and ts > $2::timestamptz + interval '10 seconds')`, hostID, at).Scan(&back); err != nil {
		return nil, err
	}
	if back {
		return nil, nil
	}
	return &at, nil
}
