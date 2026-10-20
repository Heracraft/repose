package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// cmdStatus answers what the box is, which projects it holds and how much
// undo space is left (SPINE.md "Contract 3").
func cmdStatus(ctx context.Context, env *Env, args []string) error {
	if err := noArgs(env, "status", args); err != nil {
		return err
	}
	machine, err := readMachine(env.MachineFile)
	if err != nil {
		return err
	}
	b, err := env.backend()
	if err != nil {
		return err
	}
	slugs, err := projectSlugs(env.Store)
	if err != nil {
		return err
	}
	projects := make([]wireProject, 0, len(slugs))
	for _, slug := range slugs {
		points, err := b.List(ctx, slug)
		if err != nil {
			return env.fromBackend(err, "list a project's points")
		}
		projects = append(projects, wireProject{Slug: slug, Points: len(points)})
	}
	space, err := b.Space(ctx)
	if err != nil {
		return env.fromBackend(err, "read the store's space")
	}
	return emit(env.Stdout, wireStatus{Machine: machine, Projects: projects, Space: wireSpaceOf(space)})
}

func readMachine(path string) (wireMachine, error) {
	var m wireMachine
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, errf(codeNotFound, "%s is missing", path)
	}
	if err != nil {
		return m, errf(codeInternal, "read the machine record: %v", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errf(codeInternal, "parse the machine record: %v", err)
	}
	return m, nil
}

// projectSlugs lists the projects the store holds. Contract 1 has no call for
// it: the live areas are the directories under <store>/projects, which is the
// path `project init` answers with. os.ReadDir sorts them by name.
func projectSlugs(store string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(store, "projects"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errf(codeInternal, "read the store's projects: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !slugRe.MatchString(e.Name()) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}
