package artifacts

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/epistemer/cfgx/gen/parser"
	"github.com/epistemer/cfgx/internal/ir"
)

var update = flag.Bool("update", false, "update golden files")

const (
	schemasDir = "../../testdata/schemas"
	goldenDir  = "../../testdata/golden/artifacts"
)

// Fixed man page metadata for golden output.
const (
	manApp     = "cfgx"
	manVersion = "v1.0.0"
	manDate    = "2026-09-29"
)

var goldenSchemas = []string{"full", "minimal", "nested", "deprecated"}

// generators maps each golden file name to its generator.
var generators = []struct {
	file string
	gen  func(*ir.SchemaIR) ([]byte, error)
}{
	{"env.example", EnvExample},
	{"config.example.yaml", YAMLExample},
	{"config.schema.json", JSONSchema},
	{"config.md", Markdown},
	{"config.1", func(s *ir.SchemaIR) ([]byte, error) { return ManPage(s, manApp, manVersion, manDate) }},
}

func parseFixture(t *testing.T, name string) *ir.SchemaIR {
	t.Helper()
	s, err := parser.Parse(filepath.Join(schemasDir, name+".go"))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return s
}

func TestGolden(t *testing.T) {
	for _, schema := range goldenSchemas {
		s := parseFixture(t, schema)
		for _, g := range generators {
			t.Run(schema+"/"+g.file, func(t *testing.T) {
				got, err := g.gen(s)
				if err != nil {
					t.Fatalf("generate: %v", err)
				}
				again, err := g.gen(s)
				if err != nil {
					t.Fatalf("generate again: %v", err)
				}
				if !bytes.Equal(got, again) {
					t.Fatal("output is not deterministic")
				}
				path := filepath.Join(goldenDir, schema, g.file)
				if *update {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, got, 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read golden file (run with -update to create it): %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("output differs from %s (run with -update to accept):\n%s", path, firstDiff(string(want), string(got)))
				}
			})
		}
	}
}

// firstDiff describes the first differing line of want and got.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return "line " + strconv.Itoa(i+1) + ":\n  want: " + w + "\n  got:  " + g
		}
	}
	return "(no line difference)"
}

// ---------------------------------------------------------------------------
// Helpers for hand-built schemas

func schemaOf(entries ...ir.EntryIR) *ir.SchemaIR {
	return &ir.SchemaIR{Version: ir.Version, Entries: entries}
}

func run(t *testing.T, gen func(*ir.SchemaIR) ([]byte, error), s *ir.SchemaIR) string {
	t.Helper()
	out, err := gen(s)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return string(out)
}

func manPage(s *ir.SchemaIR) ([]byte, error) { return ManPage(s, manApp, manVersion, manDate) }

func mustContain(t *testing.T, what, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, out)
		}
	}
}

func mustNotContain(t *testing.T, what, out string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(out, w) {
			t.Errorf("%s: unexpected %q in:\n%s", what, w, out)
		}
	}
}

// mustOrder asserts that each string occurs in out after the previous one.
func mustOrder(t *testing.T, what, out string, seq ...string) {
	t.Helper()
	pos := 0
	for _, s := range seq {
		i := strings.Index(out[pos:], s)
		if i < 0 {
			t.Errorf("%s: %q not found after offset %d in:\n%s", what, s, pos, out)
			return
		}
		pos += i + len(s)
	}
}

func jsonProps(t *testing.T, s *ir.SchemaIR) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(run(t, JSONSchema, s)), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return doc["properties"].(map[string]any)
}

// ---------------------------------------------------------------------------
// I/O matrix

