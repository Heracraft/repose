package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/heracraft/repose/internal/hostd/freeze"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/mount"
	"github.com/heracraft/repose/internal/hostd/schedev"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// FreezeOptions are hostd's guest freeze flags (DECISIONS I-652, I-653).
type FreezeOptions struct {
	// Enabled freezes running guests when the host shuts down and on
	// `hostd freeze`, and restores them at the next start.
	Enabled bool
	// KeyVaultURL and KeyName name the RSA key that wraps image keys; the
	// host's managed identity (BlobIdentity) needs Get and UnwrapKey on it.
	KeyVaultURL string
	KeyName     string
	// KeyFile is an RSA private key (PEM) used instead of Key Vault, for
	// staging and tests; it must not live on the host's disks.
	KeyFile string
	// Deadline bounds one FreezeAll.
	Deadline time.Duration
	// FreePageReporting gives guests started from now on a balloon with
	// free page reporting.
	FreePageReporting bool
	// ScheduledEvents runs the Azure Scheduled Events listener.
	ScheduledEvents bool
	// VMName is this VM's name in Scheduled Events' Resources.
	VMName string
}

// freezeSetup builds the guest freeze's config and deps. With the freeze
// off it still returns the image volume, so images a host left behind
// when the freeze was turned off are removed at the next start.
func freezeSetup(o Options, r shell.Runner, l lvm.LVM, log *slog.Logger) (guest.FreezeConfig, guest.FreezeDeps) {
	cfg := guest.FreezeConfig{Enabled: o.Freeze.Enabled, RecordDir: filepath.Join(o.StateDir, "freeze"), Deadline: o.Freeze.Deadline}
	deps := guest.FreezeDeps{
		Image:       &freeze.Image{R: r, LVM: l, Mount: &mount.Real{R: r}},
		Fingerprint: freeze.Fingerprint,
	}
	if !o.Freeze.Enabled {
		return cfg, deps
	}
	switch {
	case o.Freeze.KeyFile != "":
		deps.Sealer = &freeze.FileKey{Path: o.Freeze.KeyFile}
	case o.Freeze.KeyVaultURL != "" && o.Freeze.KeyName != "":
		kv, err := freeze.NewKeyVault(o.Freeze.KeyVaultURL, o.Freeze.KeyName, o.BlobIdentity)
		if err != nil {
			log.Error("guest freeze off: key vault", "event", "freeze", "err", err.Error())
			cfg.Enabled = false
			return cfg, deps
		}
		deps.Sealer = kv
	default:
		log.Error("guest freeze off: no image key (--freeze-key-vault and --freeze-key-name, or --freeze-key-file)", "event", "freeze")
		cfg.Enabled = false
	}
	return cfg, deps
}

// freezeKeyLoop keeps the wrap key's public half fresh, so a freeze at
// shutdown needs no network: at start, then hourly, with retries a
// minute apart until the first success.
func freezeKeyLoop(ctx context.Context, s freeze.Sealer, log *slog.Logger) {
	if s == nil {
		return
	}
	wait := time.Duration(0)
	ok := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.Refresh(rctx)
		cancel()
		switch {
		case err != nil && !ok:
			log.Warn("image key not fetched; guests cannot be frozen yet", "event", "freeze", "err", err.Error())
			wait = time.Minute
		case err != nil:
			log.Warn("image key not refreshed; using the one fetched before", "event", "freeze", "err", err.Error())
			wait = time.Hour
		default:
			if !ok {
				log.Info("image key fetched; guests can be frozen", "event", "freeze")
			}
			ok = true
			wait = time.Hour
		}
	}
}

// Freeze implements control.FreezeBackend.
func (d *Daemon) Freeze(ctx context.Context, reason string) ([]guest.FreezeResult, error) {
	if !d.mgr.FreezeEnabled() {
		if reason == guest.FreezeShutdown {
			return nil, nil // the shutdown unit asks on every host; off is a quiet no
		}
		return nil, errors.New("the guest freeze is off on this host (repose.host.freeze.enable)")
	}
	return d.mgr.FreezeAll(ctx, reason), nil
}

// Thaw implements control.FreezeBackend.
func (d *Daemon) Thaw(ctx context.Context) ([]guest.FreezeResult, error) {
	res := d.mgr.ThawAll(ctx)
	// Guests restored here were frozen with hostd running: reconcile so
	// their monitors and shapes are as at a start.
	return res, d.mgr.Reconcile(ctx)
}

// eventHost adapts the manager to the Scheduled Events listener.
type eventHost struct{ m *guest.Manager }

func (e eventHost) FreezeForEvent(ctx context.Context) {
	e.m.FreezeAll(ctx, guest.FreezeScheduledEvent)
}
func (e eventHost) ThawNow(ctx context.Context) {
	e.m.ThawAll(ctx)
	_ = e.m.Reconcile(ctx) // logged inside; the guests restored are running either way
}
func (e eventHost) SyncClocks(ctx context.Context) { e.m.SyncClocks(ctx) }

func scheduledEvents(o Options, m *guest.Manager, log *slog.Logger) *schedev.Listener {
	if !o.Freeze.ScheduledEvents {
		return nil
	}
	name := o.Freeze.VMName
	if name == "" {
		name, _ = os.Hostname() // empty matches every event, which is right for a lone VM
	}
	return &schedev.Listener{Resource: name, Host: eventHost{m}, Log: log}
}
