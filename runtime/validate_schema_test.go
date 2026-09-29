package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/example/cfgx"
	"github.com/example/cfgx/gen/parser"
)

func fieldErrors(t *testing.T, err error) []cfgx.FieldError {
	t.Helper()
	if err == nil {
		return nil
	}
	var ve *cfgx.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is %T, want *cfgx.ValidationErrors", err, err)
	}
	return ve.Errors
}

func fields(errs []cfgx.FieldError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Field
	}
	return out
}

func TestValidateSchema_Full(t *testing.T) {
	s, err := parser.Parse(filepath.Join("..", "testdata", "schemas", "full.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(s, validFull); err != nil {
		t.Fatalf("ValidateSchema(valid) = %v, want nil", err)
	}

	errs := fieldErrors(t, ValidateSchema(s, mapSource{}))
	want := []string{"signing_key", "db.password"}
	if !reflect.DeepEqual(fields(errs), want) {
		t.Fatalf("fields = %v, want %v", fields(errs), want)
	}
	for _, e := range errs {
		if e.Message != "required but not set" {
			t.Errorf("%s: message %q, want %q", e.Field, e.Message, "required but not set")
		}
	}

	bad := validFull.with("port", 70000).with("db.password", "").
		with("json_output", true).with("text_output", "true").with("signing_key", "!!")
	errs = fieldErrors(t, ValidateSchema(s, bad))
	want = []string{"port", "signing_key", "db.password", "json_output, text_output"}
	if !reflect.DeepEqual(fields(errs), want) {
		t.Fatalf("fields = %v, want %v (%v)", fields(errs), want, errs)
	}
	if strings.Contains(errs[1].Message, "!!") {
		t.Errorf("sensitive value leaked: %q", errs[1].Message)
	}
	if !strings.Contains(errs[3].Message, "mutually exclusive") {
		t.Errorf("constraint message = %q", errs[3].Message)
	}
}

func TestValidateSchema_Nil(t *testing.T) {
	s, err := parser.Parse(filepath.Join("..", "testdata", "schemas", "minimal.go"))
	if err != nil {
		t.Fatal(err)
	}
	if ValidateSchema(nil, mapSource{}) == nil || ValidateSchema(s, nil) == nil {
		t.Fatal("nil arguments did not fail")
	}
}

// TestValidateSchema_Parity checks that ValidateSchema reports the same fields,
// in the same order, as cfgx.Schema.Validate for a schema without closures.
func TestValidateSchema_Parity(t *testing.T) {
	schema := cfgx.Define(
		cfgx.Bool("tls"),
		cfgx.String("cert").Required().DependsOn("tls", true),
		cfgx.Int("port").Required().Validate(cfgx.PortRange(1024, 65535)),
		cfgx.List("hosts", cfgx.String("h").Validate(cfgx.URL())).Required(),
		cfgx.Object("smtp",
			cfgx.Bool("enabled"),
			cfgx.String("host").Required().DependsOn("enabled", true),
			cfgx.String("mode").Validate(cfgx.Enum("a", "b")),
		),
		cfgx.Object("mail",
			cfgx.String("user").Required(),
		).DependsOn("smtp.enabled", true),
		cfgx.Int("a"),
		cfgx.Int("b"),
		cfgx.MutuallyExclusive("a", "b"),
	)
	// The same schema as source, parsed so the IR carries the built-in
	// validators (Schema.ToIR only records that a Validate closure exists).
	src := `package p

import "github.com/example/cfgx"

var Schema = cfgx.Define(
	cfgx.Bool("tls"),
	cfgx.String("cert").Required().DependsOn("tls", true),
	cfgx.Int("port").Required().Validate(cfgx.PortRange(1024, 65535)),
	cfgx.List("hosts", cfgx.String("h").Validate(cfgx.URL())).Required(),
	cfgx.Object("smtp",
		cfgx.Bool("enabled"),
		cfgx.String("host").Required().DependsOn("enabled", true),
		cfgx.String("mode").Validate(cfgx.Enum("a", "b")),
	),
	cfgx.Object("mail",
		cfgx.String("user").Required(),
	).DependsOn("smtp.enabled", true),
	cfgx.Int("a"),
	cfgx.Int("b"),
	cfgx.MutuallyExclusive("a", "b"),
)
`
	file := filepath.Join(t.TempDir(), "schema.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := parser.Parse(file)
	if err != nil {
		t.Fatal(err)
	}

	sources := map[string]mapSource{
		"empty": {},
		"valid": {"port": 8080, "hosts": "http://x"},
		"gated": {
			"tls": true, "port": "80", "hosts": []any{"http://x", "nope"},
			"smtp.enabled": "true", "smtp.mode": "c", "a": 1, "b": 2,
		},
		"bad-type": {"port": "abc", "hosts": []any{}, "tls": "false", "cert": "x"},
		"zero":     {"port": 0, "tls": true, "cert": "", "a": 0, "b": 3},
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			want := fields(fieldErrors(t, schema.Validate(src)))
			got := fields(fieldErrors(t, ValidateSchema(s, src)))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ValidateSchema fields = %v, cfgx.Schema.Validate fields = %v", got, want)
			}
		})
	}
}