func TestTagSections(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "zeta", Type: ir.TypeString, Env: "ZETA"},
		ir.EntryIR{ID: "port", Type: ir.TypeInt, Env: "PORT", Tags: []string{"server"}},
		ir.EntryIR{ID: "db_host", Type: ir.TypeString, Env: "DB_HOST", Tags: []string{"database"}},
		ir.EntryIR{ID: "addr", Type: ir.TypeString, Env: "ADDR", Tags: []string{"server"}},
		ir.EntryIR{ID: "alpha", Type: ir.TypeString, Env: "ALPHA"},
	)
	mustOrder(t, "env", run(t, EnvExample, s), "── Server", "ADDR=", "PORT=", "── Database", "DB_HOST=", "── Other", "ALPHA=", "ZETA=")
	mustOrder(t, "yaml", run(t, YAMLExample, s), "── Server", "addr:", "port:", "── Database", "db_host:", "── Other", "alpha:", "zeta:")
	mustOrder(t, "markdown", run(t, Markdown, s), "## Server", "`addr`", "`port`", "## Database", "`db_host`", "## Other", "`alpha`", "`zeta`")
	mustOrder(t, "man", run(t, manPage, s), ".SS Server", ".B ADDR", ".B PORT", ".SS Database", ".B DB_HOST", ".SS Other", ".B ALPHA", ".B ZETA")
}

func TestNestedTags(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "name", Type: ir.TypeString, Env: "NAME"},
		ir.EntryIR{ID: "db", Type: ir.TypeObject, Tags: []string{"database"}, Properties: []ir.EntryIR{
			{ID: "host", Type: ir.TypeString, Env: "DB_HOST"},
			{ID: "pool", Type: ir.TypeObject, Properties: []ir.EntryIR{
				{ID: "size", Type: ir.TypeInt, Env: "DB_POOL_SIZE"},
			}},
		}},
	)
	mustOrder(t, "env", run(t, EnvExample, s), "── Database", "DB_HOST=", "DB_POOL_SIZE=", "── Other", "NAME=")
	mustOrder(t, "markdown", run(t, Markdown, s), "## Database", "`db.host`", "`db.pool.size`", "## Other", "`name`")
	yaml := run(t, YAMLExample, s)
	mustContain(t, "yaml", yaml, "db:\n  host: \"\"\n  pool:\n    size: 0\n")
	props := jsonProps(t, s)
	pool := props["db"].(map[string]any)["properties"].(map[string]any)["pool"].(map[string]any)
	if _, ok := pool["properties"].(map[string]any)["size"]; !ok {
		t.Errorf("json: db.pool.size missing: %v", pool)
	}
}

func TestSensitive(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "db", Type: ir.TypeObject, Properties: []ir.EntryIR{
			{ID: "password", Type: ir.TypeString, Env: "DB_PASSWORD", Required: true, Sensitive: true, Default: "leak1"},
		}},
		ir.EntryIR{ID: "token", Type: ir.TypeString, Env: "TOKEN", Sensitive: true, HasGetter: true, Default: "leak2"},
	)
	env := run(t, EnvExample, s)
	mustContain(t, "env", env, "# REQUIRED — sensitive, do not commit\nDB_PASSWORD=\n", "# sensitive, do not commit\nTOKEN=\n")
	yaml := run(t, YAMLExample, s)
	mustContain(t, "yaml", yaml, "  # (required)\n  # sensitive — do not commit this value\n  password: \"\"\n",
		"# sensitive — do not commit this value\ntoken: \"\"\n")
	md := run(t, Markdown, s)
	mustContain(t, "markdown", md, "| `db.password` | `DB_PASSWORD` | — | string 🔒 | — | **Yes** |", "| `token` | `TOKEN` | — | string 🔒 | — | No |")
	man := run(t, manPage, s)
	mustContain(t, "man", man, ".B DB_PASSWORD\n\\fBREQUIRED. SENSITIVE.\\fR\n", ".B TOKEN\n\\fBSENSITIVE.\\fR\n")
	mustNotContain(t, "man", man, "TOKEN\n\\fBREQUIRED")
	js := run(t, JSONSchema, s)
	for name, out := range map[string]string{"env": env, "yaml": yaml, "markdown": md, "man": man, "json": js} {
		mustNotContain(t, name, out, "leak1", "leak2")
	}
	props := jsonProps(t, s)
	db := props["db"].(map[string]any)
	pw := db["properties"].(map[string]any)["password"].(map[string]any)
	if pw["writeOnly"] != true {
		t.Errorf("json: password not writeOnly: %v", pw)
	}
	if req, _ := db["required"].([]any); len(req) != 1 || req[0] != "password" {
		t.Errorf("json: db.required = %v, want [password]", db["required"])
	}
	if tok := props["token"].(map[string]any); tok["writeOnly"] != true {
		t.Errorf("json: token not writeOnly: %v", tok)
	}
}

