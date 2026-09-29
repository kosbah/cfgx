package cfgx

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/cfgx/internal/ir"
)

func TestBuilderMethodsSetIR(t *testing.T) {
	noopStr := func(string) error { return nil }
	tests := []struct {
		name  string
		entry Entry
		want  ir.EntryIR
	}{
		{"flag long and short", Int("port").Flag("--port", "-p"),
			ir.EntryIR{ID: "port", Type: ir.TypeInt, FlagLong: "--port", FlagShort: "-p"}},
		{"flag long only", String("name").Flag("--name"),
			ir.EntryIR{ID: "name", Type: ir.TypeString, FlagLong: "--name"}},
		{"env", String("host").Env("DB_HOST"),
			ir.EntryIR{ID: "host", Type: ir.TypeString, Env: "DB_HOST"}},
		{"default string", String("host").Default("localhost"),
			ir.EntryIR{ID: "host", Type: ir.TypeString, Default: "localhost"}},
		{"default int", Int("port").Default(8080),
			ir.EntryIR{ID: "port", Type: ir.TypeInt, Default: 8080}},
		{"default zero int kept", Int("n").Default(0),
			ir.EntryIR{ID: "n", Type: ir.TypeInt, Default: 0}},
		{"default bool", Bool("debug").Default(true),
			ir.EntryIR{ID: "debug", Type: ir.TypeBool, Default: true}},
		{"default float", Float64("ratio").Default(0.5),
			ir.EntryIR{ID: "ratio", Type: ir.TypeFloat64, Default: 0.5}},
		{"default duration as string", Duration("t").Default(30 * time.Second),
			ir.EntryIR{ID: "t", Type: ir.TypeDuration, Default: "30s"}},
		{"required", String("name").Required(),
			ir.EntryIR{ID: "name", Type: ir.TypeString, Required: true}},
		{"sensitive", String("pw").Sensitive(),
			ir.EntryIR{ID: "pw", Type: ir.TypeString, Sensitive: true}},
		{"desc", Int("port").Desc("HTTP port"),
			ir.EntryIR{ID: "port", Type: ir.TypeInt, Description: "HTTP port"}},
		{"validate custom", String("s").Validate(noopStr),
			ir.EntryIR{ID: "s", Type: ir.TypeString, HasValidate: true}},
		{"validate builtin", String("email").Validate(Email()),
			ir.EntryIR{ID: "email", Type: ir.TypeString, HasValidate: true}},
		{"is live", Bool("debug").IsLive(),
			ir.EntryIR{ID: "debug", Type: ir.TypeBool, IsLive: true}},
		{"on change", Bool("debug").OnChange(func(_, _ bool) {}),
			ir.EntryIR{ID: "debug", Type: ir.TypeBool, HasOnChange: true}},
		{"getter", String("cert").Getter(func() (string, error) { return "", nil }),
			ir.EntryIR{ID: "cert", Type: ir.TypeString, HasGetter: true}},
		{"deprecated", String("old").Deprecated("use new"),
			ir.EntryIR{ID: "old", Type: ir.TypeString, Deprecated: "use new"}},
		{"scope", String("s").Scope("serve", "migrate"),
			ir.EntryIR{ID: "s", Type: ir.TypeString, Scope: []string{"serve", "migrate"}}},
		{"tags", String("s").Tags("email", "infra"),
			ir.EntryIR{ID: "s", Type: ir.TypeString, Tags: []string{"email", "infra"}}},
		{"depends on", String("cert").DependsOn("tls", true),
			ir.EntryIR{ID: "cert", Type: ir.TypeString, DependsOn: &ir.DependsOnIR{ID: "tls", Value: true}}},
		{"list", List("hosts", String("host")).Env("HOSTS").Default([]string{"a"}).Required().
			Validate(func([]any) error { return nil }),
			ir.EntryIR{ID: "hosts", Type: ir.TypeList, Env: "HOSTS", Default: []any{"a"}, Required: true,
				HasValidate: true, ElementType: &ir.EntryIR{ID: "host", Type: ir.TypeString}}},
		{"object", Object("db", String("host")).Desc("database").Scope("serve").Tags("db"),
			ir.EntryIR{ID: "db", Type: ir.TypeObject, Description: "database", Scope: []string{"serve"},
				Tags: []string{"db"}, Properties: []ir.EntryIR{{ID: "host", Type: ir.TypeString}}}},
		{"computed", Computed("dsn", func(any) string { return "" }).Desc("dsn").IsLive(),
			ir.EntryIR{ID: "dsn", Type: ir.TypeComputed, Description: "dsn", IsLive: true, HasGetter: true, ResultType: "string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.entry.toIR()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toIR() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestBuilderChainingReturnsReceiver(t *testing.T) {
	s := String("s")
	strSteps := []func() *StringEntry{
		func() *StringEntry { return s.Flag("--s") },
		func() *StringEntry { return s.Env("S") },
		func() *StringEntry { return s.Default("x") },
		func() *StringEntry { return s.Required() },
		func() *StringEntry { return s.Sensitive() },
		func() *StringEntry { return s.Desc("d") },
		func() *StringEntry { return s.Validate(Email()) },
		func() *StringEntry { return s.IsLive() },
		func() *StringEntry { return s.OnChange(func(_, _ string) {}) },
		func() *StringEntry { return s.Getter(func() (string, error) { return "", nil }) },
		func() *StringEntry { return s.Deprecated("m") },
		func() *StringEntry { return s.Scope("serve") },
		func() *StringEntry { return s.Tags("t") },
		func() *StringEntry { return s.DependsOn("x", 1) },
	}
	for i, step := range strSteps {
		if got := step(); got != s {
			t.Errorf("string step %d returned a different receiver", i)
		}
	}

	l := List("l", Int("n"))
	listSteps := []*ListEntry{
		l.Flag("--l"), l.Env("L"), l.Default([]int{1}), l.Required(), l.Sensitive(), l.Desc("d"),
		l.Validate(func([]any) error { return nil }), l.IsLive(), l.OnChange(func(_, _ []any) {}),
		l.Getter(func() ([]any, error) { return nil, nil }), l.Deprecated("m"), l.Scope("a"),
		l.Tags("t"), l.DependsOn("x", 1),
	}
	for i, got := range listSteps {
		if got != l {
			t.Errorf("list step %d returned a different receiver", i)
		}
	}

	o := Object("o", String("a"))
	for i, got := range []*ObjectEntry{o.Desc("d"), o.Deprecated("m"), o.Scope("a"), o.Tags("t"), o.DependsOn("x", 1)} {
		if got != o {
			t.Errorf("object step %d returned a different receiver", i)
		}
	}

	c := Computed("c", func(any) int { return 1 })
	for i, got := range []*ComputedEntry[int]{c.Desc("d"), c.Tags("t"), c.Scope("a"), c.IsLive(), c.OnChange(func(_, _ int) {})} {
		if got != c {
			t.Errorf("computed step %d returned a different receiver", i)
		}
	}

	n := Int("n")
	if n.Default(1).Validate(Port()).Flag("--n", "-n") != n {
		t.Error("int chain returned a different receiver")
	}
}

func TestFlagTooManyShortsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Int("port").Flag("--port", "-p", "-q")
}

func TestIntDefaultIsJSONNumber(t *testing.T) {
	e := Int("port").Default(8080).toIR()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back ir.EntryIR
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if got, ok := back.Default.(float64); !ok || got != 8080 {
		t.Errorf("Default after round-trip = %#v, want float64(8080)", back.Default)
	}
}

func TestDurationDefaultIsJSONString(t *testing.T) {
	data, err := json.Marshal(Duration("t").Default(30 * time.Second).toIR())
	if err != nil {
		t.Fatal(err)
	}
	var back ir.EntryIR
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Default != "30s" {
		t.Errorf("Default = %#v, want \"30s\"", back.Default)
	}
}

func TestComputedIR(t *testing.T) {
	e := Computed("dsn", func(any) string { return "x" }).toIR()
	if !e.HasGetter {
		t.Error("HasGetter = false, want true")
	}
	if e.FlagLong != "" || e.FlagShort != "" || e.Env != "" {
		t.Errorf("computed has flag/env: %+v", e)
	}
	if e.Default != nil {
		t.Errorf("computed Default = %v, want nil", e.Default)
	}
}

func TestComputedResultType(t *testing.T) {
	type named struct{}
	tests := []struct {
		entry Entry
		want  string
	}{
		{Computed("a", func(any) string { return "" }), "string"},
		{Computed("b", func(any) int { return 0 }), "int"},
		{Computed("c", func(any) time.Duration { return 0 }), "time.Duration"},
		{Computed("d", func(any) []string { return nil }), "[]string"},
		{Computed("e", func(any) any { return nil }), "any"},
		{Computed("f", func(any) named { return named{} }), "cfgx.named"},
		{Computed("g", func(any) error { return nil }), "error"},
	}
	for _, tt := range tests {
		if got := tt.entry.toIR().ResultType; got != tt.want {
			t.Errorf("%s: ResultType = %q, want %q", tt.entry.id(), got, tt.want)
		}
	}
}

func TestNestedObjectIR(t *testing.T) {
	e := Object("db", String("host")).toIR()
	if len(e.Properties) != 1 || e.Properties[0].ID != "host" {
		t.Fatalf("Properties = %+v, want one entry with ID host", e.Properties)
	}
}

func TestListElementValidation(t *testing.T) {
	s := Define(List("ports", Int("port").Validate(Port())).Required())
	err := s.Validate(mapSource{"ports": []any{80, "70000", "x"}})
	var ve *ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationErrors", err)
	}
	if len(ve.Errors) != 1 || ve.Errors[0].Field != "ports[2]" {
		t.Errorf("errors = %+v, want one conversion error at ports[2]", ve.Errors)
	}

	err = s.Validate(mapSource{"ports": "80, 70000"})
	if !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Field != "ports[1]" {
		t.Errorf("err = %v, want one validator error at ports[1]", err)
	}

	err = s.Validate(mapSource{})
	if !errors.As(err, &ve) || ve.Errors[0].Message != "required" {
		t.Errorf("err = %v, want required", err)
	}

	if err := s.Validate(mapSource{"ports": []int{80, 443}}); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestListValidatorsSeeConvertedElements(t *testing.T) {
	var got []any
	calls := 0
	s := Define(List("ports", Int("p")).Validate(func(v []any) error {
		calls++
		got = v
		return nil
	}))
	if err := s.Validate(mapSource{"ports": "80,443"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(got, []any{80, 443}) {
		t.Errorf("validator saw %#v, want []any{80, 443} (ints)", got)
	}

	calls = 0
	var ve *ValidationErrors
	err := s.Validate(mapSource{"ports": "80,abc"})
	if !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Field != "ports[1]" {
		t.Errorf("err = %v, want one conversion error at ports[1]", err)
	}
	if calls != 0 {
		t.Errorf("list validator called %d times after conversion failure, want 0", calls)
	}
}

func TestListDurationDefaultIsJSONStrings(t *testing.T) {
	data, err := json.Marshal(List("d", Duration("x")).Default([]time.Duration{time.Second}).toIR())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"default":["1s"]`) {
		t.Errorf("JSON = %s, want default [\"1s\"]", data)
	}
}

func TestEntryIDWithReservedCharsPanics(t *testing.T) {
	for _, id := range []string{"db.host", "a[0]", "x]"} {
		t.Run(id, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic")
				}
				if msg := fmt.Sprint(r); !strings.Contains(msg, id) {
					t.Errorf("panic = %q, want it to name %q", msg, id)
				}
			}()
			String(id)
		})
	}
}

func TestListDefaultRejectsNonSlice(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	List("l", String("s")).Default("nope")
}
