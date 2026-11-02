// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.

package templates

// The declarative rules of a layout, checked over a dataset tree, and the path patterns they use.
// Shared with kaos-lib (src/kaos_lib/layouts/rules.py): both give the same problems, in the same
// order and words, on the fixtures. kaos-lake checks only these rules: it never runs a layout's
// hook (Python), which kaos-lib runs on the person's own machine or step.

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const maxLineShown = 80

// ErrNotRead is what a Check reader answers for a file it chose not to read (a budget): the file's
// line and manifest checks are skipped, nothing else.
var ErrNotRead = errors.New("file not read")

// GlobRegexp compiles a path pattern: * and ? within one folder level, ** (a whole level) any
// number of levels.
func GlobRegexp(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "/")
	var b strings.Builder
	b.WriteString(`^(?s:`)
	for i, seg := range parts {
		last := i == len(parts)-1
		if seg == "**" {
			if last {
				b.WriteString(`.*`)
			} else {
				b.WriteString(`(?:[^/]*/)*`)
			}
			continue
		}
		for _, r := range seg {
			switch r {
			case '*':
				b.WriteString(`[^/]*`)
			case '?':
				b.WriteString(`[^/]`)
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		if !last {
			b.WriteString("/")
		}
	}
	b.WriteString(`)$`)
	return regexp.MustCompile(b.String())
}

// FixedPrefix returns the literal folders a pattern starts with ("images/" of "images/**").
func FixedPrefix(pattern string) string {
	parts := strings.Split(pattern, "/")
	var b strings.Builder
	for _, seg := range parts[:len(parts)-1] {
		if strings.ContainsAny(seg, "*?") {
			break
		}
		b.WriteString(seg + "/")
	}
	return b.String()
}

// Extension returns ".jpg" of "a/b.JPG" (lower case), or "".
func Extension(path string) string {
	seg := path[strings.LastIndex(path, "/")+1:]
	if i := strings.LastIndex(seg, "."); i > 0 {
		return strings.ToLower(seg[i:])
	}
	return ""
}

func stem(path, prefix string) string {
	rel := strings.TrimPrefix(path, prefix)
	if ext := Extension(rel); ext != "" {
		return rel[:len(rel)-len(ext)]
	}
	return rel
}

func visible(path string) bool {
	for _, s := range strings.Split(path, "/") {
		if strings.HasPrefix(s, ".") {
			return false
		}
	}
	return true
}

func lowers(v any) []string {
	var out []string
	for _, s := range strs(v) {
		out = append(out, strings.ToLower(s))
	}
	return out
}

// Check returns every problem spec's rules find in the tree paths; read gives a file's bytes. A
// read error other than ErrNotRead stops the check.
func Check(spec Spec, paths []string, read func(string) ([]byte, error)) ([]string, error) {
	var files []string
	present := map[string]bool{}
	for _, p := range paths {
		if visible(p) && !present[p] {
			present[p] = true
			files = append(files, p)
		}
	}
	slices.Sort(files)
	r := spec.obj("rules")
	var errs []string
	for _, g := range strs(r["required"]) {
		rx := GlobRegexp(g)
		if !slices.ContainsFunc(files, rx.MatchString) {
			errs = append(errs, g+": no file matches (required)")
		}
	}
	if allowed := strs(r["allowed"]); len(allowed) > 0 {
		var rxs []*regexp.Regexp
		for _, g := range allowed {
			rxs = append(rxs, GlobRegexp(g))
		}
		for _, p := range files {
			if !slices.ContainsFunc(rxs, func(rx *regexp.Regexp) bool { return rx.MatchString(p) }) {
				errs = append(errs, p+": not part of this layout")
			}
		}
	}
	for _, x := range list(r["extensions"]) {
		rx, ok := GlobRegexp(rule(x)["files"].(string)), lowers(rule(x)["allowed"])
		for _, p := range files {
			if ext := Extension(p); rx.MatchString(p) && !slices.Contains(ok, ext) {
				shown := ext
				if shown == "" {
					shown = "(none)"
				}
				errs = append(errs, fmt.Sprintf("%s: extension %s is not allowed here (%s)", p, shown, strings.Join(ok, ", ")))
			}
		}
	}
	for _, x := range list(r["pairs"]) {
		files0 := rule(x)["files"].(string)
		rx, prefix, exts := GlobRegexp(files0), FixedPrefix(files0), lowers(rule(x)["extensions"])
		needs, _ := rule(x)["needs"].(string)
		for _, p := range files {
			if rx.MatchString(p) && (len(exts) == 0 || slices.Contains(exts, Extension(p))) {
				if needed := strings.ReplaceAll(needs, "{stem}", stem(p, prefix)); !present[needed] {
					errs = append(errs, fmt.Sprintf("%s has no %s", p, needed))
				}
			}
		}
	}
	for _, x := range list(r["lines"]) {
		pattern, _ := rule(x)["pattern"].(string)
		rx, lineRx := GlobRegexp(rule(x)["files"].(string)), regexp.MustCompile(`^(?:`+pattern+`)$`)
		message, _ := rule(x)["message"].(string)
		if message == "" {
			message = "does not match " + pattern
		}
		for _, p := range files {
			if !rx.MatchString(p) {
				continue
			}
			data, err := read(p)
			if errors.Is(err, ErrNotRead) {
				continue
			}
			if err != nil {
				return nil, err
			}
			errs = append(errs, checkLines(p, data, lineRx, message)...)
		}
	}
	for _, x := range list(r["manifests"]) {
		file, _ := rule(x)["file"].(string)
		if !present[file] {
			continue
		}
		data, err := read(file)
		if errors.Is(err, ErrNotRead) {
			continue
		}
		if err != nil {
			return nil, err
		}
		schema, _ := rule(x)["schema"].(map[string]any)
		errs = append(errs, checkManifest(file, data, schema)...)
	}
	return errs, nil
}

func checkLines(path string, data []byte, rx *regexp.Regexp, message string) []string {
	if !utf8.Valid(data) {
		return []string{path + ": not UTF-8 text"}
	}
	var out []string
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.Trim(line, " \t\r")
		if line != "" && !rx.MatchString(line) {
			shown := line
			if utf8.RuneCountInString(shown) > maxLineShown {
				shown = string([]rune(shown)[:maxLineShown])
			}
			out = append(out, fmt.Sprintf("%s:%d: %s: %s", path, n+1, message, shown))
		}
	}
	return out
}

func checkManifest(path string, data []byte, schema map[string]any) []string {
	if !utf8.Valid(data) {
		return []string{path + ": not valid YAML"}
	}
	v, err := LoadYAML(data)
	if err != nil {
		return []string{path + ": not valid YAML"}
	}
	var out []string
	for _, e := range Validate(v, schema, "") {
		out = append(out, path+": "+e)
	}
	return out
}