func TestDeprecated(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "legacy_host", Type: ir.TypeString, Env: "LEGACY_HOST", Description: "Old host", Deprecated: "use db.host instead"},
		ir.EntryIR{ID: "host", Type: ir.TypeString, Env: "HOST"},
	)
	env := run(t, EnvExample, s)
	mustContain(t, "env", env, "## DEPRECATED: use db.host instead\n# LEGACY_HOST=\n")
	mustOrder(t, "env", env, "── Other", "HOST=", "── DEPRECATED", "LEGACY_HOST")
	mustContain(t, "yaml", run(t, YAMLExample, s), "# DEPRECATED: use db.host instead\nlegacy_host: \"\"\n")
	lh := jsonProps(t, s)["legacy_host"].(map[string]any)
	if lh["deprecated"] != true || lh["description"] != "Old host (DEPRECATED: use db.host instead)" {
		t.Errorf("json: legacy_host = %v", lh)
	}
	md := run(t, Markdown, s)
	mustOrder(t, "markdown", md, "## Other", "`host`", "## Deprecated", "| ~~`legacy_host`~~ | `LEGACY_HOST` | string | use db.host instead |")
	mustNotContain(t, "markdown", md, "| `legacy_host` |")
	mustContain(t, "man", run(t, manPage, s), ".B LEGACY_HOST\nOld host.\n.B \"DEPRECATED: use db.host instead\"\n")
}

func TestNoEnv(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "json_output", Type: ir.TypeBool, FlagLong: "--json"},
		ir.EntryIR{ID: "srv", Type: ir.TypeObject, Properties: []ir.EntryIR{{ID: "load", Type: ir.TypeFloat64, Default: 1}}},
	)
	env := run(t, EnvExample, s)
	mustContain(t, "env", env, "# json_output: no env var; use config file\n", "# srv.load: no env var; use config file\n")
	mustNotContain(t, "env", env, "=")
	man := run(t, manPage, s)
	mustNotContain(t, "man", man, "json_output", "JSON", "load")
	mustContain(t, "yaml", run(t, YAMLExample, s), "srv:\n  load: 1.0\n")
}

func TestComputed(t *testing.T) {
	s := schemaOf(ir.EntryIR{ID: "db", Type: ir.TypeObject, Properties: []ir.EntryIR{
		{ID: "host", Type: ir.TypeString, Env: "DB_HOST"},
		{ID: "dsn", Type: ir.TypeComputed, HasGetter: true, ResultType: "string"},
	}})
	mustNotContain(t, "env", run(t, EnvExample, s), "dsn", "DSN")
	mustNotContain(t, "man", run(t, manPage, s), "dsn", "DSN")
	mustNotContain(t, "json", run(t, JSONSchema, s), "dsn")
	yaml := run(t, YAMLExample, s)
	mustContain(t, "yaml", yaml, "  # dsn is computed; do not set manually\n")
	mustNotContain(t, "yaml", yaml, "dsn:")
	mustContain(t, "markdown", run(t, Markdown, s), "| `db.dsn` | — | — | computed | — | No | derived from siblings |")
}

