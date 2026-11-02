// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.

package templates

// Skeleton files: the placeholder language of skeleton.files and the parameter values it fills in.
// Shared with kaos-lib (src/kaos_lib/layouts/render.py); both render the fixtures identically.
//
//	{{name}}  {{description}}  {{key}}     a text value, as is
//	{{key|quote}}                          as a double-quoted YAML/JSON string
//	{{key|count}}                          the number of items of a list
//	{{#each key}} ... {{/each}}            once per item: {{@item}}, {{@item|quote}}, {{@index}} (0, 1, ...)
//	{{#if key}} ... {{/if}}                only when the text is not empty / the list has items
//
// A line holding nothing but a block tag disappears with its line break.

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	standalone = regexp.MustCompile(`(?m)^[ \t]*(\{\{[ \t]*[#/][^{}\n]*\}\})[ \t]*(?:\r?\n|\z)`)
	tagRx      = regexp.MustCompile(`\{\{([^{}\n]*)\}\}`)
	exprRx     = regexp.MustCompile(`^\s*(@?[A-Za-z_][A-Za-z0-9_]*)\s*(?:\|\s*([a-z]+)\s*)?$`)
	openRx     = regexp.MustCompile(`^\s*#(each|if)\s+(@?[A-Za-z_][A-Za-z0-9_]*)\s*$`)
	closeRx    = regexp.MustCompile(`^\s*/(each|if)\s*$`)
	idRx       = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	loopValues = []string{"@index", "@item"}
)

