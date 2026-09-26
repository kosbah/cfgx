# Config Anatomy Reference

Every config entry in cfgx has a set of properties. This document is the canonical
reference for what each property means, how it is expressed in the DSL, how it appears
in generated output, and any validation rules.

---

## Entry types

| DSL constructor | Go type | JSON Schema type | Notes |
|---|---|---|---|
| `cfgx.String("id")` | `string` | `"string"` | |
| `cfgx.Int("id")` | `int` | `"integer"` | |
| `cfgx.Bool("id")` | `bool` | `"boolean"` | |
| `cfgx.Float64("id")` | `float64` | `"number"` | |
| `cfgx.List("id", elemType)` | `[]T` | `"array"` | element type is another entry |
| `cfgx.Object("id", ...props)` | `XxxConfig` struct | `"object"` | recursive; generates nested struct |
| `cfgx.Computed("id", fn)` | `T` (inferred) | omitted | derived; stored as closure; no flag/env |

---

## Properties

### `ID` (positional argument)

```go
cfgx.String("app-name")
```

- Determines the Go field name (`AppName`): kebab-case → PascalCase
- Determines the YAML/JSON field name (`app-name` or `app_name` — configurable)
- Must be unique within its parent scope (root or object)
- Must be a valid Go identifier after conversion

---

### `.Flag(long [, short])`

```go
cfgx.Int("port").Flag("--port", "-p")
```

- `long`: cobra long flag name including leading `--`
- `short`: optional single-character cobra short flag including leading `-`
- If omitted, the config is not registered as a cobra flag (env/file only)
- Appears in: `RegisterFlags()`, cobra help text, `.env.example` comment

---

### `.Env(name)`

```go
cfgx.String("db-password").Env("DB_PASSWORD")
```

- The environment variable name passed to `v.BindEnv()`
- Convention (not enforced): `UPPER_SNAKE_CASE`
- If omitted, viper uses the default env key derivation (`VIPER_ENV_KEYREPLACER`)
- Appears in: `BindViper()`, `.env.example`, `config.schema.json` (as `x-env`)

---

### `.Default(val)`

```go
cfgx.Int("port").Default(8080)
cfgx.String("app-name").Default("myapp")
```

- Type-safe via generics — mismatched types are compile errors
- Used by cobra (flag default), viper (`v.SetDefault()`), and generated docs
- If absent and `.Required()` is not set, the zero value of the type is used
- Appears in: `RegisterFlags()`, `BindViper()`, `.env.example`, `config.example.yaml`

---

### `.Required()`

```go
cfgx.String("db-name").Required()
```

- `Load()` returns an error if the value is the zero value after loading all sources
- Cannot be combined with `.Default()` (a required field with a default is always satisfied;
  generator should warn)
- Appears in: `Load()` validation, JSON Schema `required` array, docs marked **Required**

---

### `.Sensitive()`

```go
cfgx.String("db-password").Sensitive()
```

- The Go field is stored as `string` internally but:
  - `String()` method returns `"[redacted]"`
  - The field is excluded from `%v` formatting
  - `fmt.Stringer` implementation returns `"[redacted]"`
- In `.env.example`: empty string with `# REQUIRED — sensitive, do not commit`
- In `config.example.yaml`: empty string with `# sensitive` comment
- In JSON Schema: `"writeOnly": true`
- In markdown docs: displayed as **sensitive** in the Type column
- Must not appear in log lines from generated code

---

### `.Desc(text)`

```go
cfgx.Int("port").Desc("HTTP port the server listens on")
```

- Used as cobra flag usage text in `RegisterFlags()`
- Used as comment in `.env.example` and `config.example.yaml`
- Used as `description` in JSON Schema
- Used as Description column in markdown docs and man page

---

### `.Validate(fn)` / `.Validate(cfgx.BuiltIn())`

```go
// Inline validator
cfgx.String("email").Validate(func(v string) error {
    if !strings.Contains(v, "@") { return fmt.Errorf("invalid email") }
    return nil
})

// Built-in validator
cfgx.String("email").Validate(cfgx.Email())
cfgx.Int("port").Validate(cfgx.PortRange(1024, 65535))
```

- Called during `Load()` after the value is read from viper
- Multiple `.Validate()` calls are allowed; all validators run and errors are combined
- Built-in validators are provided by `cfgx/runtime`; they are referenced by name in the
  DSL and resolved at runtime
- Custom validator closures are stored in the `Schema` at runtime but are not serialized
  into `SchemaIR`; they appear as `HasValidate: true` in the IR

