package parser_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/example/cfgx"
	"github.com/example/cfgx/gen/parser"
	"github.com/example/cfgx/internal/ir"
)

const fixtures = "../../testdata/schemas"

// update is accepted so that CI can run `go test ./gen/... -update` across all
// generator packages; this package has no golden files and ignores it.
var _ = flag.Bool("update", false, "update golden files (unused in this package)")

func fixture(name string) string { return filepath.Join(fixtures, name) }

// writeSchema writes src to a temporary schema file and returns its path.
func writeSchema(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// inline builds a schema file whose Define call contains items.
func inline(items string) string {
	return "package s\n\nimport (\n\t\"time\"\n\n\t\"github.com/example/cfgx\"\n)\n\nvar _ = time.Second\n\nvar Schema = cfgx.Define(\n" + items + "\n)\n"
}

// find returns the entry at dot path id.
func find(t *testing.T, s *ir.SchemaIR, id string) ir.EntryIR {
	t.Helper()
	entries := s.Entries
	parts := strings.Split(id, ".")
	for i, part := range parts {
		var next *ir.EntryIR
		for j := range entries {
			if entries[j].ID == part {
				next = &entries[j]
				break
			}
		}
		if next == nil {
			t.Fatalf("entry %q not found", id)
		}
		if i == len(parts)-1 {
			return *next
		}
		entries = next.Properties
	}
	panic("unreachable")
}

func equal(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		file     string // fixture name
		src      string // inline source, used when file is empty
		wantErr  string // substring of the ParseError message
		errLine  int    // expected error line, if non-zero
		errCol   int    // expected error column, if non-zero
		validate func(t *testing.T, s *ir.SchemaIR)
	}{
		{
			name: "minimal schema",
			file: "minimal.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				want := []ir.EntryIR{{
					ID: "port", Type: ir.TypeInt, FlagLong: "--port", FlagShort: "-p",
					Env: "APP_PORT", Default: 8080, Description: "HTTP port",
				}}
				equal(t, "entries", s.Entries, want)
				equal(t, "version", s.Version, ir.Version)
			},
		},
		{
			name: "nested object",
			file: "nested.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "server.tls.files.cert")
				equal(t, "cert env", e.Env, "SERVER_TLS_CERT")
				equal(t, "cert depends_on", e.DependsOn, &ir.DependsOnIR{ID: "server.tls.enabled", Value: true})
				equal(t, "timeout default", find(t, s, "server.timeout").Default, "30s")
				equal(t, "load_factor default", find(t, s, "server.load_factor").Default, float64(1))
			},
		},
		{
			name: "computed entry",
			file: "computed.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				for _, id := range []string{"db.dsn", "workers"} {
					e := find(t, s, id)
					equal(t, id+" type", e.Type, ir.TypeComputed)
					equal(t, id+" has_getter", e.HasGetter, true)
					if e.FlagLong != "" || e.FlagShort != "" || e.Env != "" {
						t.Errorf("%s: computed entry has flag/env: %+v", id, e)
					}
				}
				equal(t, "dsn desc", find(t, s, "db.dsn").Description, "Connection string")
				equal(t, "workers tags", find(t, s, "workers").Tags, []string{"runtime"})
			},
		},
		{
			name: "explicit type args on Computed",
			src:  inline(`cfgx.Computed[string]("dsn", func(parent any) string { return "" }),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "entries", s.Entries, []ir.EntryIR{{ID: "dsn", Type: ir.TypeComputed, HasGetter: true}})
			},
		},
		{
			name: "sensitive entry",
			file: "sensitive.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "api_key sensitive", find(t, s, "api_key").Sensitive, true)
				sec := find(t, s, "session_secret")
				equal(t, "session_secret sensitive", sec.Sensitive, true)
				equal(t, "session_secret has_getter", sec.HasGetter, true)
				equal(t, "user sensitive", find(t, s, "user").Sensitive, false)
			},
		},
		{
			name: "deprecated entry",
			file: "deprecated.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "host deprecated", find(t, s, "host").Deprecated, "use server.host instead")
				equal(t, "server deprecated", find(t, s, "server").Deprecated, "server block moves to http in v2")
				equal(t, "server.host deprecated", find(t, s, "server.host").Deprecated, "")
			},
		},
		{
			name: "scoped entry",
			file: "scoped.go",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "port scope", find(t, s, "port").Scope, []string{"serve"})
				equal(t, "db scope", find(t, s, "db").Scope, []string{"serve", "migrate"})
				equal(t, "verbose scope", find(t, s, "verbose").Scope, []string(nil))
			},
		},
		{
			name: "isLive and onChange",
			src:  inline(`cfgx.String("level").Env("LEVEL").IsLive().OnChange(func(old, updated string) {}),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "level")
				equal(t, "is_live", e.IsLive, true)
				equal(t, "has_on_change", e.HasOnChange, true)
			},
		},
		{
			name: "custom validate func literal",
			src:  inline(`cfgx.String("x").Validate(func(v string) error { return nil }),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "x")
				equal(t, "has_validate", e.HasValidate, true)
				equal(t, "builtins", len(e.BuiltinValidators), 0)
			},
		},
		{
			name: "custom validate identifier",
			src:  inline(`cfgx.String("x").Validate(myFn),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "has_validate", find(t, s, "x").HasValidate, true)
			},
		},
		{
			name: "builtin validate Email",
			src:  inline(`cfgx.String("email").Validate(cfgx.Email()),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "email")
				equal(t, "builtins", e.BuiltinValidators, []ir.ValidatorIR{{Name: "email"}})
				equal(t, "has_validate", e.HasValidate, false)
			},
		},
		{
			name: "builtin validate PortRange",
			src:  inline(`cfgx.Int("port").Validate(cfgx.PortRange(1024, 65535)),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "port")
				equal(t, "builtins", e.BuiltinValidators, []ir.ValidatorIR{
					{Name: "port_range", Params: map[string]any{"min": 1024, "max": 65535}},
				})
				equal(t, "has_validate", e.HasValidate, false)
			},
		},
		{
			name: "validate on list element",
			src:  inline(`cfgx.List("hosts", cfgx.String("host").Validate(cfgx.URL()).Validate(check)).Env("HOSTS"),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				el := find(t, s, "hosts").ElementType
				equal(t, "element builtins", el.BuiltinValidators, []ir.ValidatorIR{{Name: "url"}})
				equal(t, "element has_validate", el.HasValidate, true)
			},
		},
		{
			name: "mutuallyExclusive",
			src:  inline(`cfgx.Bool("json"), cfgx.Object("out", cfgx.Bool("text")), cfgx.MutuallyExclusive("json", "out.text"),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "constraints", s.Constraints, []ir.ConstraintIR{
					{Type: ir.ConstraintMutuallyExclusive, EntryIDs: []string{"json", "out.text"}},
				})
			},
		},
		{
			name: "import alias",
			src:  "package s\n\nimport c \"github.com/example/cfgx\"\n\nvar S = c.Define(c.Int(\"x\").Default(1))\n",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "entries", s.Entries, []ir.EntryIR{{ID: "x", Type: ir.TypeInt, Default: 1}})
			},
		},
		{
			name: "time import alias",
			src:  "package s\n\nimport (\n\tt \"time\"\n\n\t\"github.com/example/cfgx\"\n)\n\nvar S = cfgx.Define(cfgx.Duration(\"d\").Default(90 * t.Minute))\n",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "default", find(t, s, "d").Default, "1h30m0s")
			},
		},
		{
			name: "duration defaults",
			src: inline(`cfgx.Duration("a").Default(30 * time.Second),
	cfgx.Duration("b").Default(time.Minute),
	cfgx.Duration("c").Default(time.Hour * 2),
	cfgx.Duration("d").Default(1500 * time.Millisecond),
	cfgx.Duration("e").Default(0),
	cfgx.Duration("f").Default(1.5 * 60 * time.Minute),
	cfgx.Duration("h").Default(time.Second / 2),
	cfgx.Duration("i").Default(time.Second / 3),
	cfgx.Duration("j").Default(3 / 2 * time.Second),
	cfgx.Duration("k").Default(time.Second * 3 / 2),
	cfgx.Duration("g").Default(time.Duration(5) * time.Second),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				want := map[string]string{"a": "30s", "b": "1m0s", "c": "2h0m0s", "d": "1.5s", "e": "0s", "f": "1h30m0s", "g": "5s",
					"h": "500ms", "i": "333.333333ms", "j": "1s", "k": "1.5s"}
				for id, d := range want {
					equal(t, id+" default", find(t, s, id).Default, d)
				}
			},
		},
		{
			name: "negative and float defaults",
			src:  inline(`cfgx.Int("n").Default(-1), cfgx.Float64("f").Default(0.5), cfgx.Float64("g").Default(-2), cfgx.Bool("b").Default(true), cfgx.String("s").Default("hi"),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "n", find(t, s, "n").Default, -1)
				equal(t, "f", find(t, s, "f").Default, 0.5)
				equal(t, "g", find(t, s, "g").Default, float64(-2))
				equal(t, "b", find(t, s, "b").Default, true)
				equal(t, "s", find(t, s, "s").Default, "hi")
			},
		},
		{
			name: "list defaults",
			src: inline(`cfgx.List("s", cfgx.String("e")).Default([]string{"a", "b"}),
	cfgx.List("i", cfgx.Int("e")).Default([]int{1, -2}),
	cfgx.List("d", cfgx.Duration("e")).Default([]time.Duration{time.Second, 2 * time.Minute}),
	cfgx.List("n", cfgx.Int("e")).Default(nil),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "s", find(t, s, "s").Default, []any{"a", "b"})
				equal(t, "i", find(t, s, "i").Default, []any{1, -2})
				equal(t, "d", find(t, s, "d").Default, []any{"1s", "2m0s"})
				equal(t, "n", find(t, s, "n").Default, nil)
				equal(t, "s element", find(t, s, "s").ElementType, &ir.EntryIR{ID: "e", Type: ir.TypeString})
			},
		},
		{
			name: "later builder call wins",
			src:  inline(`cfgx.String("x").Env("A").Env("B").Flag("--x", "-x").Flag("--y").Scope("a").Scope(),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				e := find(t, s, "x")
				equal(t, "env", e.Env, "B")
				equal(t, "flag_long", e.FlagLong, "--y")
				equal(t, "flag_short", e.FlagShort, "")
				equal(t, "scope", e.Scope, []string(nil))
			},
		},
		{
			name: "string concatenation and raw strings",
			src:  inline("cfgx.String(\"x\").Desc(\"a\" + `b`),"),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "desc", find(t, s, "x").Description, "ab")
			},
		},
		{
			name: "DependsOn sibling and root path",
			src: inline(`cfgx.Bool("tls"),
	cfgx.Object("db", cfgx.Bool("tls"), cfgx.String("ca").DependsOn("tls", true), cfgx.String("x").DependsOn("db.tls", false)),
	cfgx.String("cert").DependsOn("tls", true),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "db.ca", find(t, s, "db.ca").DependsOn, &ir.DependsOnIR{ID: "tls", Value: true})
				equal(t, "db.x", find(t, s, "db.x").DependsOn, &ir.DependsOnIR{ID: "db.tls", Value: false})
			},
		},

		// Errors.
		{
			name:    "duplicate ID at root level",
			file:    "invalid_duplicate.go",
			wantErr: `duplicate entry ID "port"`,
			errLine: 8, errCol: 2,
		},
		{
			name:    "duplicate ID in nested object",
			src:     inline(`cfgx.Object("db",` + "\n" + `cfgx.String("host"),` + "\n" + `cfgx.String("host"),` + "\n" + `),`),
			wantErr: `duplicate entry ID "host"`,
			errLine: 14, errCol: 1,
		},
		{
			name:    "DependsOn referencing non-existent ID",
			file:    "invalid_depends.go",
			wantErr: `DependsOn references unknown entry ID "missing"`,
			errLine: 8, errCol: 44,
		},
		{
			name:    "DependsOn cannot reach a nested entry without its path",
			src:     inline(`cfgx.Object("db", cfgx.Bool("tls")), cfgx.String("cert").DependsOn("tls", true),`),
			wantErr: `DependsOn references unknown entry ID "tls"`,
		},
		{
			name:    "MutuallyExclusive referencing non-existent ID",
			src:     inline(`cfgx.Bool("a"), cfgx.MutuallyExclusive("a", "b"),`),
			wantErr: `MutuallyExclusive references unknown entry ID "b"`,
			errLine: 12, errCol: 45,
		},
		{
			name:    "non-literal argument",
			src:     inline(`cfgx.String("x").Env(envName),`),
			wantErr: "must be a literal",
			errLine: 12, errCol: 22,
		},
		{
			name:    "non-literal default",
			src:     inline(`cfgx.Int("x").Default(defaultPort),`),
			wantErr: "must be a literal",
		},
		{
			name:    "unknown method",
			src:     inline(`cfgx.Int("x").Foo(),`),
			wantErr: `unknown method "Foo"`,
			errLine: 12, errCol: 15,
		},
		{
			name:    "method not available on type",
			src:     inline(`cfgx.Object("db", cfgx.String("h")).Env("DB"),`),
			wantErr: `unknown method "Env"`,
		},
		{
			name:    "no Define call",
			src:     "package s\n\nimport \"github.com/example/cfgx\"\n\nvar S = cfgx.Int(\"x\")\n",
			wantErr: "no cfgx.Define(...) call found",
		},
		{
			name:    "two Define calls",
			src:     "package s\n\nimport \"github.com/example/cfgx\"\n\nvar A = cfgx.Define(cfgx.Int(\"x\"))\n\nvar B = cfgx.Define(cfgx.Int(\"y\"))\n",
			wantErr: "more than one cfgx.Define(...) call",
			errLine: 7, errCol: 9,
		},
		{
			name:    "missing cfgx import",
			src:     "package s\n\nvar S = cfgx.Define()\n",
			wantErr: `does not import "github.com/example/cfgx"`,
		},
		{
			name:    "division by zero in duration",
			src:     inline(`cfgx.Duration("d").Default(time.Second / 0),`),
			wantErr: "division by zero",
		},
		{
			name:    "non-integer duration constant",
			src:     inline(`cfgx.Duration("d").Default(1.5 * time.Hour),`),
			wantErr: "truncates a non-integer constant",
		},
		{
			name:    "list default element type mismatch",
			src:     inline(`cfgx.List("ports", cfgx.Int("p")).Default([]string{"a"}),`),
			wantErr: "does not match the list's int element entry",
		},
		{
			name:    "list default any element does not fit",
			src:     inline(`cfgx.List("ports", cfgx.Int("p")).Default([]any{1, "a"}),`),
			wantErr: "does not fit the list's int element entry",
		},
		{
			name:    "DependsOn in list element object references unknown ID",
			src:     inline(`cfgx.List("xs", cfgx.Object("x", cfgx.String("a").DependsOn("missing", true))),`),
			wantErr: `DependsOn references unknown entry ID "missing"`,
		},
		{
			name:    "long non-ASCII expression in message stays valid UTF-8",
			src:     inline(`cfgx.String("x").Env(ééééééééééééééééééééééééééééééééééééééééééé),`),
			wantErr: "must be a literal",
		},
		{
			name:    "dot import",
			src:     "package s\n\nimport . \"github.com/example/cfgx\"\n\nvar S = Define(Int(\"x\"))\n",
			wantErr: "must be imported by name",
		},
		{
			name:    "entry referenced through a variable",
			src:     inline(`port,`),
			wantErr: "expected a cfgx entry constructor call",
		},
		{
			name:    "ID with a dot",
			src:     inline(`cfgx.String("db.host"),`),
			wantErr: `must not contain '.'`,
		},
		{
			name:    "empty object",
			src:     inline(`cfgx.Object("db"),`),
			wantErr: "at least one property",
		},
		{
			name:    "sensitive without env or getter",
			src:     inline(`cfgx.String("secret").Sensitive(),`),
			wantErr: `sensitive entry "secret" must have an Env or a Getter`,
		},
		{
			name:    "default type mismatch",
			src:     inline(`cfgx.Int("x").Default("8080"),`),
			wantErr: "Default of a int entry must be a int literal",
		},
		{
			name:    "builtin validator on wrong type",
			src:     inline(`cfgx.Int("x").Validate(cfgx.Email()),`),
			wantErr: "cfgx.Email validates string values and cannot be used on a int entry",
		},
		{
			name:    "invalid regex",
			src:     inline(`cfgx.String("x").Validate(cfgx.Regex("(")),`),
			wantErr: "invalid Regex pattern",
		},
		{
			name:    "too many short flags",
			src:     inline(`cfgx.String("x").Flag("--x", "-x", "-y"),`),
			wantErr: "at most one short flag",
		},
		{
			name:    "MutuallyExclusive with one ID",
			src:     inline(`cfgx.Bool("a"), cfgx.MutuallyExclusive("a"),`),
			wantErr: "requires at least two IDs",
		},
		{
			name: "list default with any elements that fit",
			src:  inline(`cfgx.List("ports", cfgx.Float64("p")).Default([]any{1, 2.5}), cfgx.List("ds", cfgx.Duration("d")).Default([]any{time.Second}),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "ports", find(t, s, "ports").Default, []any{1, 2.5})
				equal(t, "ds", find(t, s, "ds").Default, []any{"1s"})
			},
		},
		{
			name: "DependsOn in list element object resolves",
			src: inline(`cfgx.Bool("flag"),
	cfgx.List("xs", cfgx.Object("x", cfgx.Bool("on"), cfgx.String("a").DependsOn("on", true), cfgx.String("b").DependsOn("flag", true))),`),
			validate: func(t *testing.T, s *ir.SchemaIR) {
				props := find(t, s, "xs").ElementType.Properties
				equal(t, "a depends_on", props[1].DependsOn, &ir.DependsOnIR{ID: "on", Value: true})
				equal(t, "b depends_on", props[2].DependsOn, &ir.DependsOnIR{ID: "flag", Value: true})
			},
		},
		{
			name: "blank import alongside named import",
			src:  "package s\n\nimport (\n\t_ \"github.com/example/cfgx\"\n\tc \"github.com/example/cfgx\"\n)\n\nvar S = c.Define(c.Int(\"x\"))\n",
			validate: func(t *testing.T, s *ir.SchemaIR) {
				equal(t, "entries", s.Entries, []ir.EntryIR{{ID: "x", Type: ir.TypeInt}})
			},
		},
		{
			name:    "only a blank import",
			src:     "package s\n\nimport _ \"github.com/example/cfgx\"\n\nvar S = cfgx.Define()\n",
			wantErr: `does not import "github.com/example/cfgx" by name`,
		},
		{
			name:    "syntax error",
			src:     "package s\n\nvar = \n",
			wantErr: "expected",
			errLine: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.file != "" {
				path = fixture(tt.file)
			} else {
				path = writeSchema(t, tt.src)
			}
			s, err := parser.Parse(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse succeeded, want error containing %q", tt.wantErr)
				}
				var pe *parser.ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("error %v (%T) is not a *ParseError", err, err)
				}
				if !utf8.ValidString(pe.Message) {
					t.Errorf("error message is not valid UTF-8: %q", pe.Message)
				}
				if !strings.Contains(pe.Message, tt.wantErr) {
					t.Fatalf("error message %q does not contain %q", pe.Message, tt.wantErr)
				}
				if pe.File != path {
					t.Errorf("File = %q, want %q", pe.File, path)
				}
				if tt.errLine != 0 && pe.Line != tt.errLine {
					t.Errorf("Line = %d, want %d (%v)", pe.Line, tt.errLine, err)
				}
				if tt.errCol != 0 && pe.Column != tt.errCol {
					t.Errorf("Column = %d, want %d (%v)", pe.Column, tt.errCol, err)
				}
				if s != nil {
					t.Errorf("Parse returned a schema alongside an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tt.validate != nil {
				tt.validate(t, s)
			}
			checkInvariants(t, s)
		})
	}
}

