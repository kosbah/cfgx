package artifacts

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/epistemer/cfgx/internal/ir"
)

// otherSection is the title of the section holding untagged entries.
const otherSection = "Other"

// leaf is a non-Object entry flattened out of the schema tree.
type leaf struct {
	Key        string      // full dot key, e.g. "db.pool.max_open"
	Entry      *ir.EntryIR // the entry itself
	Tag        string      // own first tag, else the nearest ancestor's; "" when none
	Deprecated string      // own deprecation message, else the nearest ancestor's
}

// section is a titled group of leaves, sorted by key.
type section struct {
	Title  string
	Leaves []leaf
}

// checkSchema rejects inputs no generator can handle.
func checkSchema(gen string, schema *ir.SchemaIR) error {
	if schema == nil {
		return errors.New("artifacts: " + gen + ": nil schema")
	}
	if schema.Version != ir.Version {
		return fmt.Errorf("artifacts: %s: unsupported SchemaIR version %q (want %q)", gen, schema.Version, ir.Version)
	}
	return nil
}

// flatten returns every non-Object entry of entries in depth-first declaration
// order, with dot keys and inherited tags and deprecation.
func flatten(entries []ir.EntryIR) []leaf {
	var out []leaf
	var walk func(entries []ir.EntryIR, prefix, tag, deprecated string)
	walk = func(entries []ir.EntryIR, prefix, tag, deprecated string) {
		for i := range entries {
			e := &entries[i]
			key := joinKey(prefix, e.ID)
			t := tag
			if len(e.Tags) > 0 {
				t = e.Tags[0]
			}
			d := deprecated
			if e.Deprecated != "" {
				d = e.Deprecated
			}
			if e.Type == ir.TypeObject {
				walk(e.Properties, key, t, d)
				continue
			}
			out = append(out, leaf{Key: key, Entry: e, Tag: t, Deprecated: d})
		}
	}
	walk(entries, "", "", "")
	return out
}

// sections groups leaves by tag: tags in order of first appearance, then
// "Other" for untagged leaves. Leaves are sorted by key within a section.
// When splitDeprecated is set, deprecated leaves are removed from their tag
// sections and returned separately, also sorted by key.
func sections(leaves []leaf, splitDeprecated bool) (secs []section, deprecated []leaf) {
	index := map[string]int{}
	var other []leaf
	for _, l := range leaves {
		switch {
		case splitDeprecated && l.Deprecated != "":
			deprecated = append(deprecated, l)
		case l.Tag == "":
			other = append(other, l)
		default:
			i, ok := index[l.Tag]
			if !ok {
				i = len(secs)
				index[l.Tag] = i
				secs = append(secs, section{Title: tagTitle(l.Tag)})
			}
			secs[i].Leaves = append(secs[i].Leaves, l)
		}
	}
	if len(other) > 0 {
		secs = append(secs, section{Title: otherSection, Leaves: other})
	}
	for i := range secs {
		sortLeaves(secs[i].Leaves)
	}
	sortLeaves(deprecated)
	return secs, deprecated
}

func sortLeaves(ls []leaf) {
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].Key < ls[j].Key })
}

// joinKey joins a parent dot key and a child ID.
func joinKey(prefix, id string) string {
	if prefix == "" {
		return id
	}
	return prefix + "." + id
}

// tagTitle turns a tag into a section title: "-" and "_" become spaces and
// each word is capitalised ("database" → "Database", "http_api" → "Http Api").
func tagTitle(tag string) string {
	words := strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' || unicode.IsSpace(r) })
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	if len(words) == 0 {
		return otherSection
	}
	return strings.Join(words, " ")
}

// rule renders a section rule such as "# ── Server ─────…" padded to width runes.
func rule(prefix, title string, width int) string {
	s := prefix + "── " + title + " "
	n := width - utf8.RuneCountInString(s)
	if n < 2 {
		n = 2
	}
	return s + strings.Repeat("─", n)
}

// oneLine collapses all whitespace runs (including newlines) to single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// typeName returns a display type: "list<string>" for lists.
func typeName(e *ir.EntryIR) string {
	if e.Type == ir.TypeList {
		elem := "any"
		if e.ElementType != nil {
			elem = string(e.ElementType.Type)
		}
		return "list<" + elem + ">"
	}
	return string(e.Type)
}

// scalarText renders a scalar default as plain text: strings verbatim,
// numbers in shortest form, booleans as true/false.
func scalarText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return formatFloat(x)
	case float32:
		return formatFloat(float64(x))
	}
	return fmt.Sprint(v)
}

// formatFloat renders f in its shortest round-trip form.
func formatFloat(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Sprint(f)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// listItems returns the elements of a list default, or nil.
func listItems(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	}
	return nil
}

// hasDefault reports whether e carries a default that may be shown: never for
// sensitive or computed entries.
func hasDefault(e *ir.EntryIR) bool {
	return e.Default != nil && !e.Sensitive && e.Type != ir.TypeComputed
}
