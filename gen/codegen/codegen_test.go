package codegen

import (
	"bytes"
	"flag"
	"go/format"
	"os"
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
	goldenDir  = "../../testdata/golden"
)

// goldenCases maps each golden test to its schema fixture.
var goldenCases = []struct {
	name   string
	schema string
}{
	{"minimal", "minimal.go"},
	{"full_schema", "full.go"},
	{"nested_objects", "nested.go"},
	{"sensitive_fields", "sensitive.go"},
	{"computed_fields", "computed.go"},
	{"scoped_entries", "scoped.go"},
	{"is_live", "live.go"},
}

func parseFixture(t *testing.T, name string) *ir.SchemaIR {
	t.Helper()
	s, err := parser.Parse(filepath.Join(schemasDir, name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return s
}

func goldenOptions() Options {
	return Options{Package: "config", Source: "cmd/schema.go"}
}

func TestGenerate(t *testing.T) {
	for _, tt := range goldenCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Generate(parseFixture(t, tt.schema), goldenOptions())
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			formatted, err := format.Source(got)
			if err != nil {
				t.Fatalf("output is not valid Go: %v", err)
			}
			if !bytes.Equal(formatted, got) {
				t.Error("output is not gofmt-clean")
			}
			path := filepath.Join(goldenDir, tt.name, "config.gen.go")
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

func TestGenerateDeterministic(t *testing.T) {
	s := parseFixture(t, "full.go")
	first, err := Generate(s, goldenOptions())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Generate(s, goldenOptions())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("Generate output is not deterministic")
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

// gen generates code for entries with default options.
func gen(t *testing.T, entries ...ir.EntryIR) string {
	t.Helper()
	out, err := Generate(&ir.SchemaIR{Version: ir.Version, Entries: entries}, Options{Package: "config"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return string(out)
}

// genErr returns the Generate error for entries, failing if there is none.
func genErr(t *testing.T, entries ...ir.EntryIR) error {
	t.Helper()
	_, err := Generate(&ir.SchemaIR{Version: ir.Version, Entries: entries}, Options{Package: "config"})
	if err == nil {
		t.Fatal("Generate error = nil, want non-nil")
	}
	return err
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output does not contain %q\n%s", w, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(out, b) {
			t.Errorf("output contains %q", b)
		}
	}
}

func TestMinimalMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "minimal.go").Entries...)
	mustContain(t, out,
		"type Config struct {\n\tport int\n}",
		"func (c *Config) Port() int { return c.port }",
		`flags.IntP("port", "p", 8080, "HTTP port")`,
		`_ = v.BindEnv("port", "APP_PORT")`,
		`v.SetDefault("port", 8080)`,
		"func Load(schema *cfgx.Schema, v *viper.Viper) (*Config, error) {",
		`cfg.port = v.GetInt("port")`,
		`_ = v.BindPFlag("port", flags.Lookup("port"))`,
	)
	// Imports are exactly those used.
	mustNotContain(t, out, `"fmt"`, `"time"`, `"context"`, `"sync/atomic"`, "cfgx/runtime", "sensitiveString")
}

func TestScopedMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "scoped.go").Entries...)
	mustContain(t, out,
		"type Config struct {\n\tverbose bool\n}",
		"type ServeConfig struct {\n\tConfig\n\tport int\n\tdb   DbConfig\n}",
		"type MigrateConfig struct {\n\tConfig\n\tdb DbConfig\n}",
		"func LoadServe(schema *cfgx.Schema, v *viper.Viper) (*ServeConfig, error) {",
		"func LoadMigrate(schema *cfgx.Schema, v *viper.Viper) (*MigrateConfig, error) {",
		"func (c *ServeConfig) Db() *DbConfig { return &c.db }",
	)
}

func TestNestedMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "nested.go").Entries...)
	mustContain(t, out,
		"type ServerConfig struct {", "type TLSConfig struct {", "type FilesConfig struct {",
		`cfg.server.tls.files.cert = v.GetString("server.tls.files.cert")`,
		`v.SetDefault("server.timeout", 30*time.Second)`,
		`v.SetDefault("server.load_factor", 1.0)`,
		`v.SetDefault("server.origins", []string{"a", "b"})`,
		"func (c *TLSConfig) Files() *FilesConfig { return &c.files }",
	)
}

func TestSensitiveMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "sensitive.go").Entries...)
	mustContain(t, out,
		"apiKey        sensitiveString",
		"func (c *Config) APIKey() string { return c.apiKey.value() }",
		`cfg.apiKey = newSensitiveString(v.GetString("api_key"))`,
		`func (s sensitiveString) String() string               { return "[redacted]" }`,
		"MarshalJSON() ([]byte, error) { return []byte(`\"[redacted]\"`), nil }",
	)
	out = gen(t, ir.EntryIR{ID: "password", Type: ir.TypeString, FlagLong: "--password", Env: "PW", Default: "hunter2", Sensitive: true})
	mustContain(t, out, `flags.StringP("password", "", "", "")`)
	if strings.Count(out, "hunter2") != 1 { // only in SetDefault
		t.Errorf("sensitive default shown outside SetDefault:\n%s", out)
	}
}

func TestComputedMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "computed.go").Entries...)
	mustContain(t, out,
		"dsn  func() string",
		"func (c *DbConfig) DSN() string { return c.dsn() }",
		`cfg.db.dsn = func() string { return cfgxCompute[string](schema, "db.dsn", &cfg.db) }`,
		`cfg.workers = func() int { return cfgxCompute[int](schema, "workers", cfg) }`,
	)
	// Unknown or unsupported result types fall back to any.
	for _, rt := range []string{"", "mypkg.DSN", "map[string]int"} {
		out := gen(t, ir.EntryIR{ID: "x", Type: ir.TypeComputed, HasGetter: true, ResultType: rt})
		mustContain(t, out, "func (c *Config) X() any { return c.x() }")
	}
}

func TestGetterMatrix(t *testing.T) {
	out := gen(t,
		ir.EntryIR{ID: "cert", Type: ir.TypeString, Env: "CERT", HasGetter: true},
		ir.EntryIR{ID: "hosts", Type: ir.TypeList, HasGetter: true, ElementType: &ir.EntryIR{ID: "h", Type: ir.TypeInt}},
	)
	mustContain(t, out,
		"val, err := cfgxGet(schema, \"cert\", v.GetString)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tcfg.cert = val",
		`val, err := cfgxGet(schema, "hosts", v.GetIntSlice)`,
		"schema.Getter(key)",
		`"fmt"`,
	)
}

func TestLiveMatrix(t *testing.T) {
	out := gen(t, parseFixture(t, "live.go").Entries...)
	mustContain(t, out,
		"debug        atomic.Value",
		"apiToken     *atomic.Value",
		"func (c *Config) Debug() bool {\n\tv, _ := c.debug.Load().(bool)\n\treturn v\n}",
		`cfg.debug.Store(v.GetBool("debug"))`,
		`cfgxWatch(w, live, configFile, "debug", "APP_DEBUG", &cfg.debug, v.GetBool)`,
		`cfgxWatch(w, live, configFile, "api-token", "", cfg.apiToken, v.GetString)`,
		`cfgxWatchComputed(w, live, "limits.summary", func() (any, error) { return schema.Compute("limits.summary", &cfg.limits) })`,
		"w := runtime.NewWatcher(0)",
		"w.SetReloader(v.ReadInConfig)",
		"w.Start(context.Background())",
		"cfg.cfgxWatcher = w",
		"func (c *Config) Close() {",
		"OnChange:   e.OnChange,",
		`"github.com/epistemer/cfgx/runtime"`,
	)
}

func TestDeprecatedMatrix(t *testing.T) {
	out := gen(t,
		ir.EntryIR{ID: "old", Type: ir.TypeString, FlagLong: "--old", Deprecated: "use --new"},
		ir.EntryIR{ID: "gone", Type: ir.TypeString, Env: "GONE", Deprecated: "no flag"},
	)
	mustContain(t, out, `_ = flags.MarkDeprecated("old", "use --new")`, "// Deprecated: use --new")
	mustNotContain(t, out, `MarkDeprecated("gone"`)
}