// ErrInvalid marks a refusal the caller can fix: bad values, an unknown layout, a bad form file.
var ErrInvalid = errors.New("invalid dataset layout values")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Quote returns s as a double-quoted string, valid YAML and JSON (the same bytes as kaos-lib's).
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type node struct {
	kind     string // text, expr, each, if
	text     string // text; the value's name otherwise
	filter   string
	children []*node
}

type frame struct {
	kind, ref string
	nodes     *[]*node
}

func parseTemplate(text string) ([]*node, error) {
	text = standalone.ReplaceAllString(text, "${1}")
	var root []*node
	stack := []frame{{"root", "", &root}}
	pos := 0
	for _, m := range tagRx.FindAllStringSubmatchIndex(text, -1) {
		top := stack[len(stack)-1]
		if m[0] > pos {
			*top.nodes = append(*top.nodes, &node{kind: "text", text: text[pos:m[0]]})
		}
		pos = m[1]
		inner := text[m[2]:m[3]]
		if o := openRx.FindStringSubmatch(inner); o != nil {
			n := &node{kind: o[1], text: o[2]}
			*top.nodes = append(*top.nodes, n)
			stack = append(stack, frame{o[1], o[2], &n.children})
		} else if c := closeRx.FindStringSubmatch(inner); c != nil {
			if len(stack) == 1 {
				return nil, fmt.Errorf("{{/%s}} closes nothing", c[1])
			}
			if top.kind != c[1] {
				return nil, fmt.Errorf("{{/%s}} closes {{#%s %s}}", c[1], top.kind, top.ref)
			}
			stack = stack[:len(stack)-1]
		} else if e := exprRx.FindStringSubmatch(inner); e != nil {
			*top.nodes = append(*top.nodes, &node{kind: "expr", text: e[1], filter: e[2]})
		} else {
			return nil, fmt.Errorf("{{%s}} is not a placeholder", inner)
		}
	}
	if pos < len(text) {
		top := stack[len(stack)-1]
		*top.nodes = append(*top.nodes, &node{kind: "text", text: text[pos:]})
	}
	if len(stack) > 1 {
		top := stack[len(stack)-1]
		return nil, fmt.Errorf("{{#%s %s}} is not closed", top.kind, top.ref)
	}
	return root, nil
}

func checkNodes(nodes []*node, texts, lists []string, inEach bool) error {
	for _, n := range nodes {
		if n.kind == "text" {
			continue
		}
		ref := n.text
		loop := slices.Contains(loopValues, ref)
		isList := slices.Contains(lists, ref)
		if loop && !inEach {
			return fmt.Errorf("%s is only known inside {{#each}}", ref)
		}
		if !loop && !slices.Contains(texts, ref) && !isList {
			return fmt.Errorf("unknown value %s", ref)
		}
		switch n.kind {
		case "expr":
			if n.filter != "" && n.filter != "quote" && n.filter != "count" {
				return fmt.Errorf("unknown filter %s", n.filter)
			}
			if isList && n.filter != "count" {
				return fmt.Errorf("%s is a list: use {{#each %s}} or {{%s|count}}", ref, ref, ref)
			}
			if !isList && n.filter == "count" {
				return fmt.Errorf("count needs a list; %s is not one", ref)
			}
		case "each":
			if !isList {
				return fmt.Errorf("#each needs a list; %s is not one", ref)
			}
			if err := checkNodes(n.children, texts, lists, true); err != nil {
				return err
			}
		default:
			if err := checkNodes(n.children, texts, lists, inEach); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileTemplate(text string, texts, lists []string) ([]*node, error) {
	nodes, err := parseTemplate(text)
	if err != nil {
		return nil, err
	}
	return nodes, checkNodes(nodes, texts, lists, false)
}

func eval(nodes []*node, values map[string]any, loop map[string]any, b *strings.Builder) {
	for _, n := range nodes {
		if n.kind == "text" {
			b.WriteString(n.text)
			continue
		}
		var v any
		if slices.Contains(loopValues, n.text) {
			v = loop[n.text]
		} else {
			v = values[n.text]
		}
		switch n.kind {
		case "expr":
			switch {
			case n.filter == "count":
				b.WriteString(strconv.Itoa(len(v.([]string))))
			case n.filter == "quote":
				b.WriteString(Quote(fmt.Sprint(v)))
			default:
				b.WriteString(fmt.Sprint(v))
			}
		case "each":
			for i, item := range v.([]string) {
				eval(n.children, values, map[string]any{"@index": i, "@item": item}, b)
			}
		default:
			if l, ok := v.([]string); (ok && len(l) > 0) || (!ok && fmt.Sprint(v) != "") {
				eval(n.children, values, loop, b)
			}
		}
	}
}

// CleanValues returns the checked, trimmed values of params from raw (unknown keys are ignored).
func CleanValues(params []Param, raw map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range params {
		v := raw[p.Key]
		if p.Type == "list" {
			var items []string
			switch x := v.(type) {
			case nil:
			case []string:
				items = x
			case []any:
				for _, it := range x {
					s, ok := it.(string)
					if !ok {
						return nil, invalid("%s must be a list of text", p.Label)
					}
					items = append(items, s)
				}
			default:
				return nil, invalid("%s must be a list of text", p.Label)
			}
			clean := []string{}
			for _, s := range items {
				s = strings.TrimSpace(s)
				switch {
				case s == "":
					continue
				case strings.ContainsAny(s, "\n\r"):
					return nil, invalid("%s: %s spans lines", p.Label, s)
				case slices.Contains(clean, s):
					return nil, invalid("%s: %s is listed twice", p.Label, s)
				}
				clean = append(clean, s)
			}
			if p.Required && len(clean) == 0 {
				return nil, invalid("%s needs at least one item", p.Label)
			}
			out[p.Key] = clean
			continue
		}
		s := ""
		if v != nil {
			var ok bool
			if s, ok = v.(string); !ok {
				return nil, invalid("%s must be text", p.Label)
			}
		}
		s = strings.TrimSpace(s)
		if strings.ContainsAny(s, "\n\r") {
			return nil, invalid("%s must be one line", p.Label)
		}
		if p.Required && s == "" {
			return nil, invalid("%s is required", p.Label)
		}
		out[p.Key] = s
	}
	return out, nil
}

// File is one rendered file of a new dataset.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// RenderSpec returns the files of a new dataset name from spec's skeleton, sorted by path (empty
// folders as <folder>/.keep).
func RenderSpec(spec Spec, name, description string, values map[string]any) ([]File, error) {
	params := spec.Params()
	data, err := CleanValues(params, values)
	if err != nil {
		return nil, err
	}
	data["name"], data["description"] = strings.TrimSpace(name), strings.TrimSpace(description)
	texts, lists := slices.Clone(reservedKeys), []string{}
	for _, p := range params {
		if p.Type == "list" {
			lists = append(lists, p.Key)
		} else {
			texts = append(texts, p.Key)
		}
	}
	files := map[string]string{}
	skeleton := spec.obj("skeleton")
	for _, f := range strs(skeleton["folders"]) {
		files[strings.TrimRight(f, "/")+"/.keep"] = ""
	}
	tmpl, _ := skeleton["files"].(map[string]any)
	for p, t := range tmpl {
		text, _ := t.(string)
		nodes, err := compileTemplate(text, texts, lists)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		eval(nodes, data, nil, &b)
		files[p] = b.String()
	}
	paths := sortedKeys(files)
	sort.Strings(paths)
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		out = append(out, File{Path: p, Content: files[p]})
	}
	return out, nil
}

func nodeText(n *yaml.Node) string {
	if n.ShortTag() == "!!null" {
		return ""
	}
	return n.Value
}

// ParseForm returns the parameter values content (the layout's form_file) holds, for editing a
// dataset. A list parameter reads its form_key as a list, or as {0: a, 1: b, ...} (ids 0..n-1); a
// text parameter as a single value. Values are the YAML text as written (null is empty).
func ParseForm(spec Spec, content string) (map[string]any, error) {
	form := spec.str("form_file")
	if form == "" {
		return nil, invalid("layout %s has no form file", spec.Name())
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, invalid("%s: not valid YAML", form)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, invalid("%s: must be a mapping", form)
	}
	m := doc.Content[0]
	found := map[string]*yaml.Node{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode {
			found[m.Content[i].Value] = m.Content[i+1]
		}
	}
	out := map[string]any{}
	for _, p := range spec.Params() {
		v, ok := found[p.FormKey]
		if p.FormKey == "" || !ok {
			continue
		}
		key := p.FormKey
		if v.Kind == yaml.AliasNode {
			v = v.Alias
		}
		switch {
		case p.Type == "text":
			if v.Kind != yaml.ScalarNode {
				return nil, invalid("%s: %s must be a single value", form, key)
			}
			out[p.Key] = nodeText(v)
		case v.Kind == yaml.SequenceNode:
			items := []string{}
			for _, x := range v.Content {
				if x.Kind != yaml.ScalarNode {
					return nil, invalid("%s: %s must list single values", form, key)
				}
				items = append(items, nodeText(x))
			}
			out[p.Key] = items
		case v.Kind == yaml.MappingNode:
			byID := map[int]string{}
			for i := 0; i+1 < len(v.Content); i += 2 {
				k, x := v.Content[i], v.Content[i+1]
				if k.Kind != yaml.ScalarNode || !idRx.MatchString(k.Value) {
					return nil, invalid("%s: %s: ids must be 0, 1, 2, ...", form, key)
				}
				if x.Kind != yaml.ScalarNode {
					return nil, invalid("%s: %s must map ids to single values", form, key)
				}
				id, _ := strconv.Atoi(k.Value)
				if _, dup := byID[id]; dup {
					return nil, invalid("%s: %s: id %s is listed twice", form, key, k.Value)
				}
				byID[id] = nodeText(x)
			}
			items := make([]string, 0, len(byID))
			for i := range len(byID) {
				s, ok := byID[i]
				if !ok {
					return nil, invalid("%s: %s: ids must run 0..%d without gaps", form, key, len(byID)-1)
				}
				items = append(items, s)
			}
			out[p.Key] = items
		default:
			return nil, invalid("%s: %s must be a list or an id -> value mapping", form, key)
		}
	}
	return out, nil
}