---

### `.IsLive()`

```go
cfgx.Bool("debug").IsLive()
```

- Marks the config as hot-reloadable
- `Load()` sets up a `runtime.WatchEntry` for this field
- Accessing the value after `Load()` always returns the current live value
- Must be paired with a `Getter` or env/file source (polling the source on each access)
- The IsLive watcher in `cfgx/runtime` polls env vars or uses fsnotify for file-backed configs

---

### `.OnChange(fn)`

```go
cfgx.Bool("debug").IsLive().OnChange(func(old, new bool) {
    if new { log.SetLevel(log.DebugLevel) }
})
```

- Callback invoked by the `runtime.Watcher` when a live value changes
- Receives the old and new typed values
- Executed synchronously in the watcher goroutine; must not block
- Closure stored in `Schema`; flagged as `HasOnChange: true` in `SchemaIR`

---

### `.Getter(fn)`

```go
cfgx.String("tls-cert").Getter(func() (string, error) {
    return os.ReadFile("/etc/ssl/cert.pem")
})
```

- Lazy-load function; invoked by `Load()` instead of viper for this field
- If `.IsLive()`, invoked on every access
- Closure stored in `Schema`; flagged as `HasGetter: true` in `SchemaIR`
- Useful for secrets loaded from a vault, files read at startup, etc.

---

### `.Deprecated(message)`

```go
cfgx.String("old-flag").Deprecated("use --new-flag instead")
```

- `Load()` emits a `log.Warn` when the deprecated config is set
- Cobra flag shows `[DEPRECATED: use --new-flag instead]` in help text
- In markdown docs: entry is struck-through with the deprecation message
- In `.env.example`: prefixed with `## DEPRECATED: ...`
- In JSON Schema: `"deprecated": true` + `"description"` updated with message

---

### `.Scope(commands ...string)`

```go
cfgx.Object("db", ...).Scope("serve", "migrate")
```

- Restricts which cobra command(s) this entry belongs to
- The code generator emits the config only in the struct(s) for those scopes
- Root Config struct only contains entries with no scope or scope containing the command
- `RegisterFlags()` and `BindViper()` are scope-aware

---

### `.Tags(tags ...string)`

```go
cfgx.String("smtp-host").Tags("email", "infra")
```

- Grouping labels for generated documentation
- In markdown docs: entries are grouped by tag with a section header per tag
- No runtime effect

---

### `.DependsOn(id string, value any)`

```go
cfgx.String("tls-cert-path").DependsOn("tls-enabled", true)
```

- This entry is only validated by `Load()` when the referenced config equals `value`
- If the dependency condition is not met, the entry is treated as optional (not validated)
- Must reference an entry ID in the same schema (parser validates this)

---

## Schema-level constraints

### `cfgx.MutuallyExclusive(ids ...string)`

```go
cfgx.Define(
    cfgx.String("json-output"),
    cfgx.String("text-output"),
    cfgx.MutuallyExclusive("json-output", "text-output"),
)
```

- `Load()` returns an error if more than one of the listed configs is set (non-zero)
- Appears in generated docs as a note

---

## Generated field naming rules

| DSL ID | Go field name | Cobra flag | Env name (if `.Env()` not set) |
|---|---|---|---|
| `port` | `Port` | `--port` | `PORT` |
| `app-name` | `AppName` | `--app-name` | `APP_NAME` |
| `db` (object) | `Db` | n/a | n/a |
| `db.host` | nested in `DbConfig.Host` | `--db-host` | `DB_HOST` |
| `tls-cert-path` | `TLSCertPath` | `--tls-cert-path` | `TLS_CERT_PATH` |

**Acronym handling:** `ID`, `URL`, `TLS`, `HTTP`, `API` → all-caps in Go field names.
Acronym list is configurable in `cfgx gen` via `--acronyms`.

---

## Validation order in `Load()`

1. Viper loads values from all sources (flags > env > config file > defaults)
2. Struct is populated field by field
3. `.DependsOn` conditions are evaluated
4. `.Required()` checks run (skip if `DependsOn` condition is false)
5. `.Validate()` validators run in declaration order (skip if `DependsOn` false)
6. `MutuallyExclusive` constraints checked
7. All errors collected; if any exist, return a `*ValidationErrors` multi-error

```go
type ValidationErrors struct {
    Errors []FieldError
}

type FieldError struct {
    Field   string // dot-separated path, e.g. "db.password"
    Message string
}
```
