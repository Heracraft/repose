// Package config implements ApplyMenu on an erofs guest
// (docs/interfaces/vsock-guestd.md, DECISIONS I-1460, I-1476): it makes
// the guest's packages, runtimes and services match the project's menu,
// by the recipes in recipes.yaml, and streams a line per step.
package config

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed recipes.yaml
var recipesYAML []byte

// Kinds a recipe may have.
const (
	KindRuntime = "runtime"
	KindService = "service"
	KindTool    = "tool"
	KindBase    = "base"
)

// Recipe is one entry of recipes.yaml; the file's header says what each
// field does.
type Recipe struct {
	ID         string               `yaml:"id"`
	Kind       string               `yaml:"kind"`
	Options    map[string]OptionSet `yaml:"options"`
	AptSources []AptSource          `yaml:"apt_sources"`
	Pre        string               `yaml:"pre"`
	Apt        []string             `yaml:"apt"`
	Download   *Download            `yaml:"download"`
	Mise       []string             `yaml:"mise"`
	Files      []File               `yaml:"files"`
	Service    *Service             `yaml:"service"`
	Post       string               `yaml:"post"`
	PostDev    string               `yaml:"post_dev"`
	Check      string               `yaml:"check"`
	Remove     string               `yaml:"remove"`
	Ready      string               `yaml:"ready"`
}

// OptionSet is the values an option takes and its default.
type OptionSet struct {
	Values  []string `yaml:"values"`
	Default string   `yaml:"default"`
}

// AptSource is a signed apt repository a recipe needs.
type AptSource struct {
	Name      string `yaml:"name"`
	KeyURL    string `yaml:"key_url"`
	KeySHA256 string `yaml:"key_sha256"`
	Line      string `yaml:"line"`
}

// Download is an upstream file pinned by its sha256, per architecture.
type Download struct {
	Name    string            `yaml:"name"`
	Version string            `yaml:"version"`
	Install string            `yaml:"install"`
	AMD64   *DownloadArtifact `yaml:"amd64"`
	ARM64   *DownloadArtifact `yaml:"arm64"`
}

// DownloadArtifact is one architecture's file.
type DownloadArtifact struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}

// File is a file a recipe writes as root.
type File struct {
	Path    string `yaml:"path"`
	Mode    string `yaml:"mode"`
	Content string `yaml:"content"`
}

// Service is a recipe's systemd unit.
type Service struct {
	User          string   `yaml:"user"`
	Port          int      `yaml:"port"`
	Data          string   `yaml:"data"`
	Mask          []string `yaml:"mask"`
	Unit          string   `yaml:"unit"`
	ReadyTimeoutS int      `yaml:"ready_timeout_s"`
}

// Recipes is the loaded file.
type Recipes struct {
	Version int      `yaml:"version"`
	List    []Recipe `yaml:"recipes"`
	byID    map[string]*Recipe
}

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	unitRe    = regexp.MustCompile(`^[A-Za-z0-9@._{}-]+\.service$`)
	installRe = regexp.MustCompile(`^(binary|awscli)$`)
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// LoadRecipes parses the embedded recipes.
func LoadRecipes() (*Recipes, error) { return ParseRecipes(recipesYAML) }

// ParseRecipes parses and checks a recipes file: known kinds, unique
// ids, every template parses, every pin is a sha256.
func ParseRecipes(b []byte) (*Recipes, error) {
	var r Recipes
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse recipes: %w", err)
	}
	if r.Version != 1 {
		return nil, fmt.Errorf("recipes: version %d, want 1", r.Version)
	}
	r.byID = map[string]*Recipe{}
	for i := range r.List {
		rec := &r.List[i]
		if err := rec.check(); err != nil {
			return nil, fmt.Errorf("recipe %q: %w", rec.ID, err)
		}
		if _, dup := r.byID[rec.ID]; dup {
			return nil, fmt.Errorf("recipe %q twice", rec.ID)
		}
		r.byID[rec.ID] = rec
	}
	return &r, nil
}

// Get is the recipe for a catalog id.
func (r *Recipes) Get(id string) (*Recipe, bool) {
	rec, ok := r.byID[id]
	return rec, ok
}

