package artifacts

import (
	"bytes"
	"strings"

	"github.com/epistemer/cfgx/internal/ir"
)

// ManPage generates a groff man page fragment (config.1) from schema.
//
// The fragment has no .TH line; it starts with a comment carrying appName,
// version and date, followed by ".SH CONFIGURATION" and one ".TP" item per
// entry that has an env var, grouped into ".SS" subsections by tag
// (first-appearance order, "Other", then "Deprecated"). Computed entries are
// omitted and sensitive defaults are never shown.
func ManPage(schema *ir.SchemaIR, appName, version, date string) ([]byte, error) {
	if err := checkSchema("ManPage", schema); err != nil {
		return nil, err
	}
	var leaves []leaf
	for _, l := range flatten(schema.Entries) {
		if l.Entry.Env != "" && l.Entry.Type != ir.TypeComputed {
			leaves = append(leaves, l)
		}
	}
	secs, deprecated := sections(leaves, true)
	if len(deprecated) > 0 {
		secs = append(secs, section{Title: "Deprecated", Leaves: deprecated})
	}

	var b bytes.Buffer
	b.WriteString(`.\" ` + oneLine(strings.Join(nonEmpty(appName, version, date), " ")) + "\n")
	b.WriteString(".SH CONFIGURATION\n")
	for _, sec := range secs {
		b.WriteString(".SS " + groffLine(sec.Title) + "\n")
		for _, l := range sec.Leaves {
			writeManEntry(&b, l)
		}
	}
	return b.Bytes(), nil
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeManEntry(b *bytes.Buffer, l leaf) {
	e := l.Entry
	b.WriteString(".TP\n.B " + groffEscape(e.Env) + "\n")

	var parts []string
	if d := oneLine(e.Description); d != "" {
		parts = append(parts, groffEscape(sentence(d)))
	}
	if hasDefault(e) {
		parts = append(parts, "Default: "+groffEscape(manDefault(e))+".")
	}
	if hint := manHint(e); hint != "" {
		parts = append(parts, groffEscape(hint))
	}
	if e.IsLive {
		parts = append(parts, groffEscape("Hot-reloadable."))
	}
	switch {
	case e.Sensitive && e.Required:
		parts = append(parts, `\fBREQUIRED. SENSITIVE.\fR`)
	case e.Sensitive:
		parts = append(parts, `\fBSENSITIVE.\fR`)
	case e.Required:
		parts = append(parts, `\fBREQUIRED.\fR`)
	}
	if len(parts) > 0 {
		b.WriteString(groffLineStart(strings.Join(parts, " ")) + "\n")
	}
	if l.Deprecated != "" {
		msg := groffEscape("DEPRECATED: " + oneLine(l.Deprecated))
		b.WriteString(`.B "` + strings.ReplaceAll(msg, `"`, `\(dq`) + "\"\n")
	}
}

// manDefault renders a default as plain text; lists are comma-separated.
func manDefault(e *ir.EntryIR) string {
	if e.Type == ir.TypeList {
		items := listItems(e.Default)
		parts := make([]string, len(items))
		for i, it := range items {
			parts[i] = scalarText(it)
		}
		return strings.Join(parts, ", ")
	}
	if s := scalarText(e.Default); s != "" {
		return s
	}
	return `""`
}

// manHint summarises range and enum validators as a sentence.
func manHint(e *ir.EntryIR) string {
	var hints []string
	for _, v := range e.BuiltinValidators {
		switch v.Name {
		case "port":
			hints = append(hints, "Range: 1-65535.")
		case "port_range", "range":
			lo, okMin := v.Params["min"]
			hi, okMax := v.Params["max"]
			if okMin && okMax {
				hints = append(hints, "Range: "+scalarText(lo)+"-"+scalarText(hi)+".")
			}
		case "enum":
			vals := listItems(v.Params["values"])
			parts := make([]string, len(vals))
			for i, x := range vals {
				parts[i] = scalarText(x)
			}
			if len(parts) > 0 {
				hints = append(hints, "One of: "+strings.Join(parts, ", ")+".")
			}
		}
	}
	return strings.Join(hints, " ")
}

// sentence ensures s ends with terminal punctuation.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// groffEscape escapes text for groff: backslashes become \e and hyphens \-.
func groffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	return strings.ReplaceAll(s, "-", `\-`)
}

// groffLineStart protects a text line that would otherwise be read as a
// request or macro call (leading "." or "'").
func groffLineStart(s string) string {
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		return `\&` + s
	}
	return s
}

// groffLine escapes s and makes it safe as a macro argument line.
func groffLine(s string) string {
	return groffEscape(oneLine(s))
}
