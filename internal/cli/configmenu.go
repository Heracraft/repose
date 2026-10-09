package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/heracraft/repose/internal/menu"
)

// MenuItem is one element of a menu selection (docs/interfaces/api.md
// "MenuSelection"): a catalog entry {id, options?} or any nixpkgs package
// {package} (DECISIONS I-220).
type MenuItem struct {
	ID      string            `json:"id,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Package string            `json:"package,omitempty"`
}

func (it MenuItem) name() string {
	if it.Package != "" {
		return it.Package
	}
	return it.ID
}

// menuName turns what the user typed into a catalog id or a nixpkgs
// attribute path. `pkgs.gcc` and `nixpkgs#gcc` mean `gcc`.
func menuName(arg string) string {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "nixpkgs#")
	arg = strings.TrimPrefix(arg, "pkgs.")
	return arg
}

// currentMenu reads the project's selection. A project whose fragment the
// menu did not generate has none; the api refuses the PUT in that case
// unless the fragment is still the default one.
func currentMenu(cfg *ConfigResponse) ([]MenuItem, error) {
	if cfg.Menu == nil {
		return nil, nil
	}
	b, err := json.Marshal(cfg.Menu)
	if err != nil {
		return nil, err
	}
	var sel []MenuItem
	if err := json.Unmarshal(b, &sel); err != nil {
		return nil, fmt.Errorf("reading the project's menu selection: %w", err)
	}
	return sel, nil
}

// joinNames is "gcc", "gcc and air", "gcc, air and zig".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ConfigMenuListCmd is `repose config add` with no names: the menu's
// entries, the names `config add` takes beside any nixpkgs attribute,
// grouped as the dashboard's Config page groups them (review 8.5, I-633).
// It needs no project.
func ConfigMenuListCmd(ctx context.Context, e *Env) error {
	items, err := e.Client.Catalog(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, items)
	}
	var groups []string
	byGroup := map[string][]CatalogItem{}
	width := 0
	for _, it := range items {
		if _, ok := byGroup[it.Group]; !ok {
			groups = append(groups, it.Group)
		}
		byGroup[it.Group] = append(byGroup[it.Group], it)
		width = max(width, len(it.ID))
	}
	for i, g := range groups {
		if i > 0 {
			_, _ = fmt.Fprintln(e.Out)
		}
		_, _ = fmt.Fprintln(e.Out, g)
		for _, it := range byGroup[g] {
			_, _ = fmt.Fprintf(e.Out, "  %-*s  %s\n", width, it.ID, it.Description)
		}
	}
	return nil
}

