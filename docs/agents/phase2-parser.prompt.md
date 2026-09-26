# Phase 2: Schema Parser

## Goal

Implement `gen/parser` — a package that reads a Go source file containing a
`cfgx.Define(...)` call and returns a `*ir.SchemaIR`.

The parser uses `go/ast` for static analysis only — no compilation, no `go run`.
It can only extract literal values; function literals are detected and noted as boolean
sentinels (`HasOnChange`, `HasGetter`, `HasValidate`).

---

## Prerequisites

- Phase 1 complete: `internal/ir/schema_ir.go` exists with all types
- Read `docs/design/schema-ir-spec.md` — invariants that the parser must guarantee
- Familiarity with `go/parser`, `go/ast`, `go/token`

---

## Files to create

```
gen/parser/parser.go          — Parse() function + AST walking logic
gen/parser/parser_test.go     — table-driven tests against testdata fixtures
testdata/schemas/              — schema fixture files (create these too)
    minimal.go                 — minimal schema (one int entry)
    full.go                    — full schema exercising all features
    nested.go                  — deeply nested object
    invalid_duplicate.go       — triggers duplicate-ID error
    invalid_depends.go         — references non-existent DependsOn ID
```

---

## Public API

```go
package parser

// Parse reads the Go source file at filename, locates the cfgx.Define(...)
// call, and returns its SchemaIR representation.
//
// Parse performs static analysis only — it does not compile or run the source.
// All literal values are extracted. Function literals are detected but not
// extracted; their presence is noted via HasOnChange / HasGetter / HasValidate flags.
//
// Parse returns a ParseError for any structural problem. All SchemaIR invariants
// from docs/design/schema-ir-spec.md are validated before returning.
func Parse(filename string) (*ir.SchemaIR, error)

// ParseError describes a structured parse failure.
type ParseError struct {
    File    string
    Line    int
    Column  int
    Message string
}

func (e *ParseError) Error() string // "path/to/file.go:10:5: message"
```

---

## Parsing strategy

### High-level algorithm

1. `go/parser.ParseFile(fset, filename, nil, 0)` — parse the source file
2. Walk the AST using `ast.Inspect` to find a `*ast.CallExpr` whose function is
   `cfgx.Define` (selector expression `cfgx` + `Define`)
3. For each argument to `Define`, dispatch to `parseEntry(expr)` or `parseConstraint(expr)`
4. Return the assembled `SchemaIR`

### `parseEntry(expr *ast.CallExpr) (ir.EntryIR, error)`

Dispatch on the function name:
- `cfgx.String(id)` → `EntryType: TypeString`
- `cfgx.Int(id)` → `EntryType: TypeInt`
- `cfgx.Bool(id)` → `EntryType: TypeBool`
- `cfgx.Float64(id)` → `EntryType: TypeFloat64`
- `cfgx.Duration(id)` → `EntryType: TypeDuration`
- `cfgx.List(id, elemExpr)` → `EntryType: TypeList` + recurse for `ElementType`
- `cfgx.Object(id, ...propExprs)` → `EntryType: TypeObject` + recurse for `Properties`
- `cfgx.Computed(id, fn)` → `EntryType: TypeComputed` + set `HasGetter: true`
- `cfgx.MutuallyExclusive(...)` → handled in `parseConstraint`

Builder methods are chained calls. Walk the chain from the outermost `CallExpr`:
- The outermost call is the last builder method called
- Walking inward (through `Fun.X`) reaches the constructor call

For each recognized method name, extract its argument(s):

| Method | Arg type | IR field |
|--------|----------|----------|
| `.Flag(long, [short])` | string literals | `FlagLong`, `FlagShort` |
| `.Env(name)` | string literal | `Env` |
| `.Default(val)` | literal (string/int/bool/float) | `Default` |
| `.Required()` | none | `Required = true` |
| `.Sensitive()` | none | `Sensitive = true` |
| `.Desc(text)` | string literal | `Description` |
| `.IsLive()` | none | `IsLive = true` |
| `.Deprecated(msg)` | string literal | `Deprecated = msg` |
| `.Scope(cmds...)` | string literals | `Scope` |
| `.Tags(tags...)` | string literals | `Tags` |
| `.DependsOn(id, val)` | string literal + literal | `DependsOn` |
| `.Validate(fn)` | any → detect if builtin or closure | see below |
| `.OnChange(fn)` | function literal | `HasOnChange = true` |
| `.Getter(fn)` | function literal | `HasGetter = true` |

### Detecting built-in vs. custom validators

`.Validate(cfgx.Email())` — the argument is a `*ast.CallExpr` with selector `cfgx.Email`.
Extract the validator name and params, add to `BuiltinValidators`.

`.Validate(func(v string) error {...})` — the argument is a `*ast.FuncLit`.
Set `HasValidate = true`.

`.Validate(myCustomValidator)` — the argument is an identifier (not a cfgx call, not a
function literal). Set `HasValidate = true` (cannot determine more statically).

### Built-in validator argument extraction

| Builtin call | Params to extract |
|---|---|
| `cfgx.Email()` | none |
| `cfgx.URL()` | none |
| `cfgx.Port()` | none |
| `cfgx.PortRange(min, max)` | two int literals → `{"min": m, "max": n}` |
| `cfgx.Base64()` | none |
| `cfgx.Regex(pattern)` | one string literal → `{"pattern": p}` |
| `cfgx.DurationValidator()` | none |
| `cfgx.Semver()` | none |
| `cfgx.Enum(vals...)` | variadic string literals → `{"values": [...]}` |
| `cfgx.Range(min, max)` | two float literals → `{"min": m, "max": n}` |

### Post-parse validation (invariants from schema-ir-spec.md)

After building the full IR, validate:
1. Unique IDs within each level
2. `DependsOn.ID` references an existing entry
3. `ConstraintIR.EntryIDs` reference existing entries
4. Warn (not error) if `Required` + non-nil `Default` on same entry

Return a `ParseError` for violations of rules 1–3.

---

## Test requirements

### `testdata/schemas/minimal.go`

```go
package testschema

import "github.com/example/cfgx"

var Schema = cfgx.Define(
    cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080).Desc("HTTP port"),
)
```

Expected IR: one `EntryIR` with all fields populated correctly.

### `testdata/schemas/full.go`

A schema that exercises ALL properties: nested object, computed, isLive, onChanged,
getter, sensitive, deprecated, scope, tags, dependsOn, mutuallyExclusive,
all built-in validators.

### Test cases

```go
func TestParse(t *testing.T) {
    tests := []struct{
        name     string
        file     string
        wantErr  bool
        validate func(t *testing.T, ir *ir.SchemaIR)
    }{
        // minimal schema
        // nested object
        // computed entry → HasGetter=true, no Flag/Env
        // sensitive entry → Sensitive=true
        // deprecated entry → Deprecated="message"
        // isLive + onChanged → IsLive=true, HasOnChange=true
        // custom validate → HasValidate=true
        // builtin validate Email → BuiltinValidators contains {Name:"email"}
        // builtin validate PortRange → params extracted correctly
        // mutuallyExclusive → ConstraintIR produced
        // duplicate ID at root level → ParseError
        // duplicate ID in nested object → ParseError
        // DependsOn referencing non-existent ID → ParseError
    }
}
```

---

## Definition of done

- `go test ./gen/parser/... -count=1` passes
- `go vet ./gen/parser/...` clean
- All SchemaIR invariants verified for the `full.go` fixture
- `ParseError.Error()` format: `"path/to/file.go:10:5: duplicate entry ID "port""`
- No external dependencies beyond standard library + `internal/ir`
