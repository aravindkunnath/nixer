// Package nixedit edits `attr = [ ... ];` lists in Nix source text in place,
// preserving the surrounding formatting and comments.
//
// It is not a Nix parser. It masks out comments and strings, finds the list by
// attribute name (written dotted, or nested inside its parent attribute set),
// and splits the list into items. Anything it cannot read confidently, such as
// `++`, function calls or parenthesised entries, is refused with
// ErrUnsupported rather than guessed at.
package nixedit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNoList is matched (errors.Is) by the error returned when the attribute's
// list is not in the text at all, as opposed to being present but unreadable.
var ErrNoList = errors.New("list not found")

// ErrUnsupported is matched by errors for lists nixedit refuses to edit
// because their syntax is beyond what it understands.
var ErrUnsupported = errors.New("unsupported syntax")

type noListError struct{ attr string }

func (e noListError) Error() string        { return fmt.Sprintf("could not find `%s = [ ... ]`", e.attr) }
func (e noListError) Is(target error) bool { return target == ErrNoList }

type unsupportedError struct{ msg string }

func (e unsupportedError) Error() string        { return e.msg }
func (e unsupportedError) Is(target error) bool { return target == ErrUnsupported }

// ---- lexical masking -------------------------------------------------------

// skipSpan returns the end of the comment or string that starts at i, or i if
// none starts there.
func skipSpan(text string, i int) int {
	switch {
	case text[i] == '#':
		if nl := strings.IndexByte(text[i:], '\n'); nl >= 0 {
			return i + nl
		}
		return len(text)
	case strings.HasPrefix(text[i:], "/*"):
		if e := strings.Index(text[i+2:], "*/"); e >= 0 {
			return i + 2 + e + 2
		}
		return len(text)
	case text[i] == '"':
		for j := i + 1; j < len(text); {
			switch {
			case text[j] == '\\':
				j += 2
			case strings.HasPrefix(text[j:], "${"):
				j = skipInterp(text, j)
			case text[j] == '"':
				return j + 1
			default:
				j++
			}
		}
		return len(text)
	case strings.HasPrefix(text[i:], "''"):
		for j := i + 2; j < len(text); {
			switch {
			case strings.HasPrefix(text[j:], "'''"), strings.HasPrefix(text[j:], "''$"), strings.HasPrefix(text[j:], "''\\"):
				j += 3
			case strings.HasPrefix(text[j:], "''"):
				return j + 2
			case strings.HasPrefix(text[j:], "${"):
				j = skipInterp(text, j)
			default:
				j++
			}
		}
		return len(text)
	}
	return i
}