// ConfigAddCmd implements `repose config add <package>...`: each name is a
// catalog id when the catalog has it, otherwise a nixpkgs attribute path.
// The selection is PUT as the menu and the build streams as for apply.
func ConfigAddCmd(ctx context.Context, e *Env, projectArg string, args []string) error {
	if len(args) == 0 {
		return ConfigMenuListCmd(ctx, e)
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	// The two reads are independent: one round trip, not two (I-326).
	type catalogRead struct {
		items []CatalogItem
		err   error
	}
	catCh := make(chan catalogRead, 1)
	go func() {
		items, err := e.Client.Catalog(ctx)
		catCh <- catalogRead{items, err}
	}()
	cfg, err := e.Client.GetConfig(ctx, project.ID)
	cat := <-catCh
	if err != nil {
		return err
	}
	if cat.err != nil {
		return cat.err
	}
	inCatalog := map[string]bool{}
	for _, c := range cat.items {
		inCatalog[c.ID] = true
	}
	sel, err := currentMenu(cfg)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, it := range sel {
		have[it.name()] = true
	}
	var added, already []string
	for _, arg := range args {
		name := menuName(arg)
		var it MenuItem
		switch {
		case inCatalog[name]:
			it = MenuItem{ID: name}
		case menu.ValidPackage(name):
			it = MenuItem{Package: name}
		default:
			return exitf(ExitUsage, "%q is not a nixpkgs attribute name (letters, digits, _ - + and dots, like gcc or python312Packages.black).", arg)
		}
		if have[name] {
			already = append(already, name)
			continue
		}
		have[name] = true
		sel = append(sel, it)
		added = append(added, name)
	}
	if len(already) > 0 {
		_, _ = fmt.Fprintf(e.Out, "%s %s already in %s.\n", joinNames(already), isAre(len(already)), project.Name)
	}
	if len(added) == 0 {
		return nil
	}
	return putMenuAndRender(ctx, e, project, sel, "Added "+joinNames(added)+" to "+project.Name+".")
}

// ConfigRemoveCmd implements `repose config remove <package>...`.
func ConfigRemoveCmd(ctx context.Context, e *Env, projectArg string, args []string) error {
	if len(args) == 0 {
		return exitf(ExitUsage, "Name at least one package: `repose config remove gcc`.")
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	cfg, err := e.Client.GetConfig(ctx, project.ID)
	if err != nil {
		return err
	}
	sel, err := currentMenu(cfg)
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, arg := range args {
		drop[menuName(arg)] = true
	}
	var kept []MenuItem
	var removed []string
	for _, it := range sel {
		if drop[it.name()] {
			removed = append(removed, it.name())
			delete(drop, it.name())
			continue
		}
		kept = append(kept, it)
	}
	var missing []string
	for _, arg := range args {
		if n := menuName(arg); drop[n] {
			missing = append(missing, n)
			delete(drop, n)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(e.Out, "%s %s not in %s's menu.\n", joinNames(missing), isAre(len(missing)), project.Name)
	}
	if len(removed) == 0 {
		if cfg.Menu == nil {
			return exitf(ExitUsage, "%s's repose.nix was written by hand; remove the package with `repose config edit`.", project.Name)
		}
		return silent(ExitUsage)
	}
	if kept == nil {
		kept = []MenuItem{}
	}
	return putMenuAndRender(ctx, e, project, kept, "Removed "+joinNames(removed)+" from "+project.Name+".")
}

// putMenuAndRender PUTs a selection and streams its build. A build that
// fails prints the summary line (for a package nixpkgs lacks, the
// fragment's own "nixpkgs has no package" line) without the generated
// fragment's line number, which names nothing the user wrote.
func putMenuAndRender(ctx context.Context, e *Env, project *Project, sel []MenuItem, done string) error {
	revisionID, opID, err := e.Client.PutConfigMenu(ctx, project.ID, sel)
	if err != nil {
		var apiErr *APIError
		if asAPIError(err, &apiErr) {
			switch {
			case apiErr.Code == "conflict" && strings.Contains(apiErr.Message, "custom fragment"):
				return exitf(ExitUsage, "%s's repose.nix was written by hand, so the menu is off. Add packages with `repose config edit` (home.packages = [ pkgs.gcc ];).", project.Name)
			case apiErr.Code == "invalid":
				return exitf(ExitUsage, "%s", apiErr.Message)
			}
		}
		return err
	}
	if opID == "" {
		_, _ = fmt.Fprintf(e.Out, "%s Nothing to build; %s is still active.\n", done, shortRev(revisionID))
		return nil
	}
	_, _ = fmt.Fprintf(e.Out, "%s Building revision %s ...\n", done, shortRev(revisionID))
	op, pr, err := waitConfigOp(ctx, e, project, opID)
	if err != nil {
		return err
	}
	if op.State == "error" {
		summary := genLocRe.ReplaceAllString(firstLine(op.Error.Message), "")
		_, _ = fmt.Fprintf(e.Out, "%s%s\n", buildErrorPrefix(op.Error.Code), summary)
		if rest := strings.Trim(restAfterFirstLine(op.Error.Message), "\n"); rest != "" && !strings.HasPrefix(summary, "nixpkgs ") {
			_, _ = fmt.Fprintf(e.Out, "\n%s\n", rest)
		}
		_, _ = fmt.Fprintf(e.Out, "Nothing changed in %s; the previous revision is still active.\n", project.Name)
		return silent(ExitBuildFailed)
	}
	printApplied(e, project, op, revisionID, pr)
	return nil
}

// genLocRe is the " at fragment.nix:L:C" hostd appends to a summary.
var genLocRe = regexp.MustCompile(` at fragment\.nix:\d+(:\d+)?`)

// shortRev is a revision as lines name it: the random end of its UUIDv7,
// whose start changes only once a minute (DECISIONS I-619).
func shortRev(id string) string { return shortID(id) }