func TestModuleOption(t *testing.T) {
	s := parseFixture(t, "live.go")
	out, err := Generate(s, Options{Package: "cmd", Module: "example.org/fork/cfgxfork"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(out), `cfgx "example.org/fork/cfgxfork"`, `"example.org/fork/cfgxfork/runtime"`, "package cmd")
	mustNotContain(t, string(out), "// Source:")
}

func TestGenerateErrors(t *testing.T) {
	str := func(id string) ir.EntryIR { return ir.EntryIR{ID: id, Type: ir.TypeString} }
	tests := []struct {
		name    string
		entries []ir.EntryIR
		want    []string
	}{
		{"name clash", []ir.EntryIR{str("db-port"), str("db_port")}, []string{`"db-port"`, `"db_port"`, "DbPort"}},
		{"name clash in object", []ir.EntryIR{{ID: "db", Type: ir.TypeObject, Properties: []ir.EntryIR{str("a-b"), str("a_b")}}}, []string{`"a-b"`, `"a_b"`, `object "db"`}},
		{"list of object", []ir.EntryIR{{ID: "servers", Type: ir.TypeList, ElementType: &ir.EntryIR{ID: "s", Type: ir.TypeObject, Properties: []ir.EntryIR{str("h")}}}}, []string{`"servers"`, "object"}},
		{"list of list", []ir.EntryIR{{ID: "matrix", Type: ir.TypeList, ElementType: &ir.EntryIR{ID: "row", Type: ir.TypeList, ElementType: &ir.EntryIR{ID: "c", Type: ir.TypeInt}}}}, []string{`"matrix"`, "list"}},
		{"list of bool", []ir.EntryIR{{ID: "bits", Type: ir.TypeList, ElementType: &ir.EntryIR{ID: "b", Type: ir.TypeBool}}}, []string{`"bits"`}},
		{"invalid identifier", []ir.EntryIR{str("2fa")}, []string{`"2fa"`}},
		{"reserved name", []ir.EntryIR{str("close")}, []string{`"close"`, "Close"}},
		{"duplicate flag", []ir.EntryIR{{ID: "a", Type: ir.TypeString, FlagLong: "--x"}, {ID: "b", Type: ir.TypeString, FlagLong: "--x"}}, []string{`"a"`, `"b"`, "--x"}},
		{"bad default", []ir.EntryIR{{ID: "n", Type: ir.TypeInt, Default: "eight"}}, []string{"int default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := genErr(t, tt.entries...)
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
	if _, err := Generate(&ir.SchemaIR{Version: "2"}, Options{Package: "x"}); err == nil {
		t.Error("unsupported version: error = nil")
	}
	if _, err := Generate(&ir.SchemaIR{Version: ir.Version}, Options{Package: "not valid"}); err == nil {
		t.Error("invalid package: error = nil")
	}
}

func TestObjectNameCollision(t *testing.T) {
	obj := func(id string, props ...ir.EntryIR) ir.EntryIR {
		return ir.EntryIR{ID: id, Type: ir.TypeObject, Properties: props}
	}
	host := ir.EntryIR{ID: "host", Type: ir.TypeString}
	out := gen(t, obj("server", obj("tls", host)), obj("client", obj("tls", host)))
	mustContain(t, out, "type ServerTLSConfig struct", "type ClientTLSConfig struct", "type ServerConfig struct")
}

func TestLiterals(t *testing.T) {
	tests := []struct {
		typ  string
		v    any
		want string
	}{
		{"string", "a\"b", `"a\"b"`},
		{"int", 8080, "8080"},
		{"int", float64(3), "3"},
		{"bool", true, "true"},
		{"float64", 0.5, "0.5"},
		{"float64", 1, "1.0"},
		{"float64", 1e21, "1e+21"},
		{"time.Duration", "30s", "30 * time.Second"},
		{"time.Duration", "1h0m0s", "time.Hour"},
		{"time.Duration", "1.5s", "1500 * time.Millisecond"},
		{"time.Duration", "0s", "0"},
		{"time.Duration", "3ns", "time.Duration(3)"},
		{"[]string", []any{"a", "b"}, `[]string{"a", "b"}`},
		{"[]int", []any{1, 2}, "[]int{1, 2}"},
		{"[]string", nil, "nil"},
	}
	for _, tt := range tests {
		got, err := literal(tt.typ, tt.v)
		if err != nil || got != tt.want {
			t.Errorf("literal(%s, %#v) = %q, %v; want %q", tt.typ, tt.v, got, err, tt.want)
		}
	}
}
