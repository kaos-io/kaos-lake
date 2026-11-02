// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05; dataset layouts): new file, Apache-2.0.

// Package templates serves the dataset layouts (the "dataset templates" of the UI and the CLI):
// the built-in ones (shared/builtin: yolo, raw, the same files kaos-lib ships) and the project's
// own, from main of the project's lakeFS repository "layouts" (<name>/layout.yaml each). A layout
// is a declarative spec (shared/layout.schema.json). This package renders a new dataset's files
// from one, reads a dataset's form file back into its values, and checks a dataset against the
// layout's declarative rules. It writes nothing: the UI and the CLI commit the files with the
// person's own identity. It never runs a layout's Python hook; kaos-lib does that.
package templates

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

//go:embed shared
var shared embed.FS

// Sources of a layout.
const (
	SourceBuiltin = "builtin"
	SourceProject = "project"
)

// legacy holds ids datasets may still carry from before a layout got its current name: they
// resolve to that layout and are never listed.
var legacy = map[string]string{"ultralytics": "yolo", "empty": "raw"}

// Canonical returns the current name of a layout id.
func Canonical(name string) string {
	if c, ok := legacy[name]; ok {
		return c
	}
	return name
}

// Layout is one layout as the API serves it: what the New dataset page shows, and the whole spec
// for kaos-lib.
type Layout struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Version     string   `json:"version,omitempty"`
	Source      string   `json:"source"`
	Parameters  []Param  `json:"parameters"`
	FormFile    string   `json:"form_file,omitempty"`
	Files       []string `json:"files"`
	// Hook names the layout's Python hook, which only kaos-lib runs; kaos-lake checks the
	// declarative rules only.
	Hook string `json:"hook,omitempty"`
	Spec Spec   `json:"spec"`
}

func newLayout(s Spec, source string) Layout {
	files, _ := RenderSpec(s, "dataset-x", "", sampleValues(s))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	params := s.Params()
	if params == nil {
		params = []Param{}
	}
	return Layout{Name: s.Name(), Title: s.str("title"), Description: s.str("description"), Version: s.str("version"),
		Source: source, Parameters: params, FormFile: s.str("form_file"), Files: paths, Hook: s.str("hook"), Spec: s}
}

// sampleValues fills every parameter so the skeleton's file list can be shown.
func sampleValues(s Spec) map[string]any {
	v := map[string]any{}
	for _, p := range s.Params() {
		if p.Type == "list" {
			v[p.Key] = []string{"x"}
		} else {
			v[p.Key] = "x"
		}
	}
	return v
}

var (
	builtinOnce sync.Once
	builtins    []Layout
	builtinErr  error
)

// Builtins returns the built-in layouts, in name order.
func Builtins() ([]Layout, error) {
	builtinOnce.Do(func() {
		entries, err := shared.ReadDir("shared/builtin")
		if err != nil {
			builtinErr = err
			return
		}
		for _, e := range entries {
			data, err := shared.ReadFile(path.Join("shared/builtin", e.Name(), "layout.yaml"))
			if err != nil {
				builtinErr = err
				return
			}
			s, err := ParseSpec(data, "built-in layout "+e.Name())
			if err != nil {
				builtinErr = err
				return
			}
			builtins = append(builtins, newLayout(s, SourceBuiltin))
		}
	})
	return builtins, builtinErr
}

var (
	schemaOnce sync.Once
	schemaVal  map[string]any
)

func layoutSchema() map[string]any {
	schemaOnce.Do(func() {
		data, err := fs.ReadFile(shared, "shared/layout.schema.json")
		if err != nil {
			panic(err)
		}
		v, err := LoadYAML(data)
		if err != nil {
			panic(err)
		}
		schemaVal = v.(map[string]any)
	})
	return schemaVal
}

// Problem is a project layout that could not be served, and why.
type Problem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// ProjectStore reads the project's own layouts; nil means none.
type ProjectStore interface {
	// Layouts returns the layout.yaml of each project layout by its folder name.
	Layouts(ctx context.Context) (map[string][]byte, error)
}

// All returns the built-in layouts, then the project's (in name order), and the project layouts
// that were left out with why. A project layout may not take a built-in's name.
func All(ctx context.Context, store ProjectStore) ([]Layout, []Problem, error) {
	all, err := Builtins()
	if err != nil {
		return nil, nil, err
	}
	all = slices.Clone(all)
	if store == nil {
		return all, nil, nil
	}
	raw, err := store.Layouts(ctx)
	if err != nil {
		return nil, nil, err
	}
	var problems []Problem
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, folder := range names {
		where := path.Join(LayoutsRepository, folder, "layout.yaml")
		s, err := ParseSpec(raw[folder], where)
		switch {
		case err != nil:
			problems = append(problems, Problem{where, err.Error()})
		case s.Name() != folder:
			problems = append(problems, Problem{where, fmt.Sprintf("its name %s is not its folder's name %s", s.Name(), folder)})
		case slices.ContainsFunc(all, func(l Layout) bool { return l.Name == folder }) || legacy[folder] != "":
			problems = append(problems, Problem{where, fmt.Sprintf("%s is a built-in layout's name", folder)})
		default:
			all = append(all, newLayout(s, SourceProject))
		}
	}
	return all, problems, nil
}

// Get returns the layout called name (an old id resolves to its layout's current name).
func Get(ctx context.Context, store ProjectStore, name string) (Layout, error) {
	name = Canonical(name)
	if strings.ContainsAny(name, "/.") {
		return Layout{}, invalid("no layout %q", name)
	}
	all, _, err := All(ctx, store)
	if err != nil {
		return Layout{}, err
	}
	for _, l := range all {
		if l.Name == name {
			return l, nil
		}
	}
	return Layout{}, invalid("no layout %q", name)
}

// Render returns the files of a new dataset called name, from layout l and the form values.
func Render(l Layout, name, description string, values map[string]any) ([]File, error) {
	if !repos.ValidName(name, repos.DatasetPrefix) {
		return nil, invalid("a dataset's name is %s followed by 3-48 lowercase letters, digits or hyphens; %q is not",
			repos.DatasetPrefix, name)
	}
	return RenderSpec(l.Spec, name, description, values)
}
