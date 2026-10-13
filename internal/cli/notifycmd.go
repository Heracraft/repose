package cli

import (
	"context"
	"fmt"
)

// setNtfyURL builds the **string PatchMeRequest.Notify.NtfyURL (and
// GChatURL) needs to tell "not touching it" (nil outer pointer, omitted)
// from "clearing it" (non-nil outer pointer to a nil inner one, marshals
// to JSON null) from "setting it" (both non-nil).
func setNtfyURL(v string) **string {
	if v == "off" || v == "none" { // "none" is the old spelling (I-619)
		var inner *string
		return &inner
	}
	inner := &v
	return &inner
}

// NotifySetCmd implements `repose notify set --email on|off --ntfy
// <url>|off --gchat <url>|off` (I-8, I-692). email/ntfy/gchat are nil when
// that flag was not given.
func NotifySetCmd(ctx context.Context, e *Env, email *bool, ntfy, gchat *string) error {
	patch := PatchMeRequest{Notify: &NotifyPatch{}}
	if email != nil {
		patch.Notify.Email = email
	}
	if ntfy != nil {
		patch.Notify.NtfyURL = setNtfyURL(*ntfy)
	}
	if gchat != nil {
		patch.Notify.GChatURL = setNtfyURL(*gchat)
	}
	me, err := e.Client.PatchMe(ctx, patch)
	if err != nil {
		return err
	}
	writeNotifySettings(e, me)
	return nil
}

// NotifyShowCmd is bare `repose notify` (I-622): where notifications go.
func NotifyShowCmd(ctx context.Context, e *Env) error {
	me, err := e.Client.GetMe(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, me.Notify)
	}
	writeNotifySettings(e, me)
	return nil
}

func writeNotifySettings(e *Env, me *Me) {
	email := onOff(me.Notify.Email)
	if me.Notify.Email && me.Email != "" {
		email += ", to " + me.Email
	}
	ntfyURL := "off"
	if me.Notify.NtfyURL != nil {
		ntfyURL = *me.Notify.NtfyURL
	}
	_, _ = fmt.Fprintf(e.Out, "email: %s\nntfy: %s\ngchat: %s\n", email, ntfyURL, gchatLine(me.Notify.GChat))
}

// gchatLine is the Google Chat channel in a few words: its space, never
// the webhook URL, which is a credential (I-692).
func gchatLine(g *GChatChannel) string {
	switch {
	case g == nil:
		return "off"
	case g.BrokenAt != nil:
		return "broken since " + g.BrokenAt.Local().Format("2006-01-02") + " (Google refused the webhook)"
	}
	return "on, space " + g.Space
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// NotifyTestCmd implements `repose notify test`. It exits 1 when any
// channel that is on failed (I-622), and says why when the api does.
func NotifyTestCmd(ctx context.Context, e *Env) error {
	res, err := e.Client.NotifyTest(ctx)
	if err != nil {
		return err
	}
	if res.Email == "" && res.Ntfy == "" && res.GChat == "" {
		return exitf(ExitGeneric, "Every channel is off; nothing was sent. `repose notify set --email on`, `--ntfy URL` or `--gchat URL` turns one on.")
	}
	failed := false
	word := func(result, why string) string {
		switch result {
		case "":
			return "off"
		case "ok":
			return "sent"
		}
		failed = true
		if why != "" {
			return "failed: " + why
		}
		return "failed"
	}
	_, _ = fmt.Fprintf(e.Out, "email: %s\nntfy: %s\ngchat: %s\n", word(res.Email, ""), word(res.Ntfy, res.NtfyError), word(res.GChat, res.GChatError))
	if failed {
		return silent(ExitGeneric)
	}
	return nil
}
