# Architecture

## Overview

cfgx sits between your schema definition (a single Go file per project) and the cobra/viper
wiring (generated once, rarely touched). Business logic packages are completely isolated from
both cobra and cfgx.

```
┌─────────────────────────────────────────────────────────────────────┐
│  Developer writes                                                   │
│                                                                     │
│   cmd/schema.go ──── cfgx.Define(...)                              │
└──────────────────────────┬──────────────────────────────────────────┘
                           │  go generate / cfgx gen
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  cfgx generator pipeline                                            │
│                                                                     │
│  gen/parser ──► SchemaIR ──► gen/codegen ──► config.gen.go         │
│                    │                                                │
│                    └───────► gen/artifacts ──► .env.example        │
│                                                  config.example.yaml│
│                                                  config.schema.json │
│                                                  docs/config.md     │
│                                                  docs/config.1      │
└─────────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  At runtime                                                         │
│                                                                     │
│  cmd/root.go                                                        │
│    RegisterFlags(root, Schema) ──────────────────► cobra flags     │
│    BindViper(root, v, Schema) ───────────────────► viper env bind  │
│    cfg, err := Load(Schema, v) ─────────────────► *Config          │
│                    │                                                │
│                    │  Schema retained for validators + IsLive       │
│                    ▼                                                │
│  cfgx/runtime                                                       │
│    Validator registry    ◄──── Schema.Validate()                   │
│    IsLive Watcher        ◄──── Schema.WatcherEntries()             │
│    OnChange dispatch                                                │
└──────────────────────────┬──────────────────────────────────────────┘
                           │  Go structural typing — no cfgx import
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Business logic packages                                            │
│                                                                     │
│  pkg/server/server.go:                                             │
│    type Config interface { Port() int; TLSEnabled() bool }         │
│    func New(cfg Config) *Server { ... }                            │
│                                                                     │
│  pkg/database/database.go:                                         │
│    type Config interface { Host() string; DSN() string }           │
│    func New(cfg Config) *DB { ... }                                │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Package responsibilities

### `cfgx` (root package)

The only package application developers import in their schema files.

- **`Schema`** — holds the full tree of `Entry` values; retained at runtime
- **`Define(entries ...Entry) *Schema`** — schema constructor
- **`String | Int | Bool | Float64 | List | Object | Computed`** — entry constructors
- **`MutuallyExclusive(ids ...string) Constraint`** — schema-level constraint
- **Builder chain** on every entry: `.Flag()`, `.Env()`, `.Default()`, `.Required()`,
  `.Sensitive()`, `.Desc()`, `.Validate()`, `.IsLive()`, `.OnChange()`, `.Getter()`,
  `.Deprecated()`, `.Scope()`, `.Tags()`, `.DependsOn()`
- **`schema.ToIR() *ir.SchemaIR`** — produces the intermediate representation (used by gen)
- **`schema.Validate(v *viper.Viper) error`** — runs all runtime validators (called by generated `Load()`)
- **`schema.WatcherEntries() []runtime.WatchEntry`** — returns IsLive entries for the watcher

**Dependencies:** standard library only (no cobra, no viper, no fsnotify).

---

### `cfgx/internal/ir` (intermediate representation)

The data-transfer layer between the parser and the generators. Pure data; no logic.

- **`SchemaIR`** — serializable snapshot of the schema tree
- All entry metadata (names, types, defaults, flags, etc.) — see [schema-ir-spec.md](schema-ir-spec.md)
- Closures are NOT included; they are flagged with boolean sentinels (`HasValidate`, `HasOnChange`, `HasGetter`)
- JSON-serializable; used by `cfgx gen` to pass data between parse and generate steps

**Dependencies:** standard library only.

---

### `cfgx/gen/parser`

Reads a Go source file and extracts the `cfgx.Define(...)` call into a `SchemaIR`.

- Uses `go/parser` and `go/ast` — static analysis, no compilation
- Extracts only literal values (string, int, bool, float); skips and flags closures
- Returns `ParseError` with file/line/column on invalid DSL

**Dependencies:** standard library only (`go/ast`, `go/parser`, `go/token`).

---

### `cfgx/gen/codegen`

Transforms a `SchemaIR` into a formatted Go source file.

- Template-driven (`text/template`); output always passed through `go/format.Source()`
- Generates typed structs, methods, registration helpers, `Load()`
- One struct per scope + one root `Config` struct

**Dependencies:** `github.com/example/cfgx/internal/ir` (data only),
standard library (`text/template`, `go/format`).

---

### `cfgx/gen/artifacts`

Transforms a `SchemaIR` into non-Go artifacts.

- Independent generators per artifact type; each takes `*ir.SchemaIR` and returns `[]byte`
- No external dependencies

---

### `cfgx/runtime`

Thin runtime package imported by generated `config.gen.go`.

- **`Validator registry`** — built-in validators + `Register()` for custom ones
- **`Watcher`** — background goroutine; polls env vars or listens to file changes for IsLive entries

**Dependencies:** `github.com/fsnotify/fsnotify` (file watching only).

---

### `cmd/cfgx` (CLI binary)

The `cfgx` command. Uses cobra for its own subcommands.

- Orchestrates parser → codegen + artifact generators
- Embeds scaffold templates for `cfgx init`

**Dependencies:** all `gen/` packages + `github.com/spf13/cobra`.

---

## Data flow: `cfgx gen`

```
cmd/schema.go
    │
    │  gen/parser.Parse("cmd/schema.go")
    ▼
