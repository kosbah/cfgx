# Phase 1: Core DSL Package

## Goal

Implement the `cfgx` root package — the developer-facing DSL that application developers
import in their schema files. Also implement the `internal/ir` package that defines the
`SchemaIR` intermediate representation.

After this phase, a developer should be able to write a schema file and call `.ToIR()` to
get a `SchemaIR` value, and call `schema.Validate(v)` to run all validators.

---

## Prerequisites

- Go 1.21+ module at `github.com/example/cfgx`
- Package stubs (doc.go) already exist in: `cfgx/`, `runtime/`, `gen/parser/`,
  `gen/codegen/`, `gen/artifacts/`, `cmd/cfgx/`, `internal/ir/`
- Read `docs/design/config-anatomy.md` — the canonical property reference
- Read `docs/design/schema-ir-spec.md` — the SchemaIR type definitions and invariants

---

## Files to create

```
internal/ir/schema_ir.go           — SchemaIR and related types (copy from spec)
cfgx.go                            — package doc + Define() function
schema.go                          — Schema type, MutuallyExclusive()
entry.go                           — all entry types and builder chain
entry_test.go                      — unit tests for builder chain
schema_test.go                     — unit tests for Schema + ToIR() + Validate()
```

---

## Detailed specification

### `internal/ir/schema_ir.go`

Copy the type definitions verbatim from `docs/design/schema-ir-spec.md`.
Add `Version string` field to `SchemaIR` with value `"1"`.
All types must be JSON-marshallable (use `encoding/json`; no external marshal libraries).

---

### `cfgx.go`

```go
// Package cfgx provides a declarative, type-safe configuration layer for
// cobra/viper applications.
//
// Define your configs once in a schema file:
//
//	var Schema = cfgx.Define(
//	    cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080),
//	    cfgx.Object("db", cfgx.String("host").Env("DB_HOST").Default("localhost")),
//	)
//
// Then run `cfgx gen` to generate typed structs and all wiring code.
package cfgx

// Define constructs a Schema from the provided entries and constraints.
// Entries are evaluated in order; duplicate IDs at the same level are a panic.
func Define(entries ...Entry) *Schema
```

---

### `schema.go` — `Schema` type

```go
type Schema struct {
    entries     []Entry
    constraints []Constraint
}

// MutuallyExclusive returns a Constraint that errors in Load() if more than one
// of the named entries has a non-zero value.
func MutuallyExclusive(ids ...string) Constraint

// ToIR converts the Schema to its serializable intermediate representation.
// Closures (Validate, OnChange, Getter) are not included; they are noted as
// HasValidate/HasOnChange/HasGetter bool flags in EntryIR.
func (s *Schema) ToIR() *ir.SchemaIR

// Validate runs all registered validator functions against values in v.
// Returns a *ValidationErrors if any validators fail; nil on success.
// Called by generated Load() functions.
func (s *Schema) Validate(v *viper.Viper) error

// WatcherEntries returns the list of IsLive entries suitable for the runtime Watcher.
// Called by generated Load() to set up the IsLive watcher.
func (s *Schema) WatcherEntries() []WatchEntry
```

```go
// ValidationErrors is a multi-error returned by Validate().
type ValidationErrors struct {
    Errors []FieldError
}

func (e *ValidationErrors) Error() string   // "2 config validation errors: ..."
func (e *ValidationErrors) Unwrap() []error // for errors.Is / errors.As

type FieldError struct {
    Field   string // dot-separated path, e.g. "db.password"
    Message string
}

func (e FieldError) Error() string
```

---

### `entry.go` — Entry types and builder chain

The builder chain uses generics for type-safe defaults and validators.

**Entry interface (unexported):**
```go
type Entry interface {
    id() string
    toIR(parentPath string) ir.EntryIR
}
```

**Typed entry structs (one per type):**

Each type (`stringEntry`, `intEntry`, `boolEntry`, `float64Entry`, `durationEntry`,
`listEntry`, `objectEntry`, `computedEntry`) carries all property fields.

**Constructor functions:**
```go
func String(id string) *StringEntry
func Int(id string) *IntEntry
func Bool(id string) *BoolEntry
func Float64(id string) *Float64Entry
func Duration(id string) *DurationEntry  // stores as time.Duration
func List(id string, elemType Entry) *ListEntry
func Object(id string, properties ...Entry) *ObjectEntry
func Computed[T any](id string, fn func(parent any) T) *ComputedEntry[T]
```

**Builder methods on each typed entry** (return the receiver for chaining):

```go
func (e *StringEntry) Flag(long string, short ...string) *StringEntry
func (e *StringEntry) Env(name string) *StringEntry
func (e *StringEntry) Default(val string) *StringEntry       // generic: Default[T]
func (e *StringEntry) Required() *StringEntry
func (e *StringEntry) Sensitive() *StringEntry
func (e *StringEntry) Desc(text string) *StringEntry
func (e *StringEntry) Validate(fn func(string) error) *StringEntry
func (e *StringEntry) IsLive() *StringEntry
func (e *StringEntry) OnChange(fn func(old, new string)) *StringEntry
func (e *StringEntry) Getter(fn func() (string, error)) *StringEntry
func (e *StringEntry) Deprecated(message string) *StringEntry
func (e *StringEntry) Scope(commands ...string) *StringEntry
func (e *StringEntry) Tags(tags ...string) *StringEntry
func (e *StringEntry) DependsOn(id string, value any) *StringEntry
```

(Repeat analogously for Int, Bool, Float64, Duration, List, Object entries.)

**Built-in validator constructors** (return a typed validator function):

```go
func Email() func(string) error
func URL() func(string) error
func Port() func(int) error
func PortRange(min, max int) func(int) error
func Base64() func(string) error
func Regex(pattern string) func(string) error  // panics if pattern fails to compile
func DurationValidator() func(string) error    // parseable by time.ParseDuration
func Semver() func(string) error
func Enum(values ...string) func(string) error
func Range(min, max float64) func(float64) error
```

Note: These constructors are DSL conveniences. At runtime they are thin wrappers around
the validators in `cfgx/runtime`. The cfgx package may import cfgx/runtime, or it may
re-implement lightweight versions to avoid the circular dependency. Prefer importing runtime
unless it creates a circular dependency.

---

## Test requirements

### `entry_test.go`

Table-driven tests covering:
- All builder methods set the expected field (verify via `.toIR()`)
- Chaining returns the same receiver
- `Flag("--port", "-p")` sets both long and short correctly
- `Validate(cfgx.Email())` sets `HasValidate: true` in IR (not the function itself)
- `Default(8080)` on an IntEntry produces `IR.Default = float64(8080)` in JSON (JSON numbers)
- `Sensitive()` sets `IR.Sensitive = true`
- Nested `Object("db", String("host"))` produces `IR.Properties` with one element
- `Computed("dsn", fn)` produces `IR.HasGetter = true` and `IR.FlagLong == ""`

### `schema_test.go`

- `Define(...)` with duplicate IDs at the same level panics with a clear message
- `Schema.ToIR()` round-trips through `json.Marshal` / `json.Unmarshal` correctly
- `Schema.Validate()` with a validator that returns an error → `*ValidationErrors` with correct field path
- `Schema.Validate()` with no errors → `nil`
- `MutuallyExclusive("a", "b")` in `ToIR()` produces a `ConstraintIR`

---

## Definition of done

- `go test ./... -count=1` passes with no failures
- `go vet ./...` clean
- No external dependencies beyond standard library (the DSL package itself)
- All exported symbols have godoc comments
