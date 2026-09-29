package cfgx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/cfgx/internal/ir"
)

// mapSource is a ValueSource backed by a flat map of dot-separated keys.
type mapSource map[string]any

func (m mapSource) Get(key string) any { return m[key] }

func (m mapSource) IsSet(key string) bool {
	_, ok := m[key]
	return ok
}

// docExample mirrors the example in doc.go.
func docExample() *Schema {
	return Define(
		Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080),
		Object("db",
			String("host").Env("DB_HOST").Default("localhost"),
			String("password").Env("DB_PASSWORD").Sensitive().Required(),
		),
	)
}

func TestDocExample(t *testing.T) {
	got := docExample().ToIR()
	if got.Version != "1" {
		t.Errorf("Version = %q, want \"1\"", got.Version)
	}
	if len(got.Entries) != 2 || got.Entries[0].ID != "port" || got.Entries[1].ID != "db" {
		t.Fatalf("entries = %+v, want port and db", got.Entries)
	}
	props := got.Entries[1].Properties
	if len(props) != 2 || props[0].ID != "host" || props[1].ID != "password" {
		t.Errorf("db properties = %+v, want host, password", props)
	}
}

func TestDefineDuplicateIDPanics(t *testing.T) {
	tests := []struct {
		name  string
		items []Item
		want  string
	}{
		{"root", []Item{String("a"), Int("a")}, `duplicate entry ID "a" in schema root`},
		{"nested", []Item{Object("db", String("host"), String("host"))}, `duplicate entry ID "host" in object "db"`},
		{"deep", []Item{Object("a", Object("b", Int("x"), Bool("x")))}, `duplicate entry ID "x" in object "a.b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic")
				}
				if msg := fmt.Sprint(r); !strings.Contains(msg, tt.want) {
					t.Errorf("panic = %q, want it to contain %q", msg, tt.want)
				}
			}()
			Define(tt.items...)
		})
	}
}

func TestDefineSameIDDifferentParents(t *testing.T) {
	s := Define(String("host"), Object("db", String("host")))
	if n := len(s.ToIR().Entries); n != 2 {
		t.Errorf("entries = %d, want 2", n)
	}
}

func TestToIRJSONRoundTrip(t *testing.T) {
	s := Define(
		Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080).Desc("HTTP port").
			Validate(PortRange(1024, 65535)).Scope("serve").Tags("server"),
		Duration("timeout").Default(30*time.Second).IsLive().OnChange(func(_, _ time.Duration) {}),
		Float64("ratio").Default(0.25),
		Bool("debug").Deprecated("use --log-level"),
		List("hosts", String("host")).Default([]string{"a", "b"}),
		Object("db",
			String("host").Env("DB_HOST").Default("localhost"),
			String("password").Env("DB_PASSWORD").Required().Sensitive(),
			String("driver").DependsOn("mode", "sql"),
			Computed("dsn", func(any) string { return "" }),
		).Scope("serve", "migrate"),
		String("json-output"),
		String("text-output"),
		MutuallyExclusive("json-output", "text-output"),
	)
	first, err := json.Marshal(s.ToIR())
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
		t.Errorf("round-trip mismatch:\n%s\n%s", first, second)
	}
	if back.Version != "1" {
		t.Errorf("version = %q, want \"1\"", back.Version)
	}
	if !bytes.Contains(first, []byte(`"version":"1"`)) {
		t.Errorf("JSON lacks version: %s", first)
	}
}

func TestMutuallyExclusiveIR(t *testing.T) {
	s := Define(String("a"), String("b"), MutuallyExclusive("a", "b"))
	want := []ir.ConstraintIR{{Type: ir.ConstraintMutuallyExclusive, EntryIDs: []string{"a", "b"}}}
	if got := s.ToIR().Constraints; !reflect.DeepEqual(got, want) {
		t.Errorf("Constraints = %+v, want %+v", got, want)
	}
}

func TestMutuallyExclusiveNeedsTwoIDs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MutuallyExclusive("a")
}

func TestValidate(t *testing.T) {
	failPw := func(string) error { return errors.New("too short") }
	tests := []struct {
		name   string
		schema *Schema
		src    mapSource
		want   []FieldError // nil means Validate must return nil
	}{
		{
			name:   "all pass",
			schema: docExample(),
			src:    mapSource{"port": "8080", "db.host": "h", "db.password": "secret"},
		},
		{
			name:   "failing validator reports dot path",
			schema: Define(Object("db", String("password").Validate(failPw))),
			src:    mapSource{"db.password": "x"},
			want:   []FieldError{{Field: "db.password", Message: "too short"}},
		},
		{
			name:   "required missing",
			schema: docExample(),
			src:    mapSource{"port": 8080},
			want:   []FieldError{{Field: "db.password", Message: "required"}},
		},
		{
			name:   "required zero value",
			schema: Define(String("name").Required()),
			src:    mapSource{"name": ""},
			want:   []FieldError{{Field: "name", Message: "required"}},
		},
		{
			name: "depends on inactive skips required",
			schema: Define(
				String("email"),
				Object("smtp", String("host").Required().DependsOn("email", "smtp")),
			),
			src: mapSource{"email": "none"},
		},
		{
			name: "depends on active enforces required",
			schema: Define(
				String("email"),
				Object("smtp", String("host").Required().DependsOn("email", "smtp")),
			),
			src:  mapSource{"email": "smtp"},
			want: []FieldError{{Field: "smtp.host", Message: "required"}},
		},
		{
			name: "depends on sibling with converted bool",
			schema: Define(Object("tls",
				Bool("enabled"),
				String("cert").Required().DependsOn("enabled", true),
			)),
			src:  mapSource{"tls.enabled": "true"},
			want: []FieldError{{Field: "tls.cert", Message: "required"}},
		},
		{
			name: "depends on gates whole object",
			schema: Define(
				Bool("tls"),
				Object("cert", String("path").Required()).DependsOn("tls", true),
			),
			src: mapSource{"tls": false},
		},
		{
			name:   "mutually exclusive both set",
			schema: Define(String("a"), String("b"), MutuallyExclusive("a", "b")),
			src:    mapSource{"a": "x", "b": "y"},
			want:   []FieldError{{Field: "a, b", Message: "mutually exclusive: only one of a, b may be set"}},
		},
		{
			name:   "mutually exclusive one zero",
			schema: Define(Int("a"), Int("b"), MutuallyExclusive("a", "b")),
			src:    mapSource{"a": "1", "b": "0"},
		},
		{
			name:   "bad conversion skips validators",
			schema: Define(Int("port").Validate(func(int) error { return errors.New("never") })),
			src:    mapSource{"port": "abc"},
			want:   []FieldError{{Field: "port", Message: `invalid int value: "abc" is not an integer`}},
		},
		{
			name:   "unset optional skips validators",
			schema: Define(String("email").Validate(Email())),
			src:    mapSource{},
		},
		{
			name: "validators run in declaration order and all errors collected",
			schema: Define(
				String("a").Validate(func(string) error { return errors.New("first") }).
					Validate(func(string) error { return errors.New("second") }),
				Int("b").Required(),
				Duration("c").Validate(func(d time.Duration) error {
					if d < time.Minute {
						return errors.New("too short")
					}
					return nil
				}),
				Float64("d").Validate(Range(0, 1)),
			),
			src: mapSource{"a": "x", "c": "30s", "d": "2"},
			want: []FieldError{
				{Field: "a", Message: "first"},
				{Field: "a", Message: "second"},
				{Field: "b", Message: "required"},
				{Field: "c", Message: "too short"},
				{Field: "d", Message: "2 is out of range [0, 1]"},
			},
		},
		{
			name:   "getter supplies value",
			schema: Define(String("cert").Required().Getter(func() (string, error) { return "", errors.New("vault down") })),
			src:    mapSource{},
			want:   []FieldError{{Field: "cert", Message: "getter failed: vault down"}},
		},
		{
			name: "getter value passes validator, source ignored",
			schema: Define(String("cert").Required().Validate(Regex(`^PEM`)).
				Getter(func() (string, error) { return "PEM-data", nil })),
			src: mapSource{"cert": "ignored"},
		},
		{
			name: "getter value fails validator",
			schema: Define(Int("port").Validate(Port()).
				Getter(func() (int, error) { return 70000, nil })),
			src:  mapSource{},
			want: []FieldError{{Field: "port", Message: "port 70000 is out of range [1, 65535]"}},
		},
		{
			name:   "range rejects NaN from source",
			schema: Define(Float64("ratio").Validate(Range(0, 1))),
			src:    mapSource{"ratio": "NaN"},
			want:   []FieldError{{Field: "ratio", Message: "NaN is out of range [0, 1]"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.schema.Validate(tt.src)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var ve *ValidationErrors
			if !errors.As(err, &ve) {
				t.Fatalf("Validate() = %v (%T), want *ValidationErrors", err, err)
			}
			if !reflect.DeepEqual(ve.Errors, tt.want) {
				t.Errorf("errors = %+v, want %+v", ve.Errors, tt.want)
			}
		})
	}
}

func TestValidateNilReturnsUntypedNil(t *testing.T) {
	err := Define(String("a")).Validate(mapSource{"a": "x"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationErrorsFormatting(t *testing.T) {
	one := &ValidationErrors{Errors: []FieldError{{Field: "a", Message: "bad"}}}
	if got, want := one.Error(), "1 config validation error: a: bad"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	two := &ValidationErrors{Errors: []FieldError{{Field: "a", Message: "bad"}, {Field: "db.b", Message: "worse"}}}
	if got, want := two.Error(), "2 config validation errors: a: bad; db.b: worse"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	var fe FieldError
	if !errors.As(two, &fe) || fe.Field != "a" {
		t.Errorf("errors.As FieldError = %+v", fe)
	}
	if !errors.Is(two, FieldError{Field: "db.b", Message: "worse"}) {
		t.Error("errors.Is did not find second FieldError")
	}
}

func TestWatcherEntries(t *testing.T) {
	var (
		gotOld, gotNew   bool
		dsnOld, dsnNew   string
		dsnCalled        bool
		listOld, listNew []any
	)
	s := Define(
		Bool("debug").Env("DEBUG").IsLive().OnChange(func(o, n bool) { gotOld, gotNew = o, n }),
		String("static"),
		Object("db", String("host").IsLive().Getter(func() (string, error) { return "h", nil })),
		Computed("dsn", func(any) string { return "d" }).IsLive().
			OnChange(func(o, n string) { dsnOld, dsnNew, dsnCalled = o, n, true }),
		List("hosts", String("host")).IsLive().OnChange(func(o, n []any) { listOld, listNew = o, n }),
	)
	ws := s.WatcherEntries()
	if len(ws) != 4 {
		t.Fatalf("WatcherEntries() len = %d, want 4: %+v", len(ws), ws)
	}
	if ws[0].Key != "debug" || ws[0].Env != "DEBUG" || ws[0].Type != ir.TypeBool {
		t.Errorf("ws[0] = %+v", ws[0])
	}
	ws[0].OnChange("false", true)
	if gotOld || !gotNew {
		t.Errorf("OnChange got (%v, %v), want (false, true)", gotOld, gotNew)
	}
	if ws[1].Key != "db.host" || ws[1].Getter == nil {
		t.Errorf("ws[1] = %+v", ws[1])
	} else if v, err := ws[1].Getter(); err != nil || v != "h" {
		t.Errorf("Getter() = %v, %v", v, err)
	}
	if ws[2].Key != "dsn" || ws[2].Compute == nil || ws[2].Compute(nil) != "d" {
		t.Errorf("ws[2] = %+v", ws[2])
	}
	// A nil old value (no previous computation) is treated as the zero value.
	ws[2].OnChange(nil, "new-dsn")
	if !dsnCalled || dsnOld != "" || dsnNew != "new-dsn" {
		t.Errorf("computed OnChange got (%q, %q, called=%v), want (\"\", \"new-dsn\", true)", dsnOld, dsnNew, dsnCalled)
	}
	if ws[3].Key != "hosts" || ws[3].Type != ir.TypeList || ws[3].OnChange == nil {
		t.Fatalf("ws[3] = %+v", ws[3])
	}
	ws[3].OnChange(nil, "a,b")
	if listOld != nil || !reflect.DeepEqual(listNew, []any{"a", "b"}) {
		t.Errorf("list OnChange got (%#v, %#v), want (nil, [a b])", listOld, listNew)
	}
}

func TestSensitiveMessagesRedacted(t *testing.T) {
	const secret = "Hunter2Secret"
	tests := []struct {
		name   string
		schema *Schema
		src    mapSource
		want   []FieldError
	}{
		{
			name:   "validator error",
			schema: Define(String("pw").Sensitive().Validate(Regex(`^[a-z]+$`))),
			src:    mapSource{"pw": secret},
			want:   []FieldError{{Field: "pw", Message: `[redacted] does not match pattern "^[a-z]+$"`}},
		},
		{
			name:   "unquoted value in custom validator",
			schema: Define(String("pw").Sensitive().Validate(func(v string) error { return fmt.Errorf("bad %s!", v) })),
			src:    mapSource{"pw": secret},
			want:   []FieldError{{Field: "pw", Message: "bad [redacted]!"}},
		},
		{
			name:   "conversion error",
			schema: Define(Int("pin").Sensitive()),
			src:    mapSource{"pin": secret},
			want:   []FieldError{{Field: "pin", Message: "invalid int value [redacted]"}},
		},
		{
			name:   "list element conversion",
			schema: Define(List("pins", Int("pin")).Sensitive()),
			src:    mapSource{"pins": []any{1, secret}},
			want:   []FieldError{{Field: "pins[1]", Message: "invalid element value [redacted]"}},
		},
		{
			name:   "list element validator",
			schema: Define(List("keys", String("k").Validate(Enum("a"))).Sensitive()),
			src:    mapSource{"keys": []any{"a", secret}},
			want:   []FieldError{{Field: "keys[1]", Message: "[redacted] is not one of [a]"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ve *ValidationErrors
			if err := tt.schema.Validate(tt.src); !errors.As(err, &ve) {
				t.Fatalf("Validate() = %v, want *ValidationErrors", err)
			}
			if !reflect.DeepEqual(ve.Errors, tt.want) {
				t.Errorf("errors = %+v, want %+v", ve.Errors, tt.want)
			}
			if strings.Contains(ve.Error(), secret) {
				t.Errorf("error leaks secret: %s", ve.Error())
			}
		})
	}
}

func TestSchemaGetter(t *testing.T) {
	s := Define(
		String("plain"),
		String("token").Getter(func() (string, error) { return "tok", nil }),
		List("hosts", String("host")).Getter(func() ([]any, error) { return []any{"a"}, nil }),
		Object("db", Int("port").Getter(func() (int, error) { return 5432, nil })),
		Computed("dsn", func(any) string { return "d" }),
	)
	for _, key := range []string{"plain", "missing", "db", "db.missing", "plain.x", "dsn"} {
		if g := s.Getter(key); g != nil {
			t.Errorf("Getter(%q) = non-nil, want nil", key)
		}
	}
	tests := []struct {
		key  string
		want any
	}{
		{"token", "tok"},
		{"hosts", []any{"a"}},
		{"db.port", 5432},
	}
	for _, tt := range tests {
		g := s.Getter(tt.key)
		if g == nil {
			t.Fatalf("Getter(%q) = nil", tt.key)
		}
		got, err := g()
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Getter(%q)() = %#v, %v; want %#v", tt.key, got, err, tt.want)
		}
	}
}

func TestSchemaCompute(t *testing.T) {
	type parent struct{ host string }
	s := Define(
		String("host"),
		Object("db", Computed("dsn", func(p any) string { return "pg://" + p.(*parent).host })),
		Computed("any", func(p any) any { return p }),
	)
	got, err := s.Compute("db.dsn", &parent{host: "h"})
	if err != nil || got != "pg://h" {
		t.Errorf("Compute(db.dsn) = %#v, %v; want \"pg://h\"", got, err)
	}
	if got, err := s.Compute("any", 7); err != nil || got != 7 {
		t.Errorf("Compute(any) = %#v, %v; want 7", got, err)
	}
	for _, key := range []string{"host", "db", "missing", "db.missing"} {
		if _, err := s.Compute(key, nil); err == nil {
			t.Errorf("Compute(%q) error = nil, want non-nil", key)
		}
	}
}

func TestValidateScope(t *testing.T) {
	s := Define(
		String("name").Required(),
		Int("port").Required().Scope("serve"),
		Object("db",
			String("host").Required(),
			String("replica").Required().Scope("serve"),
		).Scope("serve", "migrate"),
		Object("cache",
			String("url").Required(),
		).Scope("worker"),
		Bool("json").Scope("serve"),
		Bool("text"),
		Bool("a"),
		Bool("b"),
		MutuallyExclusive("json", "text"),
		MutuallyExclusive("a", "b"),
	)
	src := mapSource{"json": true, "text": true, "a": true, "b": true}
	fields := func(err error) []string {
		t.Helper()
		if err == nil {
			return nil
		}
		var ve *ValidationErrors
		if !errors.As(err, &ve) {
			t.Fatalf("error %v is not *ValidationErrors", err)
		}
		var out []string
		for _, fe := range ve.Errors {
			out = append(out, fe.Field)
		}
		return out
	}
	tests := []struct {
		scope string
		want  []string
	}{
		{"", []string{"name", "a, b"}},
		{"serve", []string{"name", "port", "db.host", "db.replica", "json, text", "a, b"}},
		{"migrate", []string{"name", "db.host", "a, b"}},
		{"worker", []string{"name", "cache.url", "a, b"}},
		{"other", []string{"name", "a, b"}},
	}
	for _, tt := range tests {
		t.Run("scope="+tt.scope, func(t *testing.T) {
			got := fields(s.ValidateScope(src, tt.scope))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ValidateScope(%q) fields = %q, want %q", tt.scope, got, tt.want)
			}
		})
	}
	t.Run("Validate checks everything", func(t *testing.T) {
		want := []string{"name", "port", "db.host", "db.replica", "cache.url", "json, text", "a, b"}
		if got := fields(s.Validate(src)); !reflect.DeepEqual(got, want) {
			t.Errorf("Validate fields = %q, want %q", got, want)
		}
	})
	t.Run("valid", func(t *testing.T) {
		ok := mapSource{"name": "x", "db.host": "h"}
		if err := s.ValidateScope(ok, "migrate"); err != nil {
			t.Errorf("ValidateScope(migrate) = %v, want nil", err)
		}
		if err := s.ValidateScope(ok, "serve"); err == nil {
			t.Error("ValidateScope(serve) = nil, want port and db.replica errors")
		}
	})
	t.Run("nil source", func(t *testing.T) {
		if err := s.ValidateScope(nil, "serve"); err == nil {
			t.Error("ValidateScope(nil) = nil, want error")
		}
	})
}
