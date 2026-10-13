package events

import (
	"context"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// Channel names, as events_outbox.channel, events.delivered's keys and
// questions.answered_via spell them.
const (
	ChannelEmail = "email"
	ChannelNtfy  = "ntfy"
	ChannelGChat = "gchat"
)

// channelsSQL says which of a user's channels are on: email with
// notify_email and an address, ntfy with a URL, Google Chat with a webhook
// Google has not refused (DECISIONS I-692). Every place that writes an
// outbox row for a user's notification, and the question's channel count,
// reads it here, so a new channel is added once.
const channelsSQL = `select u.notify_email and coalesce(u.email, '') <> '', coalesce(u.ntfy_url, '') <> '', coalesce(u.gchat_url, '') <> '' and u.gchat_broken_at is null from users u where `

// UserChannels returns the channels user has on, in a fixed order.
func UserChannels(ctx context.Context, q store.Querier, userID uuid.UUID) ([]string, error) {
	return channels(ctx, q, "u.id = $1", userID)
}

// ProjectChannels returns the channels the owner of project has on.
func ProjectChannels(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]string, error) {
	return channels(ctx, q, "u.id = (select user_id from projects where id = $1)", projectID)
}

func channels(ctx context.Context, q store.Querier, where string, id uuid.UUID) ([]string, error) {
	var email, ntfy, gchat bool
	if err := q.QueryRow(ctx, channelsSQL+where, id).Scan(&email, &ntfy, &gchat); err != nil {
		return nil, err
	}
	var out []string
	if email {
		out = append(out, ChannelEmail)
	}
	if ntfy {
		out = append(out, ChannelNtfy)
	}
	if gchat {
		out = append(out, ChannelGChat)
	}
	return out, nil
}