func TestValidatorKeywords(t *testing.T) {
	v := func(name string, params map[string]any) []ir.ValidatorIR {
		return []ir.ValidatorIR{{Name: name, Params: params}}
	}
	s := schemaOf(
		ir.EntryIR{ID: "email", Type: ir.TypeString, BuiltinValidators: v("email", nil)},
		ir.EntryIR{ID: "url", Type: ir.TypeString, BuiltinValidators: v("url", nil)},
		ir.EntryIR{ID: "port", Type: ir.TypeInt, BuiltinValidators: v("port", nil)},
		ir.EntryIR{ID: "port_range", Type: ir.TypeInt, BuiltinValidators: v("port_range", map[string]any{"min": 1024, "max": 65535})},
		ir.EntryIR{ID: "base64", Type: ir.TypeString, BuiltinValidators: v("base64", nil)},
		ir.EntryIR{ID: "regex", Type: ir.TypeString, BuiltinValidators: v("regex", map[string]any{"pattern": "^[a-z]+$"})},
		ir.EntryIR{ID: "duration", Type: ir.TypeString, BuiltinValidators: v("duration", nil)},
		ir.EntryIR{ID: "timeout", Type: ir.TypeDuration},
		ir.EntryIR{ID: "semver", Type: ir.TypeString, BuiltinValidators: v("semver", nil)},
		ir.EntryIR{ID: "enum", Type: ir.TypeString, BuiltinValidators: v("enum", map[string]any{"values": []any{"text", "json"}})},
		ir.EntryIR{ID: "range", Type: ir.TypeFloat64, BuiltinValidators: v("range", map[string]any{"min": 0.0, "max": 100.0})},
		ir.EntryIR{ID: "hosts", Type: ir.TypeList, ElementType: &ir.EntryIR{ID: "h", Type: ir.TypeString, BuiltinValidators: v("url", nil)}},
	)
	props := jsonProps(t, s)
	check := func(id, key string, want any) {
		t.Helper()
		got, _ := json.Marshal(props[id].(map[string]any)[key])
		w, _ := json.Marshal(want)
		if string(got) != string(w) {
			t.Errorf("%s.%s = %s, want %s", id, key, got, w)
		}
	}
	check("email", "format", "email")
	check("url", "format", "uri")
	check("port", "minimum", 1)
	check("port", "maximum", 65535)
	check("port_range", "minimum", 1024)
	check("port_range", "maximum", 65535)
	check("base64", "contentEncoding", "base64")
	check("regex", "pattern", "^[a-z]+$")
	check("duration", "pattern", durationPattern)
	check("timeout", "pattern", durationPattern)
	check("semver", "pattern", semverPattern)
	check("enum", "enum", []string{"text", "json"})
	check("range", "minimum", 0)
	check("range", "maximum", 100)
	check("hosts", "type", "array")
	check("hosts", "items", map[string]any{"type": "string", "format": "uri"})
	check("timeout", "type", "string")
	check("range", "type", "number")
}

func TestConstraintNote(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "a", Type: ir.TypeBool},
		ir.EntryIR{ID: "b", Type: ir.TypeBool},
	)
	s.Constraints = []ir.ConstraintIR{{Type: ir.ConstraintMutuallyExclusive, EntryIDs: []string{"a", "b"}}}
	md := run(t, Markdown, s)
	if !strings.HasSuffix(md, "> **Note:** `a` and `b` are mutually exclusive; set at most one of them.\n") {
		t.Errorf("markdown: constraint note not at end:\n%s", md)
	}
}

func TestGroffEscaping(t *testing.T) {
	s := schemaOf(
		ir.EntryIR{ID: "a", Type: ir.TypeString, Env: "A", Description: `.leading dot with a-hyphen and C:\path`},
		ir.EntryIR{ID: "b", Type: ir.TypeString, Env: "B", Deprecated: `say "no"`},
	)
	man := run(t, manPage, s)
	mustContain(t, "man", man, "\\&.leading dot with a\\-hyphen and C:\\epath.\n", `.B "DEPRECATED: say \(dqno\(dq"`)
	if !strings.HasPrefix(man, ".\\\" cfgx v1.0.0 2026-09-29\n.SH CONFIGURATION\n") {
		t.Errorf("man: unexpected header:\n%s", man)
	}
}

func TestMarkdownEscaping(t *testing.T) {
	s := schemaOf(ir.EntryIR{ID: "sep", Type: ir.TypeString, Default: "a|b", Description: "pipe | here", IsLive: true})
	mustContain(t, "markdown", run(t, Markdown, s), "| `sep` | — | — | string | `a\\|b` | No | pipe \\| here (🔄 hot-reload) |")
}