func TestParseErrorFormat(t *testing.T) {
	_, err := parser.Parse(fixture("invalid_duplicate.go"))
	var pe *parser.ParseError
	if !errors.As(fmt.Errorf("wrapped: %w", err), &pe) {
		t.Fatalf("errors.As did not find *ParseError in %v", err)
	}
	want := fmt.Sprintf("%s:%d:%d: %s", fixture("invalid_duplicate.go"), 8, 2, `duplicate entry ID "port"`)
	if got := pe.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	_, err = parser.Parse(filepath.Join(t.TempDir(), "missing.go"))
	if !errors.As(err, &pe) {
		t.Fatalf("missing file: error %v is not a *ParseError", err)
	}
}

func TestParseWithWarnings(t *testing.T) {
	path := writeSchema(t, inline(`cfgx.String("ok").Env("OK"),
	cfgx.String("h").Required().Default("x"),`))
	s, warns, err := parser.ParseWithWarnings(path)
	if err != nil {
		t.Fatalf("ParseWithWarnings: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warns), warns)
	}
	w := warns[0]
	if w.File != path || w.Line != 13 || w.Column != 2 || !strings.Contains(w.Message, `"h"`) {
		t.Errorf("warning = %+v", w)
	}
	if want := fmt.Sprintf("%s:13:2: %s", path, w.Message); w.String() != want {
		t.Errorf("String() = %q, want %q", w.String(), want)
	}
	equal(t, "h", find(t, s, "h").Default, "x")

	s2, err := parser.Parse(path)
	if err != nil {
		t.Fatalf("Parse returned error for a warning-only schema: %v", err)
	}
	equal(t, "Parse IR", s2, s)

	_, warns, err = parser.ParseWithWarnings(fixture("full.go"))
	if err != nil || len(warns) != 0 {
		t.Errorf("full.go: warnings %v, err %v", warns, err)
	}
}

// TestParseMatchesRuntime checks that parsing a fixture yields the same IR as
// building the same schema with the cfgx DSL and calling ToIR.
func TestParseMatchesRuntime(t *testing.T) {
	tests := []struct {
		file   string
		schema *cfgx.Schema
	}{
		{
			file: "minimal.go",
			schema: cfgx.Define(
				cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080).Desc("HTTP port"),
			),
		},
		{
			file: "nested.go",
			schema: cfgx.Define(
				cfgx.String("name").Env("APP_NAME").Default("cfgx"),
				cfgx.Object("server",
					cfgx.String("host").Env("SERVER_HOST").Default("0.0.0.0"),
					cfgx.Duration("timeout").Env("SERVER_TIMEOUT").Default(30*time.Second),
					cfgx.Float64("load_factor").Default(1),
					cfgx.List("origins", cfgx.String("origin")).Env("SERVER_ORIGINS").Default([]string{"a", "b"}),
					cfgx.Object("tls",
						cfgx.Bool("enabled").Env("SERVER_TLS_ENABLED").Default(false),
						cfgx.Int("min_version").Env("SERVER_TLS_MIN_VERSION").Default(12).DependsOn("enabled", true),
						cfgx.Object("files",
							cfgx.String("cert").Env("SERVER_TLS_CERT").DependsOn("server.tls.enabled", true),
							cfgx.String("key").Env("SERVER_TLS_KEY").Sensitive(),
						).Desc("Certificate files"),
					).Desc("TLS settings"),
				).Desc("HTTP server").Tags("server"),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := parser.Parse(fixture(tt.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			want := tt.schema.ToIR()
			if !reflect.DeepEqual(got, want) {
				gj, _ := json.MarshalIndent(got, "", "  ")
				wj, _ := json.MarshalIndent(want, "", "  ")
				t.Errorf("parsed IR differs from ToIR\ngot:  %s\nwant: %s", gj, wj)
			}
		})
	}
}

func TestParseFull(t *testing.T) {
	s, err := parser.Parse(fixture("full.go"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkInvariants(t, s)

	// JSON round trip is byte-identical.
	first, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back ir.SchemaIR
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("JSON round trip differs\nfirst:  %s\nsecond: %s", first, second)
	}

	// All ten built-in validators are detected.
	names := map[string]bool{}
	var collect func(entries []ir.EntryIR)
	collect = func(entries []ir.EntryIR) {
		for _, e := range entries {
			for _, v := range e.BuiltinValidators {
				names[v.Name] = true
			}
			collect(e.Properties)
			if e.ElementType != nil {
				collect([]ir.EntryIR{*e.ElementType})
			}
		}
	}
	collect(s.Entries)
	for _, n := range []string{"email", "url", "port", "port_range", "base64", "regex", "duration", "semver", "enum", "range"} {
		if !names[n] {
			t.Errorf("built-in validator %q not found", n)
		}
	}

	equal(t, "port", find(t, s, "port"), ir.EntryIR{
		ID: "port", Type: ir.TypeInt, FlagLong: "--port", FlagShort: "-p", Env: "APP_PORT",
		Default: 8080, Description: "HTTP port the server listens on",
		BuiltinValidators: []ir.ValidatorIR{{Name: "port_range", Params: map[string]any{"min": 1024, "max": 65535}}},
		Scope:             []string{"serve"}, Tags: []string{"server"},
	})
	name := find(t, s, "name")
	equal(t, "name builtins", name.BuiltinValidators, []ir.ValidatorIR{{Name: "regex", Params: map[string]any{"pattern": `^[a-z][a-z0-9-]*$`}}})
	equal(t, "name has_validate", name.HasValidate, true)
	equal(t, "version has_validate", find(t, s, "version").HasValidate, true)
	equal(t, "log_format", find(t, s, "log_format").BuiltinValidators, []ir.ValidatorIR{{Name: "enum", Params: map[string]any{"values": []string{"text", "json"}}}})
	lf := find(t, s, "log_format")
	equal(t, "log_format live", lf.IsLive && lf.HasOnChange, true)
	equal(t, "sample_rate", find(t, s, "sample_rate").BuiltinValidators, []ir.ValidatorIR{{Name: "range", Params: map[string]any{"min": 0.0, "max": 1.0}}})
	equal(t, "timeout", find(t, s, "timeout").Default, "30s")
	equal(t, "token getter", find(t, s, "token").HasGetter, true)
	equal(t, "legacy deprecated", find(t, s, "legacy_host").Deprecated, "use db.host instead")
	hosts := find(t, s, "hosts")
	equal(t, "hosts default", hosts.Default, []any{"http://a", "http://b"})
	equal(t, "hosts has_validate", hosts.HasValidate, true)
	equal(t, "hosts element", hosts.ElementType.BuiltinValidators, []ir.ValidatorIR{{Name: "url"}})
	equal(t, "db scope", find(t, s, "db").Scope, []string{"serve", "migrate"})
	equal(t, "db.pool.idle_timeout", find(t, s, "db.pool.idle_timeout").Default, "5m0s")
	dsn := find(t, s, "db.dsn")
	equal(t, "dsn", dsn.HasGetter && dsn.IsLive && dsn.HasOnChange, true)
	equal(t, "report_dir depends", find(t, s, "report_dir").DependsOn, &ir.DependsOnIR{ID: "db.tls", Value: true})
	equal(t, "constraints", s.Constraints, []ir.ConstraintIR{
		{Type: ir.ConstraintMutuallyExclusive, EntryIDs: []string{"json_output", "text_output"}},
	})
}

// checkInvariants verifies SchemaIR invariants 1–8 from docs/design/schema-ir-spec.md.
func checkInvariants(t *testing.T, s *ir.SchemaIR) {
	t.Helper()
	if s.Version != ir.Version {
		t.Errorf("version = %q", s.Version)
	}
	index := map[string]bool{}
	var walk func(entries []ir.EntryIR, parent string, indexed bool)
	walk = func(entries []ir.EntryIR, parent string, indexed bool) {
		seen := map[string]bool{}
		for _, e := range entries {
			path := e.ID
			if parent != "" {
				path = parent + "." + e.ID
			}
			if seen[e.ID] { // 1
				t.Errorf("invariant 1: duplicate ID %q", path)
			}
			seen[e.ID] = true
			if indexed {
				index[path] = true
			}
			checkEntry(t, path, e)
			walk(e.Properties, path, indexed)
			if e.ElementType != nil {
				walk([]ir.EntryIR{*e.ElementType}, path+"[]", false)
			}
		}
	}
	walk(s.Entries, "", true)

	// Invariant 5. parent is the scope for sibling lookup. Entries inside a List
	// element are not addressable, so they use the List's scope, plus their
	// siblings within the element Object (local).
	var deps func(entries []ir.EntryIR, parent string, inElem bool)
	checkDep := func(e ir.EntryIR, parent string, local map[string]bool) {
		if d := e.DependsOn; d != nil {
			if !local[d.ID] && !index[d.ID] && !(parent != "" && index[parent+"."+d.ID]) {
				t.Errorf("invariant 5: %q depends on unknown %q", e.ID, d.ID)
			}
		}
	}
	deps = func(entries []ir.EntryIR, parent string, inElem bool) {
		var local map[string]bool
		if inElem {
			local = map[string]bool{}
			for _, e := range entries {
				local[e.ID] = true
			}
		}
		for _, e := range entries {
			checkDep(e, parent, local)
			if inElem {
				deps(e.Properties, parent, true)
			} else {
				path := e.ID
				if parent != "" {
					path = parent + "." + e.ID
				}
				deps(e.Properties, path, false)
			}
			if el := e.ElementType; el != nil {
				checkDep(*el, parent, nil)
				deps(el.Properties, parent, true)
			}
		}
	}
	deps(s.Entries, "", false)
	for _, c := range s.Constraints { // 6
		for _, id := range c.EntryIDs {
			if !index[id] {
				t.Errorf("invariant 6: constraint references unknown %q", id)
			}
		}
	}
}

func checkEntry(t *testing.T, path string, e ir.EntryIR) {
	t.Helper()
	switch e.Type {
	case ir.TypeObject: // 2
		if len(e.Properties) == 0 {
			t.Errorf("invariant 2: object %q has no properties", path)
		}
	case ir.TypeList: // 3
		if e.ElementType == nil {
			t.Errorf("invariant 3: list %q has no element type", path)
		}
	case ir.TypeComputed: // 4
		if e.FlagLong != "" || e.FlagShort != "" || e.Env != "" {
			t.Errorf("invariant 4: computed %q has flag/env", path)
		}
	}
	if e.Default != nil { // 7
		ok := false
		switch e.Type {
		case ir.TypeString, ir.TypeDuration:
			_, ok = e.Default.(string)
		case ir.TypeInt:
			_, ok = e.Default.(int)
		case ir.TypeBool:
			_, ok = e.Default.(bool)
		case ir.TypeFloat64:
			_, ok = e.Default.(float64)
		case ir.TypeList:
			_, ok = e.Default.([]any)
		case ir.TypeObject, ir.TypeComputed:
			ok = false // must be null
		}
		if !ok {
			t.Errorf("invariant 7: %s entry %q has default %#v", e.Type, path, e.Default)
		}
	}
	if e.Sensitive && !e.HasGetter && e.Env == "" { // 8
		t.Errorf("invariant 8: sensitive %q has neither Env nor Getter", path)
	}
}