func (rec *Recipe) check() error {
	if !idRe.MatchString(rec.ID) {
		return fmt.Errorf("bad id")
	}
	switch rec.Kind {
	case KindRuntime, KindService, KindTool, KindBase:
	default:
		return fmt.Errorf("unknown kind %q", rec.Kind)
	}
	for name, o := range rec.Options {
		if !nameRe.MatchString(name) || len(o.Values) == 0 || !contains(o.Values, o.Default) {
			return fmt.Errorf("option %q: needs values and a default among them", name)
		}
	}
	for _, s := range rec.AptSources {
		if !nameRe.MatchString(s.Name) || !sha256Re.MatchString(s.KeySHA256) || !strings.HasPrefix(s.KeyURL, "https://") || s.Line == "" {
			return fmt.Errorf("apt source %q: needs a name, an https key_url, its sha256 and a line", s.Name)
		}
	}
	if d := rec.Download; d != nil {
		if !nameRe.MatchString(d.Name) || !installRe.MatchString(d.Install) || d.AMD64 == nil || d.ARM64 == nil {
			return fmt.Errorf("download: needs a name, install binary|awscli, amd64 and arm64")
		}
		for _, a := range []*DownloadArtifact{d.AMD64, d.ARM64} {
			if !strings.HasPrefix(a.URL, "https://") || !sha256Re.MatchString(a.SHA256) {
				return fmt.Errorf("download: every file needs an https url and its sha256")
			}
		}
		if d.Install == "awscli" && rec.Check == "" {
			return fmt.Errorf("download: an awscli install needs a check")
		}
	}
	if s := rec.Service; s != nil {
		if rec.Kind != KindService {
			return fmt.Errorf("a service block on kind %s", rec.Kind)
		}
		if !userRe.MatchString(s.User) || s.Port <= 0 || s.Port > 65535 || s.Unit == "" {
			return fmt.Errorf("service: needs a user, a port and unit lines")
		}
		for _, m := range s.Mask {
			if !unitRe.MatchString(m) {
				return fmt.Errorf("service: bad unit to mask %q", m)
			}
		}
	} else if rec.Kind == KindService {
		return fmt.Errorf("kind service without a service block")
	}
	for _, f := range rec.Files {
		if !strings.HasPrefix(f.Path, "/") || f.Mode == "" {
			return fmt.Errorf("file %q: needs an absolute path and a mode", f.Path)
		}
	}
	if rec.Ready == "" {
		return fmt.Errorf("no ready line")
	}
	// Every template parses, with every option at its default.
	_, err := rec.Render(rec.Defaults(nil), Env{Arch: "amd64", Codename: "noble"})
	return err
}

// Defaults fills in the options an item left out.
func (rec *Recipe) Defaults(opts map[string]string) map[string]string {
	out := map[string]string{}
	for name, o := range rec.Options {
		out[name] = o.Default
	}
	for k, v := range opts {
		out[k] = v
	}
	return out
}

// ValidateOptions refuses an option the recipe has not, or a value it
// does not list. Option values reach shell lines, so this is the only
// gate between a request and a command.
func (rec *Recipe) ValidateOptions(opts map[string]string) error {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o, ok := rec.Options[k]
		if !ok {
			return fmt.Errorf("%s has no option %q", rec.ID, k)
		}
		if !contains(o.Values, opts[k]) {
			return fmt.Errorf("%s option %s is one of %s", rec.ID, k, strings.Join(o.Values, ", "))
		}
	}
	return nil
}

// Env is what a template sees beside the options.
type Env struct {
	Arch     string
	Codename string
	// ServicesDir is /var/lib/repose/services on the guest; empty means
	// that path.
	ServicesDir string
}

// Rendered is a recipe with every template filled in.
type Rendered struct {
	Recipe     *Recipe
	Options    map[string]string
	AptSources []AptSource
	Pre        string
	Apt        []string
	Download   *DownloadArtifact
	Mise       []string
	Files      []File
	DataDir    string
	Unit       string
	Mask       []string
	Post       string
	PostDev    string
	Check      string
	Remove     string
	Ready      string
}

// Render fills the recipe's templates for opts.
func (rec *Recipe) Render(opts map[string]string, env Env) (*Rendered, error) {
	vars := map[string]string{"arch": env.Arch, "codename": env.Codename}
	for k, v := range opts {
		vars[k] = v
	}
	out := &Rendered{Recipe: rec, Options: opts}
	var err error
	t := func(name, text string) string {
		if err != nil || text == "" {
			return ""
		}
		var s string
		s, err = render(rec.ID+"."+name, text, vars)
		return s
	}
	if rec.Service != nil {
		base := env.ServicesDir
		if base == "" {
			base = "/var/lib/repose/services"
		}
		dir := base + "/" + rec.ID
		if sub := t("service.data", rec.Service.Data); sub != "" {
			dir += "/" + sub
		}
		out.DataDir = dir
	}
	vars["data"] = out.DataDir
	for _, s := range rec.AptSources {
		v := map[string]string{"key": keyringPath(s.Name)}
		for k, x := range vars {
			v[k] = x
		}
		line, e := render(rec.ID+".apt_sources", s.Line, v)
		if e != nil && err == nil {
			err = e
		}
		out.AptSources = append(out.AptSources, AptSource{Name: s.Name, KeyURL: s.KeyURL, KeySHA256: s.KeySHA256, Line: line})
	}
	out.Pre = t("pre", rec.Pre)
	for _, a := range rec.Apt {
		out.Apt = append(out.Apt, t("apt", a))
	}
	if d := rec.Download; d != nil {
		if env.Arch == "arm64" {
			out.Download = d.ARM64
		} else {
			out.Download = d.AMD64
		}
	}
	for _, m := range rec.Mise {
		out.Mise = append(out.Mise, t("mise", m))
	}
	for _, f := range rec.Files {
		out.Files = append(out.Files, File{Path: t("files.path", f.Path), Mode: f.Mode, Content: t("files.content", f.Content)})
	}
	if s := rec.Service; s != nil {
		out.Unit = t("service.unit", s.Unit)
		for _, m := range s.Mask {
			out.Mask = append(out.Mask, t("service.mask", m))
		}
	}
	out.Post = t("post", rec.Post)
	out.PostDev = t("post_dev", rec.PostDev)
	out.Check = t("check", rec.Check)
	out.Remove = t("remove", rec.Remove)
	out.Ready = t("ready", rec.Ready)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func keyringPath(name string) string { return "/etc/apt/keyrings/repose-" + name + ".asc" }

func render(name, text string, vars map[string]string) (string, error) {
	tpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	var b strings.Builder
	if err := tpl.Execute(&b, vars); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return b.String(), nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
