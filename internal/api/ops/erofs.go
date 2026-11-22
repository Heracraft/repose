package ops

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// erofs guests (DECISIONS I-1458, I-1462, I-1464): an erofs project boots
// a published image and never builds. Its create, start and restore carry
// a BaseImage for the host's arch and its menu as a MenuSpec, and a menu
// change is an ApplyConfig with the menu (docs/interfaces/grpc-hostd.md
// "erofs guests").

// hostArches maps image arches to the host arch names hosts.arch holds.
var hostArches = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}

// ErofsHostArches lists the host arches (x86_64, aarch64) some erofs base
// has an image for: the arches placement may put an erofs project on.
func ErofsHostArches(ctx context.Context, q store.Querier) ([]string, error) {
	var out []string
	for _, a := range []string{"amd64", "arm64"} {
		_, err := store.LatestErofsBase(ctx, q, a)
		if errors.Is(err, db.ErrNotFound) || db.IsNoRows(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, hostArches[a])
	}
	return out, nil
}

// hostImageArch is the image arch (amd64, arm64) of a project's host.
func (e *Engine) hostImageArch(ctx context.Context, hostID uuid.UUID) (string, error) {
	var arch *string
	if err := e.pool.QueryRow(ctx, "select arch from hosts where id = $1", hostID).Scan(&arch); err != nil {
		return "", err
	}
	if arch == nil {
		return "", &opError{code: "internal", msg: "the host has not reported its architecture"}
	}
	a := store.ImageArch(*arch)
	if a == "" {
		return "", &opError{code: "internal", msg: "the host's architecture has no erofs image"}
	}
	return a, nil
}

// erofsBase picks the base an erofs guest boots on hostID: the version the
// op fixed on its first send (so a re-send is identical), else the
// project's pending base, else its current one, else the newest erofs
// base for the host's arch. A named version without files for the arch
// falls back to the newest that has them.
func (e *Engine) erofsBase(ctx context.Context, op *store.Op, p *store.Project, hostID uuid.UUID) (*hostdv1.BaseImage, error) {
	arch, err := e.hostImageArch(ctx, hostID)
	if err != nil {
		return nil, err
	}
	var want []string
	if v, ok := op.Params["base_image_version"].(string); ok && v != "" {
		want = append(want, v)
	}
	if op.Kind != KindCreate {
		if p.PendingBaseVersion != nil {
			want = append(want, *p.PendingBaseVersion)
		}
		if p.BaseVersion != nil {
			want = append(want, *p.BaseVersion)
		}
	}
	for _, v := range want {
		b, err := store.GetBase(ctx, e.pool, v)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if bi, ok := baseImage(b, arch); ok {
			op.Params["base_image_version"] = bi.Version
			return bi, nil
		}
	}
	b, err := store.LatestErofsBase(ctx, e.pool, arch)
	if errors.Is(err, db.ErrNotFound) {
		return nil, &opError{code: "internal", msg: "no erofs base is published for " + arch + "; run `repose-admin base publish --format erofs`"}
	}
	if err != nil {
		return nil, err
	}
	bi, ok := baseImage(b, arch)
	if !ok {
		return nil, &opError{code: "internal", msg: "base " + b.Version + " has no " + arch + " image"}
	}
	op.Params["base_image_version"] = bi.Version
	return bi, nil
}

// baseImage is the wire form of an erofs row's files for arch.
func baseImage(b *store.BaseVersion, arch string) (*hostdv1.BaseImage, bool) {
	if b.Format != store.RuntimeErofs {
		return nil, false
	}
	f, ok := b.Files(arch)
	if !ok {
		return nil, false
	}
	kv := ""
	if b.KernelVersion != nil {
		kv = *b.KernelVersion
	}
	return &hostdv1.BaseImage{
		Version: b.Version, Url: f.RootfsURL, Sha256: f.RootfsSHA256, KernelVersion: kv,
		InitramfsUrl: f.InitramfsURL, InitramfsSha256: f.InitramfsSHA256,
		KernelUrl: f.KernelURL, KernelSha256: f.KernelSHA256,
	}, true
}