// skipInterp returns the end of the `${ ... }` starting at i.
func skipInterp(text string, i int) int {
	depth := 1
	for j := i + 2; j < len(text); {
		if e := skipSpan(text, j); e > j {
			j = e
			continue
		}
		switch text[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
		j++
	}
	return len(text)
}

// mask returns text with comments blanked and the insides of strings blanked
// (their quotes stay), keeping every byte offset and newline.
func mask(text string) string {
	b := []byte(text)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	for i := 0; i < len(text); {
		e := skipSpan(text, i)
		if e == i {
			i++
			continue
		}
		switch {
		case text[i] == '"':
			if e-i >= 2 && text[e-1] == '"' {
				blank(i+1, e-1)
			} else {
				blank(i+1, e)
			}
		case strings.HasPrefix(text[i:], "''"):
			if e-i >= 4 && strings.HasSuffix(text[:e], "''") {
				blank(i+2, e-2)
			} else {
				blank(i+2, e)
			}
		default:
			blank(i, e)
		}
		i = e
	}
	return string(b)
}

// ---- finding and splitting a list -----------------------------------------

type item struct {
	name       string // text of the item, quotes removed
	start, end int    // byte range in the text
}

type list struct {
	attr        string
	open, close int  // offsets of `[` and its matching `]`
	hasWith     bool // `attr = with pkgs; [ ... ]`
	items       []item
}

var withRe = regexp.MustCompile(`^with\s+[\w.\-]+\s*;\s*`)

func attrRe(attr string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)(?:^|[;{])[ \t]*(` + regexp.QuoteMeta(attr) + `)\s*=\s*`)
}

func lineOf(text string, off int) int { return strings.Count(text[:off], "\n") + 1 }

func snippet(text string, off int) string {
	s := text[off:]
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40] + "…"
	}
	return s
}

func unsupported(attr, text string, off int, why string) error {
	return unsupportedError{fmt.Sprintf("`%s` (line %d): %s: `%s`; edit this one by hand",
		attr, lineOf(text, off), why, snippet(text, off))}
}

// depthBetween returns the net nesting of {, [ and ( between from and to.
func depthBetween(masked string, from, to int) int {
	d := 0
	for _, c := range []byte(masked[from:to]) {
		switch c {
		case '{', '[', '(':
			d++
		case '}', ']', ')':
			d--
		}
	}
	return d
}

// matching returns the offset of the bracket closing the one at open.
func matching(masked string, open int) int {
	o := masked[open]
	c := map[byte]byte{'[': ']', '{': '}', '(': ')'}[o]
	depth := 0
	for i := open; i < len(masked); i++ {
		switch masked[i] {
		case o:
			depth++
		case c:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// locate finds the assignment of attr in masked[lo:hi]. A dotted attr may be
// written either dotted (`a.b = [`) or nested (`a = { b = [`). At the top
// level the assignment may sit at any depth; inside a nested block it must be
// a direct child.
func locate(text, masked string, parts []string, lo, hi int, anyDepth bool) (*list, error) {
	full := strings.Join(parts, ".")
	re := attrRe(full)
	for _, m := range re.FindAllStringSubmatchIndex(masked[lo:hi], -1) {
		start, rhs := lo+m[2], lo+m[1]
		if !anyDepth && depthBetween(masked, lo, start) != 0 {
			continue
		}
		return readList(text, masked, full, rhs)
	}
	for k := 1; k < len(parts); k++ {
		pre := attrRe(strings.Join(parts[:k], "."))
		for _, m := range pre.FindAllStringSubmatchIndex(masked[lo:hi], -1) {
			start, rhs := lo+m[2], lo+m[1]
			if !anyDepth && depthBetween(masked, lo, start) != 0 {
				continue
			}
			if rhs >= hi || masked[rhs] != '{' {
				continue
			}
			end := matching(masked, rhs)
			if end < 0 || end > hi {
				continue
			}
			l, err := locate(text, masked, parts[k:], rhs+1, end, false)
			if errors.Is(err, ErrNoList) {
				continue
			}
			if l != nil {
				l.attr = full
			}
			return l, err
		}
	}
	return nil, noListError{full}
}

// readList reads the list value starting at rhs (just after `=`).
func readList(text, masked, attr string, rhs int) (*list, error) {
	l := &list{attr: attr}
	i := rhs
	if m := withRe.FindString(masked[i:]); m != "" {
		l.hasWith = true
		i += len(m)
	}
	if i >= len(masked) || masked[i] != '[' {
		return nil, unsupported(attr, text, rhs, "value is not a plain `[ ... ]` list")
	}
	l.open = i
	l.close = matching(masked, i)
	if l.close < 0 {
		return nil, fmt.Errorf("unbalanced brackets after `%s`", attr)
	}
	if rest := strings.TrimLeft(masked[l.close+1:], " \t\r\n"); strings.HasPrefix(rest, "++") {
		return nil, unsupported(attr, text, l.close, "list is joined with `++`")
	}
	items, err := splitItems(text, masked, attr, l.open+1, l.close)
	if err != nil {
		return nil, err
	}
	l.items = items
	return l, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '\''
}

func splitItems(text, masked, attr string, lo, hi int) ([]item, error) {
	var items []item
	for i := lo; i < hi; {
		c := masked[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"':
			j := strings.IndexByte(masked[i+1:hi], '"')
			if j < 0 {
				return nil, unsupported(attr, text, i, "unterminated string")
			}
			j += i + 1
			body := text[i+1 : j]
			if strings.Contains(body, "${") || strings.Contains(body, `\`) {
				return nil, unsupported(attr, text, i, "string with interpolation or escapes")
			}
			items = append(items, item{body, i, j + 1})
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < hi && isIdentChar(masked[j]) {
				j++
			}
			items = append(items, item{text[i:j], i, j})
			i = j
		default:
			return nil, unsupported(attr, text, i, "list entry is not a plain name")
		}
	}
	return items, nil
}

func find(text, attr string) (*list, string, error) {
	masked := mask(text)
	l, err := locate(text, masked, strings.Split(attr, "."), 0, len(masked), true)
	return l, masked, err
}

// ---- public API ------------------------------------------------------------

// Normalize strips the pkgs. / pkgs-unstable. prefix from an item, and any
// extra attribute-set names given (a config may call its unstable set
// something else).
func Normalize(name string, extra ...string) string {
	var prefixes []string
	for _, e := range extra {
		if e != "" {
			prefixes = append(prefixes, e+".")
		}
	}
	prefixes = append(prefixes, "pkgs-unstable.", "pkgs.")
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return name[len(p):]
		}
	}
	return name
}

// List returns the items of the list assigned to attr.
func List(text, attr string) ([]string, error) {
	l, _, err := find(text, attr)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, it := range l.items {
		out = append(out, it.name)
	}
	return out, nil
}

// Style returns the prefix new items for attr should be written with, so
// they match the list: "pkgs." when its items are written `pkgs.name` (the
// list has no `with pkgs;`), otherwise "". Items from the unstable set are
// ignored when counting.
func Style(text, attr, unstable string) (string, error) {
	l, _, err := find(text, attr)
	if err != nil {
		return "", err
	}
	if l.hasWith {
		return "", nil
	}
	prefixed, bare := 0, 0
	for _, it := range l.items {
		switch {
		case strings.HasPrefix(it.name, "pkgs."):
			prefixed++
		case strings.HasPrefix(it.name, "pkgs-unstable."), unstable != "" && strings.HasPrefix(it.name, unstable+"."):
		default:
			bare++
		}
	}
	if prefixed > 0 && prefixed >= bare {
		return "pkgs.", nil
	}
	return "", nil
}

func leadingWhitespace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func lineStart(text string, off int) int { return strings.LastIndexByte(text[:off], '\n') + 1 }

func lineEnd(text string, off int) int {
	if nl := strings.IndexByte(text[off:], '\n'); nl >= 0 {
		return off + nl
	}
	return len(text)
}

// alone reports whether the item is the only thing on its line (apart from a
// trailing `#` comment).
func alone(text string, it item) bool {
	before := text[lineStart(text, it.start):it.start]
	after := strings.TrimSpace(text[it.end:lineEnd(text, it.end)])
	return strings.TrimSpace(before) == "" && (after == "" || strings.HasPrefix(after, "#"))
}

// Add appends entry to the list assigned to attr, after its last item.
func Add(text, attr, entry string) (string, error) {
	l, _, err := find(text, attr)
	if err != nil {
		return "", err
	}
	oneLine := !strings.Contains(text[l.open:l.close], "\n")
	if len(l.items) == 0 {
		if oneLine {
			return text[:l.open+1] + " " + entry + " " + text[l.close:], nil
		}
		indent := leadingWhitespace(text[lineStart(text, l.open):]) + "  "
		eol := lineEnd(text, l.open)
		return text[:eol] + "\n" + indent + entry + text[eol:], nil
	}
	last := l.items[len(l.items)-1]
	switch {
	case oneLine:
		return text[:last.end] + " " + entry + text[last.end:], nil
	case alone(text, last):
		indent := leadingWhitespace(text[lineStart(text, last.start):])
		eol := lineEnd(text, last.end)
		return text[:eol] + "\n" + indent + entry + text[eol:], nil
	}
	first := l.items[0]
	if lineStart(text, first.start) == lineStart(text, l.open) {
		// `[ a` with the closing bracket on a later line: put items on their own lines.
		indent := leadingWhitespace(text[lineStart(text, l.open):]) + "  "
		expanded := text[:l.open+1] + "\n" + indent + text[first.start:]
		return Add(expanded, attr, entry)
	}
	return text[:last.end] + " " + entry + text[last.end:], nil
}

// Remove deletes the item called name (ignoring pkgs. prefixes, quotes and
// any extra attribute-set prefixes, see Normalize).
// It reports whether anything was removed.
func Remove(text, attr, name string, extra ...string) (string, bool, error) {
	l, _, err := find(text, attr)
	if err != nil {
		return "", false, err
	}
	removed := false
	for i := len(l.items) - 1; i >= 0; i-- {
		it := l.items[i]
		if Normalize(it.name, extra...) != name {
			continue
		}
		text = deleteItem(text, it)
		removed = true
	}
	return text, removed, nil
}

// deleteItem removes one item, taking its whole line when it is alone on it.
func deleteItem(text string, it item) string {
	ls, le := lineStart(text, it.start), lineEnd(text, it.end)
	if alone(text, it) {
		if le < len(text) {
			le++
		}
		return text[:ls] + text[le:]
	}
	end := it.end
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	if end >= len(text) || text[end] == '\n' || text[end] == ']' {
		// Last on its line or in the list: take the space before it instead.
		start := it.start
		for start > ls && (text[start-1] == ' ' || text[start-1] == '\t') {
			start--
		}
		return text[:start] + text[it.end:]
	}
	return text[:it.start] + text[end:]
}