SchemaIR (JSON-serializable, no closures)
    │
    ├─► gen/codegen.Generate(ir, "cmd") ────────► cmd/config.gen.go
    ├─► gen/artifacts.EnvExample(ir) ───────────► .env.example
    ├─► gen/artifacts.YAMLExample(ir) ──────────► config.example.yaml
    ├─► gen/artifacts.JSONSchema(ir) ───────────► config.schema.json
    ├─► gen/artifacts.Markdown(ir) ─────────────► docs/config.md
    └─► gen/artifacts.ManPage(ir) ──────────────► docs/config.1
```

---

## Design decisions

### Why keep `Schema` at runtime?

The generated code is mostly self-contained (typed structs, cobra registration, viper binds).
However, runtime validators and IsLive watching require the original `Schema` value because
they hold Go closures (`Validate`, `OnChange`, `Getter`) that cannot be serialized.
Keeping the `Schema` at runtime means:
- `Load()` calls `schema.Validate(v)` which invokes all registered validator functions
- The `Watcher` receives `WatchEntry` values from `schema.WatcherEntries()`, which include
  the `Getter` and `OnChange` closures

The runtime import is thin: one `*Schema` pointer and a `*runtime.Watcher` (if any IsLive entries exist).

### Why `go/ast` for parsing instead of compile-and-reflect?

Static AST parsing means `cfgx gen` has no compilation overhead — it parses the source
text directly. This is fast and avoids import cycles. The trade-off is that only literal
values can be extracted; function literals are detected but not extracted. This is fine
because function literals are runtime behaviour, not schema metadata.

### Why structural typing instead of generated interfaces?

Go's implicit interface satisfaction means the generated `DbConfig` struct automatically
satisfies any `interface { Host() string; Port() int; ... }` that a package defines.
There is no annotation required in the schema DSL. This keeps business logic packages
completely free of any cfgx import.

### Why generics for `Default()` and `Validate()`?

Type-safe defaults (`Default(8080)` — not `Default("8080")`) catch mismatches at compile
time rather than at `cfgx gen` runtime. Typed validators mean the validator function
receives the actual Go type, not `any`, which avoids manual type assertions.

```go
// Without generics — runtime type error possible
cfgx.Int("port").Default("8080")   // compiles; fails at gen time

// With generics — compile-time error
cfgx.Int("port").Default("8080")   // compile error: string is not int
```
