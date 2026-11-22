package hostd

import (
	"regexp"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// erofs guests (DECISIONS I-1458, docs/interfaces/grpc-hostd.md "erofs
// guests"). The fake refuses what hostd refuses, with the same code:
// base_image beside system_closure, a base_image on a host that does not
// name erofs, a malformed base_image, and an ApplyConfig with both or the
// wrong one of system_closure and menu. A fake that says yes where hostd
// says no turns a test into a phantom.

var (
	baseVersionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	sha256Re      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// MsgBothSet is the invalid_argument message for a command that names a
// base image and a closure.
const MsgBothSet = "base_image and system_closure are both set; an erofs guest boots a base image, a Nix guest a closure"

// checkBaseImage is hostd's check of a BaseImage: a version label and
// three files, each with a 64-hex-digit SHA-256.
func checkBaseImage(b *hostdv1.BaseImage) string {
	if !baseVersionRe.MatchString(b.GetVersion()) {
		return "base_image.version must match [A-Za-z0-9._-]{1,64}"
	}
	for _, f := range []struct{ name, url, sum string }{
		{"url", b.GetUrl(), b.GetSha256()},
		{"kernel_url", b.GetKernelUrl(), b.GetKernelSha256()},
		{"initramfs_url", b.GetInitramfsUrl(), b.GetInitramfsSha256()},
	} {
		if f.url == "" {
			return "base_image." + f.name + " is empty"
		}
		if !sha256Re.MatchString(f.sum) {
			return "base_image." + f.name + "'s sha256 is not 64 lower-case hex digits"
		}
	}
	return ""
}

// refuseShape answers a command whose erofs fields hostd refuses, or nil.
func (f *Fake) refuseShape(id string, cmd *hostdv1.Command) *hostdv1.Result {
	var bi *hostdv1.BaseImage
	var closure string
	switch c := cmd.Cmd.(type) {
	case *hostdv1.Command_CreateGuest:
		bi, closure = c.CreateGuest.GetBaseImage(), c.CreateGuest.GetSystemClosure()
	case *hostdv1.Command_StartGuest:
		bi = c.StartGuest.GetBaseImage()
	case *hostdv1.Command_Restore:
		bi, closure = c.Restore.GetBaseImage(), c.Restore.GetSystemClosure()
	case *hostdv1.Command_ApplyConfig:
		a := c.ApplyConfig
		if a.GetMenu() != nil && a.GetSystemClosure() != "" {
			return errResult(id, "invalid_argument", "apply_config takes system_closure or menu, not both")
		}
		return nil
	case *hostdv1.Command_Build:
		if f.opts.NoNix {
			return errResult(id, "invalid_argument", "this host does not build Nix guests")
		}
		return nil
	default:
		return nil
	}
	if bi == nil {
		if closure != "" && f.opts.NoNix {
			return errResult(id, "invalid_argument", "this host does not run Nix guests")
		}
		return nil
	}
	if closure != "" {
		return errResult(id, "invalid_argument", MsgBothSet)
	}
	if !f.opts.Erofs {
		return errResult(id, "invalid_argument", "this host does not run erofs guests")
	}
	if msg := checkBaseImage(bi); msg != "" {
		return errResult(id, "invalid_argument", msg)
	}
	return nil
}

// SetMenuFail makes every menu application fail with code and msg
// ("" clears it): an ApplyConfig answers the error, and a boot's replay
// reports it as menu_error with the guest running, as hostd does.
func (f *Fake) SetMenuFail(code, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.menuFailCode, f.menuFailMsg = code, msg
}

// menuError is the boot's menu_error, nil without a menu or a failure.
// Called with f.mu held.
func (f *Fake) menuError(m *hostdv1.MenuSpec) *hostdv1.Error {
	if m == nil || f.menuFailCode == "" {
		return nil
	}
	return &hostdv1.Error{Code: f.menuFailCode, Message: f.menuFailMsg}
}
