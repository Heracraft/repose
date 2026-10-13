package events

import (
	"context"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// Channels lists the channels a user's notifications go to, in the order
// their outbox rows are written: email when notify_email is on and the
// account has an address, ntfy when a URL is set. Every place that queues
// a notification or counts the user's channels reads this one list
// (enqueue, InsertUserNotice, the questions store's no_channel check), so
// a new channel is added here once (DECISIONS I-691).
func Channels(ctx context.Context, q store.Querier, userID uuid.UUID) ([]string, error) {
	var notifyEmail bool
	var email, ntfy *string
	if err := q.QueryRow(ctx, "select notify_email, email, ntfy_url from users where id = $1", userID).Scan(&notifyEmail, &email, &ntfy); err != nil {
		return nil, err
	}
	var channels []string
	if notifyEmail && email != nil && *email != "" {
		channels = append(channels, "email")
	}
	if ntfy != nil && *ntfy != "" {
		channels = append(channels, "ntfy")
	}
	return channels, nil
}

// DropQueued deletes the user's queued deliveries on channel: their
// projects' events and their user notices, never account mail, which is
// transactional and ignores the notify settings (I-269). Turning a channel
// off calls it, so nothing more reaches a channel the user just turned
// off (DECISIONS I-691: before, a queued machines_restarted notice, which
// names no project, still went out).
func DropQueued(ctx context.Context, q store.Querier, userID uuid.UUID, channel string) error {
	_, err := q.Exec(ctx, `delete from events_outbox o using events e left join projects p on p.id = e.project_id
		where o.event_id = e.id and o.channel = $2 and coalesce(p.user_id, e.user_id) = $1 and not (e.kind = any($3))`,
		userID, channel, accountKindList())
	return err
}

func accountKindList() []string {
	out := make([]string, 0, len(AccountKinds))
	for k := range AccountKinds {
		out = append(out, k)
	}
	return out
}
