// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.

package templates

// Reading a layout spec (layout.yaml) and the small schema language it and its manifest rules use.
//
// Shared contract with kaos-lib (src/kaos_lib/layouts/spec.py): shared/layout.schema.json and the
// fixtures in testdata/layout_fixtures are the same bytes in both repositories, and both
// implementations give the same answers on them. Keep the two aligned when either changes.
//
// YAML is turned into plain values the same way in both: mappings become maps keyed by the key's
// text, sequences lists, scalars nil/bool/int64/float64/string by their resolved tag (timestamps and
// binary stay text).

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxSpecBytes = 256 * 1024

var (
	reservedNames = []string{"none"}                // `kaosctl dataset create --layout none` makes an empty repository
	reservedKeys  = []string{"name", "description"} // always available to skeleton files

	schemaKeywords = []string{"type", "enum", "required", "properties", "additionalProperties", "items", "minItems",
		"maxItems", "minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern"}
	annotations = []string{"$schema", "$id", "$comment", "title", "description", "default", "examples"}
	typeNames   = []string{"array", "boolean", "integer", "null", "number", "object", "string"}

	// RE2 (Go) and Python re differ here: refuse what only one of them knows.
	notPortable = regexp.MustCompile(`\(\?[=!<>]|\(\?P=|\\[1-9]`)
)

var errNotYAML = errors.New("not valid YAML")

// LoadYAML returns the plain values of a YAML document (nil for an empty one).
func LoadYAML(text []byte) (any, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(text, &n); err != nil {
		return nil, errNotYAML
	}
	return plain(&n)
}

func plain(n *yaml.Node) (any, error) {
	switch n.Kind {
	case 0:
		return nil, nil
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return plain(n.Content[0])
	case yaml.AliasNode:
		return plain(n.Alias)
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() == "!!merge" {
				return nil, errNotYAML
			}
			v, err := plain(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := []any{}
		for _, c := range n.Content {
			v, err := plain(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return scalar(n)
	}
}

func scalar(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, errNotYAML
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			return nil, errNotYAML
		}
		return i, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, errNotYAML
		}
		return f, nil
	default:
		return n.Value, nil
	}
}

// ------------------------------------------------------------------ the schema subset

func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int64:
		return "integer"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return "object"
	}
}

func hasType(v any, t string) bool {
	actual := typeOf(v)
	return actual == t || (t == "number" && actual == "integer")
}

func number(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return math.NaN()
}

func fmtValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func fmtNumber(v any) string {
	if f, ok := v.(float64); ok && f == math.Trunc(f) && !math.IsInf(f, 0) {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmtValue(v)
}

func loc(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

func types(schema map[string]any) []string {
	switch t := schema["type"].(type) {
	case string:
		return []string{t}
	case []any:
		out := []string{}
		for _, x := range t {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CheckSchema returns the problems of a schema itself: only the supported keywords, sensible values.
func CheckSchema(schema any, where string) []string {
	s, ok := schema.(map[string]any)
	if !ok {
		return []string{where + ": must be an object"}
	}
	var errs []string
	for _, k := range sortedKeys(s) {
		if !slices.Contains(schemaKeywords, k) && !slices.Contains(annotations, k) {
			errs = append(errs, fmt.Sprintf("%s: keyword %s is not supported", where, k))
		}
	}
	if t, ok := s["type"]; ok && t != nil {
		ts := types(s)
		_, isString := t.(string)
		_, isList := t.([]any)
		bad := len(ts) == 0 || (!isString && !isList)
		for _, x := range ts {
			bad = bad || !slices.Contains(typeNames, x)
		}
		if isList {
			for _, x := range t.([]any) {
				if _, ok := x.(string); !ok {
					bad = true
				}
			}
		}
		if bad {
			errs = append(errs, fmt.Sprintf("%s: type must name %s", where, strings.Join(typeNames, ", ")))
		}
	}
	if p, ok := s["pattern"]; ok {
		errs = append(errs, CheckPattern(p, where+".pattern")...)
	}
	if p, ok := s["properties"]; ok {
		props, isMap := p.(map[string]any)
		if !isMap {
			errs = append(errs, where+".properties: must be an object")
		} else {
			for _, name := range sortedKeys(props) {
				errs = append(errs, CheckSchema(props[name], where+".properties."+name)...)
			}
		}
	}
	if items, ok := s["items"]; ok {
		errs = append(errs, CheckSchema(items, where+".items")...)
	}
	if extra, ok := s["additionalProperties"]; ok {
		if _, isBool := extra.(bool); !isBool {
			errs = append(errs, CheckSchema(extra, where+".additionalProperties")...)
		}
	}
	if req, ok := s["required"]; ok {
		list, isList := req.([]any)
		bad := !isList
		for _, x := range list {
			if _, ok := x.(string); !ok {
				bad = true
			}
		}
		if bad {
			errs = append(errs, where+".required: must be a list of keys")
		}
	}
	if e, ok := s["enum"]; ok {
		if _, isList := e.([]any); !isList {
			errs = append(errs, where+".enum: must be a list")
		}
	}
	for _, k := range []string{"minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength"} {
		if v, ok := s[k]; ok && typeOf(v) != "integer" {
			errs = append(errs, fmt.Sprintf("%s.%s: must be an integer", where, k))
		}
	}
	for _, k := range []string{"minimum", "maximum"} {
		if v, ok := s[k]; ok && typeOf(v) != "integer" && typeOf(v) != "number" {
			errs = append(errs, fmt.Sprintf("%s.%s: must be a number", where, k))
		}
	}
	return errs
}

func intOf(v any) int {
	i, _ := v.(int64)
	return int(i)
}

// Validate returns the problems of value against schema (a checked schema), in a fixed order.
func Validate(value any, schema map[string]any, path string) []string {
	if ts := types(schema); schema["type"] != nil {
		ok := false
		for _, t := range ts {
			ok = ok || hasType(value, t)
		}
		if !ok {
			return []string{fmt.Sprintf("%s: must be %s", loc(path), strings.Join(ts, " or "))}
		}
	}
	if e, ok := schema["enum"].([]any); ok {
		found := false
		for _, x := range e {
			found = found || (typeOf(x) == typeOf(value) && equalScalar(x, value))
		}
		if !found {
			shown := make([]string, len(e))
			for i, x := range e {
				shown[i] = fmtValue(x)
			}
			return []string{fmt.Sprintf("%s: must be one of %s", loc(path), strings.Join(shown, ", "))}
		}
	}
	var errs []string
	switch v := value.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, k := range req {
				key, _ := k.(string)
				if _, ok := v[key]; !ok {
					errs = append(errs, fmt.Sprintf("%s: %s is required", loc(path), key))
				}
			}
		}
		if n, ok := schema["minProperties"]; ok && len(v) < intOf(n) {
			errs = append(errs, fmt.Sprintf("%s: needs at least %d keys", loc(path), intOf(n)))
		}
		if n, ok := schema["maxProperties"]; ok && len(v) > intOf(n) {
			errs = append(errs, fmt.Sprintf("%s: allows at most %d keys", loc(path), intOf(n)))
		}
		props, _ := schema["properties"].(map[string]any)
		extra, hasExtra := schema["additionalProperties"]
		for _, key := range sortedKeys(v) {
			sub := key
			if path != "" {
				sub = path + "." + key
			}
			if p, ok := props[key]; ok {
				ps, _ := p.(map[string]any)
				errs = append(errs, Validate(v[key], ps, sub)...)
			} else if hasExtra && extra == false {
				errs = append(errs, sub+": is not allowed")
			} else if es, ok := extra.(map[string]any); ok {
				errs = append(errs, Validate(v[key], es, sub)...)
			}
		}
	case []any:
		if n, ok := schema["minItems"]; ok && len(v) < intOf(n) {
			errs = append(errs, fmt.Sprintf("%s: needs at least %d items", loc(path), intOf(n)))
		}
		if n, ok := schema["maxItems"]; ok && len(v) > intOf(n) {
			errs = append(errs, fmt.Sprintf("%s: allows at most %d items", loc(path), intOf(n)))
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, x := range v {
				errs = append(errs, Validate(x, items, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case int64, float64:
		if m, ok := schema["minimum"]; ok && number(v) < number(m) {
			errs = append(errs, fmt.Sprintf("%s: must be at least %s", loc(path), fmtNumber(m)))
		}
		if m, ok := schema["maximum"]; ok && number(v) > number(m) {
			errs = append(errs, fmt.Sprintf("%s: must be at most %s", loc(path), fmtNumber(m)))
		}
	case string:
		n := utf8.RuneCountInString(v)
		if m, ok := schema["minLength"]; ok && n < intOf(m) {
			errs = append(errs, fmt.Sprintf("%s: needs at least %d characters", loc(path), intOf(m)))
		}
		if m, ok := schema["maxLength"]; ok && n > intOf(m) {
			errs = append(errs, fmt.Sprintf("%s: allows at most %d characters", loc(path), intOf(m)))
		}
		if p, ok := schema["pattern"].(string); ok {
			if rx, err := regexp.Compile(p); err == nil && !rx.MatchString(v) {
				errs = append(errs, fmt.Sprintf("%s: does not match %s", loc(path), p))
			}
		}
	}
	return errs
}

func equalScalar(a, b any) bool {
	switch a.(type) {
	case nil, bool, int64, float64, string:
		return a == b
	}
	return false
}

// CheckPattern returns the problems of a regular expression both Go (RE2) and Python must read the same way.
func CheckPattern(pattern any, where string) []string {
	p, ok := pattern.(string)
	if !ok {
		return []string{where + ": must be text"}
	}
	if notPortable.MatchString(p) {
		return []string{where + ": lookaround and backreferences are not supported"}
	}
	if _, err := regexp.Compile(p); err != nil {
		return []string{where + ": does not compile"}
	}
	return nil
}

// ------------------------------------------------------------------ paths and patterns

// CheckPath returns the problems of a relative POSIX path inside the dataset; a pattern may hold *, ?
// and whole-segment **.
func CheckPath(p, where string, pattern bool) []string {
	what := "path"
	if pattern {
		what = "pattern"
	}
	segs := strings.Split(p, "/")
	if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || slices.ContainsFunc(segs, func(s string) bool {
		return s == "" || s == "." || s == ".."
	}) {
		return []string{fmt.Sprintf("%s: %s is not a relative %s inside the dataset", where, p, what)}
	}
	if !pattern && strings.ContainsAny(p, "*?") {
		return []string{fmt.Sprintf("%s: %s must not hold * or ?", where, p)}
	}
	if pattern && slices.ContainsFunc(segs, func(s string) bool { return strings.Contains(s, "**") && s != "**" }) {
		return []string{fmt.Sprintf("%s: %s: ** must be a whole folder level", where, p)}
	}
	return nil
}

// ------------------------------------------------------------------ the spec

// Spec is a checked layout spec, as plain values (what kaos-lib reads back from the API).
type Spec map[string]any

func (s Spec) str(key string) string {
	v, _ := s[key].(string)
	return v
}

// Name is the layout's id, the kaos.template label of datasets made with it.
func (s Spec) Name() string { return s.str("name") }

func (s Spec) obj(key string) map[string]any {
	v, _ := s[key].(map[string]any)
	return v
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// Param is one parameter of a layout.
type Param struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Help     string `json:"help,omitempty"`
	FormKey  string `json:"form_key,omitempty"`
}

// Params returns the layout's parameters.
func (s Spec) Params() []Param {
	var out []Param
	for _, x := range list(s["parameters"]) {
		m, _ := x.(map[string]any)
		get := func(k string) string { v, _ := m[k].(string); return v }
		req, _ := m["required"].(bool)
		out = append(out, Param{Key: get("key"), Label: get("label"), Type: get("type"), Required: req,
			Help: get("help"), FormKey: get("form_key")})
	}
	return out
}

func rule(r any) map[string]any {
	m, _ := r.(map[string]any)
	return m
}

func semantic(s Spec) []string {
	var errs []string
	if slices.Contains(reservedNames, s.Name()) {
		errs = append(errs, fmt.Sprintf("name: %s is reserved", s.Name()))
	}
	params := s.Params()
	var keys []string
	texts, lists := slices.Clone(reservedKeys), []string{}
	for i, p := range params {
		switch {
		case slices.Contains(reservedKeys, p.Key):
			errs = append(errs, fmt.Sprintf("parameters[%d].key: %s is reserved", i, p.Key))
		case slices.Contains(keys, p.Key):
			errs = append(errs, fmt.Sprintf("parameters[%d].key: %s is used twice", i, p.Key))
		}
		keys = append(keys, p.Key)
		if p.FormKey != "" && s.str("form_file") == "" {
			errs = append(errs, fmt.Sprintf("parameters[%d].form_key: the layout has no form_file", i))
		}
		if p.Type == "list" {
			lists = append(lists, p.Key)
		} else {
			texts = append(texts, p.Key)
		}
	}
	if _, ok := s["form_file"]; ok {
		errs = append(errs, CheckPath(s.str("form_file"), "form_file", false)...)
	}
	skeleton := s.obj("skeleton")
	for i, f := range strs(skeleton["folders"]) {
		errs = append(errs, CheckPath(f, fmt.Sprintf("skeleton.folders[%d]", i), false)...)
	}
	files, _ := skeleton["files"].(map[string]any)
	for _, p := range sortedKeys(files) {
		errs = append(errs, CheckPath(p, "skeleton.files."+p, false)...)
		text, _ := files[p].(string)
		if _, err := compileTemplate(text, texts, lists); err != nil {
			errs = append(errs, fmt.Sprintf("skeleton.files.%s: %s", p, err.Error()))
		}
	}
	r := s.obj("rules")
	for _, key := range []string{"required", "allowed"} {
		for i, g := range strs(r[key]) {
			errs = append(errs, CheckPath(g, fmt.Sprintf("rules.%s[%d]", key, i), true)...)
		}
	}
	for _, key := range []string{"extensions", "pairs", "lines"} {
		for i, x := range list(r[key]) {
			files, _ := rule(x)["files"].(string)
			errs = append(errs, CheckPath(files, fmt.Sprintf("rules.%s[%d].files", key, i), true)...)
		}
	}
	for i, x := range list(r["pairs"]) {
		needs, _ := rule(x)["needs"].(string)
		errs = append(errs, CheckPath(strings.ReplaceAll(needs, "{stem}", "x"), fmt.Sprintf("rules.pairs[%d].needs", i), false)...)
	}
	for i, x := range list(r["lines"]) {
		errs = append(errs, CheckPattern(rule(x)["pattern"], fmt.Sprintf("rules.lines[%d].pattern", i))...)
	}
	for i, x := range list(r["manifests"]) {
		file, _ := rule(x)["file"].(string)
		errs = append(errs, CheckPath(file, fmt.Sprintf("rules.manifests[%d].file", i), false)...)
		errs = append(errs, CheckSchema(rule(x)["schema"], fmt.Sprintf("rules.manifests[%d].schema", i))...)
	}
	for i, g := range strs(s.obj("materialize")["include"]) {
		errs = append(errs, CheckPath(g, fmt.Sprintf("materialize.include[%d]", i), true)...)
	}
	return errs
}

// Problems returns every problem of a layout spec (plain values): the schema first, then the rules in code.
func Problems(spec any) []string {
	if errs := Validate(spec, layoutSchema(), ""); len(errs) > 0 {
		return errs
	}
	return semantic(Spec(spec.(map[string]any)))
}

// SpecError is a layout spec that is not valid: the origin and every problem.
type SpecError struct {
	Origin   string
	Problems []string
}

func (e *SpecError) Error() string {
	return fmt.Sprintf("%s is not a valid layout: %s", e.Origin, strings.Join(e.Problems, "; "))
}

// ParseSpec checks the spec in text (YAML or JSON); a *SpecError names origin and every problem.
func ParseSpec(text []byte, origin string) (Spec, error) {
	if len(text) > maxSpecBytes {
		return nil, &SpecError{origin, []string{fmt.Sprintf("larger than %d KiB", maxSpecBytes/1024)}}
	}
	v, err := LoadYAML(text)
	if err != nil {
		return nil, &SpecError{origin, []string{"not valid YAML"}}
	}
	return FromValue(v, origin)
}

// FromValue checks a spec that is already plain values.
func FromValue(v any, origin string) (Spec, error) {
	if errs := Problems(v); len(errs) > 0 {
		return nil, &SpecError{origin, errs}
	}
	return Spec(v.(map[string]any)), nil
}