func TestYAMLQuoting(t *testing.T) {
	cases := map[string]string{
		"":          `""`,
		"true":      `"true"`,
		"No":        `"No"`,
		"~":         `"~"`,
		"null":      `"null"`,
		"8080":      `"8080"`,
		"1.5":       `"1.5"`,
		"-x":        `"-x"`,
		"*ref":      `"*ref"`,
		"a: b":      `"a: b"`,
		"a #b":      `"a #b"`,
		"plain":     "plain",
		"30s":       "30s",
		"http://a":  "http://a",
		"0.0.0.0":   "0.0.0.0",
		"with\"q":   `with"q`,
		" padded":   `" padded"`,
		"line\nbrk": `"line\nbrk"`,
	}
	for in, want := range cases {
		if got := yamlString(in); got != want {
			t.Errorf("yamlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	gens := map[string]func(*ir.SchemaIR) ([]byte, error){
		"env": EnvExample, "yaml": YAMLExample, "json": JSONSchema, "markdown": Markdown, "man": manPage,
	}
	for name, gen := range gens {
		if _, err := gen(nil); err == nil {
			t.Errorf("%s: nil schema: want error", name)
		}
		if _, err := gen(&ir.SchemaIR{Version: "2"}); err == nil {
			t.Errorf("%s: version 2: want error", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Format sanity checks on the golden schemas

// TestYAMLStructure checks, without a YAML parser, that indentation is even,
// that nesting grows by one level at a time and that keys are unique within
// each mapping.
func TestYAMLStructure(t *testing.T) {
	for _, name := range goldenSchemas {
		out := run(t, YAMLExample, parseFixture(t, name))
		type scope struct {
			indent int
			keys   map[string]bool
		}
		stack := []scope{{0, map[string]bool{}}}
		for n, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			trimmed := strings.TrimLeft(line, " ")
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(trimmed)
			if indent%2 != 0 {
				t.Errorf("%s: line %d: odd indentation: %q", name, n+1, line)
			}
			if strings.HasPrefix(trimmed, "- ") {
				continue
			}
			for len(stack) > 1 && indent < stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
			}
			top := stack[len(stack)-1]
			if indent > top.indent {
				if indent != top.indent+2 {
					t.Errorf("%s: line %d: indentation jumps: %q", name, n+1, line)
				}
				stack = append(stack, scope{indent, map[string]bool{}})
				top = stack[len(stack)-1]
			}
			key, _, ok := strings.Cut(trimmed, ":")
			if !ok {
				t.Errorf("%s: line %d: not a mapping entry: %q", name, n+1, line)
				continue
			}
			if top.keys[key] {
				t.Errorf("%s: line %d: duplicate key %q", name, n+1, key)
			}
			top.keys[key] = true
		}
	}
}

func TestJSONSchemaValid(t *testing.T) {
	for _, name := range goldenSchemas {
		out := run(t, JSONSchema, parseFixture(t, name))
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if doc["$schema"] != "http://json-schema.org/draft-07/schema#" || doc["type"] != "object" {
			t.Errorf("%s: bad root: $schema=%v type=%v", name, doc["$schema"], doc["type"])
		}
	}
}

func TestManPageGroff(t *testing.T) {
	groff, err := exec.LookPath("groff")
	if err != nil {
		t.Skip("groff not on PATH")
	}
	for _, name := range goldenSchemas {
		out, err := ManPage(parseFixture(t, name), manApp, manVersion, manDate)
		if err != nil {
			t.Fatal(err)
		}
		page := append([]byte(".TH CFGX 1\n"), out...)
		cmd := exec.Command(groff, "-man", "-T", "utf8", "-z", "-ww")
		cmd.Stdin = bytes.NewReader(page)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil || stderr.Len() > 0 {
			t.Errorf("%s: groff failed: %v\n%s", name, err, stderr.String())
		}
	}
}
