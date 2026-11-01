// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05): new file, Apache-2.0.

// Package templates holds the dataset templates (PRD novelcore/kaos#1283 D-06) and renders them:
// form values in, the files of a new dataset out, or a dataset's form file back to form values for
// editing. It writes nothing: the UI and the CLI commit the files with the person's own identity.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

//go:embed defs
var defs embed.FS

// aliases keep old template names working: "yolo" was renamed "ultralytics" because the format is
// Ultralytics' (callers and datasets labelled kaos.template=yolo keep working).
var aliases = map[string]string{"yolo": "ultralytics"}

// Canonical returns the current name of the template called name (an alias resolves, any other
// name is returned as it is).
func Canonical(name string) string {
	if c, ok := aliases[name]; ok {
		return c
	}
	return name
}

var ErrInvalid = errors.New("invalid dataset template values")

// Field is one input of a template's form.
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // "list" (one item per line) or "text"
	Required bool   `json:"required,omitempty"`
	Help     string `json:"help,omitempty"`
}

// Template is one dataset template.
type Template struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Fields      []Field  `json:"fields"`
	FormFile    string   `json:"form_file,omitempty"` // the file Parse reads back into the form
	Files       []string `json:"files"`
}

// File is one rendered file of a new dataset.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// All returns the templates, in name order.
func All() ([]Template, error) {
	entries, err := defs.ReadDir("defs")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		t, err := Get(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Get returns the template called name, or the one an alias names.
func Get(name string) (Template, error) {
	var t Template
	name = Canonical(name)
	data, err := defs.ReadFile(path.Join("defs", name, "template.json"))
	if errors.Is(err, fs.ErrNotExist) || strings.ContainsAny(name, "/.") {
		return t, fmt.Errorf("%w: no template %q", ErrInvalid, name)
	}
	if err != nil {
		return t, err
	}
	return t, json.Unmarshal(data, &t)
}

// Render returns the files of a new dataset called name, from template tmpl and the form values.
func Render(tmpl, name, description string, values map[string]any) ([]File, error) {
	t, err := Get(tmpl)
	if err != nil {
		return nil, err
	}
	if !repos.ValidName(name, repos.DatasetPrefix) {
		return nil, fmt.Errorf("%w: a dataset's name is %s followed by 3-48 lowercase letters, digits or hyphens; %q is not",
			ErrInvalid, repos.DatasetPrefix, name)
	}
	data := map[string]any{"name": name, "description": strings.TrimSpace(description)}
	for _, f := range t.Fields {
		v, err := value(f, values[f.Key])
		if err != nil {
			return nil, err
		}
		data[f.Key] = v
	}
	funcs := template.FuncMap{"quote": strconv.Quote}
	var out []File
	for _, p := range t.Files {
		if path.Base(p) == ".keep" { // marks a folder
			out = append(out, File{Path: p})
			continue
		}
		src, err := defs.ReadFile(path.Join("defs", t.Name, p))
		if err != nil {
			return nil, err
		}
		tt, err := template.New(p).Funcs(funcs).Option("missingkey=error").Parse(string(src))
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := tt.Execute(&b, data); err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, Content: b.String()})
	}
	return out, nil
}

// value checks one form value: a list is trimmed, has no empty or repeated items, and is
// required when the field says so.
func value(f Field, raw any) (any, error) {
	switch f.Type {
	case "list":
		var items []string
		switch v := raw.(type) {
		case nil:
		case []any:
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return nil, fmt.Errorf("%w: %s must be a list of text", ErrInvalid, f.Label)
				}
				items = append(items, s)
			}
		case []string:
			items = v
		default:
			return nil, fmt.Errorf("%w: %s must be a list", ErrInvalid, f.Label)
		}
		var clean []string
		for _, s := range items {
			s = strings.TrimSpace(s)
			switch {
			case s == "":
				continue
			case strings.ContainsAny(s, "\n\r"):
				return nil, fmt.Errorf("%w: %s: %q spans lines", ErrInvalid, f.Label, s)
			case slices.Contains(clean, s):
				return nil, fmt.Errorf("%w: %s: %q is listed twice", ErrInvalid, f.Label, s)
			}
			clean = append(clean, s)
		}
		if f.Required && len(clean) == 0 {
			return nil, fmt.Errorf("%w: %s needs at least one item", ErrInvalid, f.Label)
		}
		return clean, nil
	default:
		s, _ := raw.(string)
		s = strings.TrimSpace(s)
		if f.Required && s == "" {
			return nil, fmt.Errorf("%w: %s is required", ErrInvalid, f.Label)
		}
		return s, nil
	}
}

// Parse reads a dataset's form file back into form values, for editing. Only the Ultralytics template
// has a form file today: its classes are data.yaml's names, as a list or as {id: name}.
func Parse(tmpl, content string) (map[string]any, error) {
	t, err := Get(tmpl)
	if err != nil {
		return nil, err
	}
	if t.FormFile == "" {
		return nil, fmt.Errorf("%w: template %q has no form file", ErrInvalid, tmpl)
	}
	var doc struct {
		NC    *int      `yaml:"nc"`
		Names yaml.Node `yaml:"names"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, t.FormFile, err)
	}
	var classes []string
	switch doc.Names.Kind {
	case yaml.SequenceNode:
		if err := doc.Names.Decode(&classes); err != nil {
			return nil, fmt.Errorf("%w: names: %w", ErrInvalid, err)
		}
	case yaml.MappingNode:
		var byID map[int]string
		if err := doc.Names.Decode(&byID); err != nil {
			return nil, fmt.Errorf("%w: names: %w", ErrInvalid, err)
		}
		for i := 0; i < len(byID); i++ {
			name, ok := byID[i]
			if !ok {
				return nil, fmt.Errorf("%w: names: ids must run 0..%d without gaps", ErrInvalid, len(byID)-1)
			}
			classes = append(classes, name)
		}
	default:
		return nil, fmt.Errorf("%w: %s has no names", ErrInvalid, t.FormFile)
	}
	if doc.NC != nil && *doc.NC != len(classes) {
		return nil, fmt.Errorf("%w: nc is %d but there are %d names", ErrInvalid, *doc.NC, len(classes))
	}
	return map[string]any{"classes": classes}, nil
}
