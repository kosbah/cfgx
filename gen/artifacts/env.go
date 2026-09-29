package artifacts

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/example/cfgx/internal/ir"
)

// ruleWidth is the width, in runes, of section rules in .env and YAML output.
const ruleWidth = 72

// EnvExample generates a .env.example file from schema.
//
// Entries are grouped into sections by tag (first-appearance order, "Other"
// last, deprecated entries in a trailing DEPRECATED section) and sorted by key
// within a section. Computed entries are omitted; entries without an env var
// get a note instead of an assignment; sensitive values are always empty.
func EnvExample(schema *ir.SchemaIR) ([]byte, error) {
	if err := checkSchema("EnvExample", schema); err != nil {
		return nil, err
	}
	var leaves []leaf
	for _, l := range flatten(schema.Entries) {
		if l.Entry.Type != ir.TypeComputed {
			leaves = append(leaves, l)
		}
	}
	secs, deprecated := sections(leaves, true)
	if len(deprecated) > 0 {
		secs = append(secs, section{Title: "DEPRECATED", Leaves: deprecated})
	}

	var b bytes.Buffer
	for i, sec := range secs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(rule("# ", sec.Title, ruleWidth))
		b.WriteByte('\n')
		for j, l := range sec.Leaves {
			if j > 0 {
				b.WriteByte('\n')
			}
			writeEnvEntry(&b, l)
		}
	}
	return b.Bytes(), nil
}

func writeEnvEntry(b *bytes.Buffer, l leaf) {
	e := l.Entry
	if d := oneLine(e.Description); d != "" {
		b.WriteString("# " + d + "\n")
	}
	if l.Deprecated != "" {
		b.WriteString("## DEPRECATED: " + oneLine(l.Deprecated) + "\n")
	}
	if e.Env == "" {
		b.WriteString("# " + l.Key + ": no env var; use config file\n")
		return
	}
	switch {
	case e.Sensitive && e.Required:
		b.WriteString("# REQUIRED — sensitive, do not commit\n")
	case e.Sensitive:
		b.WriteString("# sensitive, do not commit\n")
	case e.Required:
		b.WriteString("# REQUIRED\n")
	}
	if l.Deprecated != "" {
		b.WriteString("# ")
	}
	b.WriteString(e.Env + "=" + envValue(e) + "\n")
}

// envValue renders the default of e as a .env value; lists are space-separated
// (as viper splits them). Values that a shell or dotenv parser could misread
// are double-quoted.
func envValue(e *ir.EntryIR) string {
	if !hasDefault(e) {
		return ""
	}
	var s string
	if e.Type == ir.TypeList {
		items := listItems(e.Default)
		parts := make([]string, len(items))
		for i, it := range items {
			parts[i] = scalarText(it)
		}
		s = strings.Join(parts, " ")
	} else {
		s = scalarText(e.Default)
	}
	if strings.ContainsAny(s, " \t\r\n#\"'`$\\=") {
		return strconv.Quote(s)
	}
	return s
}