// MenuSpecOf renders a revision's menu (config_revisions.menu, whose
// "selection" is the api's MenuSelection) as the wire's MenuSpec. A
// revision with no menu is an empty spec: the guest installs nothing.
func MenuSpecOf(rev *store.Revision) *hostdv1.MenuSpec {
	spec := &hostdv1.MenuSpec{}
	if rev == nil {
		return spec
	}
	spec.RevisionId = rev.ID.String()
	sel, ok := rev.Menu["selection"]
	if !ok || sel == nil {
		return spec
	}
	raw, err := json.Marshal(sel)
	if err != nil {
		return spec
	}
	var items []struct {
		ID      string            `json:"id"`
		Options map[string]string `json:"options"`
		Package string            `json:"package"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return spec
	}
	for _, it := range items {
		spec.Items = append(spec.Items, &hostdv1.MenuItem{Id: it.ID, Options: it.Options, Package: it.Package})
	}
	return spec
}

// erofsStartRevision is the revision whose menu an erofs start sends: the
// newest built or applied one. A menu change made while the machine was
// stopped waits as built and is applied by this start.
func (e *Engine) erofsStartRevision(ctx context.Context, p *store.Project) (*store.Revision, error) {
	var rid uuid.UUID
	err := e.pool.QueryRow(ctx, `select id from config_revisions where project_id = $1 and status in ('built', 'applied') order by created_at desc limit 1`, p.ID).Scan(&rid)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return store.GetRevision(ctx, e.pool, rid)
}

// menuWarning turns a boot's menu_error into the warning a start or
// create leaves on the project (as I-590's boot fallback does): the
// machine runs, and its menu did not apply.
func menuWarning(me *hostdv1.Error) map[string]any {
	if me == nil || me.Code == "" {
		return nil
	}
	return map[string]any{"code": me.Code, "message": "the machine started, but its configuration did not apply: " + me.Message}
}

// onErofsBooted records what an erofs create or start booted: the base
// (clearing pending_base_version once it is the one booted) and the menu
// revision, applied, unless the menu failed.
func (e *Engine) onErofsBooted(ctx context.Context, op *store.Op, p *store.Project, booted string, menuErr *hostdv1.Error) error {
	if booted == "" {
		booted, _ = op.Params["base_image_version"].(string)
	}
	if booted != "" {
		if _, err := e.pool.Exec(ctx, `update projects set base_version = $2,
			pending_base_version = case when pending_base_version = $2 then null else pending_base_version end where id = $1`, p.ID, booted); err != nil {
			return err
		}
	}
	if w := menuWarning(menuErr); w != nil {
		op.Params["warning"] = w
		if _, err := e.pool.Exec(ctx, "update projects set last_error = $2 where id = $1", p.ID, w["code"].(string)+": "+w["message"].(string)); err != nil {
			return err
		}
		if rs, ok := op.Params["menu_revision_id"].(string); ok && rs != "" && op.Kind != KindCreate {
			if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'failed', error = $2 where id = $1 and status = 'built'", rs, menuErr.Code+": "+menuErr.Message); err != nil {
				return err
			}
		}
		return e.setResult(ctx, op, map[string]any{"warning": w})
	}
	if rs, ok := op.Params["menu_revision_id"].(string); ok && rs != "" {
		rid, err := uuid.Parse(rs)
		if err != nil {
			return nil
		}
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'applied', applied_at = now() where id = $1", rid); err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update config_revisions set status = 'built' where project_id = $1 and id <> $2 and status = 'applied'", p.ID, rid); err != nil {
			return err
		}
		if _, err := e.pool.Exec(ctx, "update projects set config_revision_id = $2 where id = $1", p.ID, rid); err != nil {
			return err
		}
	}
	return nil
}

// buildApplyMenu is an erofs project's apply_config (DECISIONS I-1464):
// the revision's menu, applied inside the running guest. Skipped when the
// machine is not running (the revision waits as built and the next start
// sends its menu) and for a start, whose StartGuest already carried it.
func (e *Engine) buildApplyMenu(ctx context.Context, op *store.Op, p *store.Project) (*hostdv1.Command, uuid.UUID, bool, error) {
	if op.Kind == KindStart || p.GuestID == nil || p.HostID == nil || p.State != "running" {
		return nil, uuid.Nil, true, nil
	}
	rev, err := e.revisionFor(ctx, op, p)
	if err != nil {
		return nil, uuid.Nil, false, err
	}
	if p.ConfigRevisionID != nil && *p.ConfigRevisionID == rev.ID && rev.Status == "applied" && op.Kind != KindApply {
		return nil, uuid.Nil, true, nil
	}
	return &hostdv1.Command{CommandId: newCommandID(), Cmd: &hostdv1.Command_ApplyConfig{ApplyConfig: &hostdv1.ApplyConfig{GuestId: p.GuestID.String(), Menu: MenuSpecOf(rev)}}}, *p.HostID, false, nil
}
