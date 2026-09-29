# SchemaIR Specification

`SchemaIR` is the intermediate representation (IR) that passes between the schema parser
(`gen/parser`) and the code/artifact generators. It is defined in `internal/ir` and is
the formal contract between those subsystems.

**Key property:** `SchemaIR` must be fully JSON-serializable. It contains only data —
no Go closures, no function references. Runtime-only properties (validators, onChange,
getter functions) are represented as boolean sentinels.

---

## Go type definitions

> These are the authoritative type definitions. Implement them verbatim in
> `internal/ir/schema_ir.go`.

```go
package ir

// SchemaIR is the serializable snapshot of a cfgx.Schema.
type SchemaIR struct {
    Entries     []EntryIR      `json:"entries"`
    Constraints []ConstraintIR `json:"constraints,omitempty"`
}

// EntryIR represents a single config entry at any nesting level.
type EntryIR struct {
    // Core identity
    ID   string    `json:"id"`             // e.g. "port", "db", "db.host"
    Type EntryType `json:"type"`           // see EntryType constants

    // Flag & env
    FlagLong  string `json:"flag_long,omitempty"`  // "--port"
    FlagShort string `json:"flag_short,omitempty"` // "-p"
    Env       string `json:"env,omitempty"`         // "APP_PORT"

    // Value
    Default     any    `json:"default,omitempty"`     // literal; type must match Type
    Description string `json:"description,omitempty"`

    // Behaviour flags
    Required   bool   `json:"required,omitempty"`
    Sensitive  bool   `json:"sensitive,omitempty"`
    IsLive     bool   `json:"is_live,omitempty"`
    Deprecated string `json:"deprecated,omitempty"` // non-empty = deprecated; value = message

    // Runtime function sentinels (closures are NOT serialized)
    HasOnChange bool `json:"has_on_change,omitempty"`
    HasGetter   bool `json:"has_getter,omitempty"`
    HasValidate bool `json:"has_validate,omitempty"` // custom; built-ins are in BuiltinValidators

    // Built-in validators (serializable)
    BuiltinValidators []ValidatorIR `json:"builtin_validators,omitempty"`

    // Organisation
    Scope     []string `json:"scope,omitempty"`      // command names; empty = all commands
    Tags      []string `json:"tags,omitempty"`
    DependsOn *DependsOnIR `json:"depends_on,omitempty"`

    // Nested (Object only)
    Properties []EntryIR `json:"properties,omitempty"`

    // List element type (List only)
    ElementType *EntryIR `json:"element_type,omitempty"`

    // Computed result type (Computed only): the Go type T of Computed[T],
    // e.g. "string", "time.Duration", "[]string" or "any". Empty when unknown.
    ResultType string `json:"result_type,omitempty"`
}

// EntryType enumerates the supported config entry types.
type EntryType string

const (
    TypeString   EntryType = "string"
    TypeInt      EntryType = "int"
    TypeBool     EntryType = "bool"
    TypeFloat64  EntryType = "float64"
    TypeDuration EntryType = "duration" // stored as time.Duration (int64 nanoseconds)
    TypeList     EntryType = "list"
    TypeObject   EntryType = "object"
    TypeComputed EntryType = "computed"
)

// DependsOnIR describes a conditional activation rule.
type DependsOnIR struct {
    ID    string `json:"id"`    // ID of the controlling entry
    Value any    `json:"value"` // required value to activate this entry
}

// ConstraintIR represents a schema-level constraint.
type ConstraintIR struct {
    Type     ConstraintType `json:"type"`
    EntryIDs []string       `json:"entry_ids"`
}

// ConstraintType enumerates schema-level constraint types.
type ConstraintType string

const (
    ConstraintMutuallyExclusive ConstraintType = "mutually_exclusive"
)

// ValidatorIR represents a reference to a built-in validator with its parameters.
type ValidatorIR struct {
    Name   string         `json:"name"`             // e.g. "email", "port_range", "enum"
    Params map[string]any `json:"params,omitempty"` // e.g. {"min":1024,"max":65535}
}
```

---

### `result_type` (Computed entries)

`ResultType` records the Go result type `T` of a `cfgx.Computed[T]` entry so that
code generators can emit a typed getter (e.g. `DSN() string`).

- `Schema.ToIR()` fills it from `T` via reflection: predeclared types by name
  (`"string"`, `"int"`), `"time.Duration"`, composites such as `"[]string"`, and
  `"any"` for the empty interface. Named types from other packages are rendered
  package-qualified (e.g. `"mypkg.DSN"`).
- The parser fills it from explicit type arguments (`cfgx.Computed[string](...)`)
  or, when they are omitted, from the single result of the function literal
  (`func(parent any) string`). A local alias of the `time` import is normalized
  to `time`. If neither is available (e.g. the function is a named identifier),
  it is left empty.
- Generators must treat an empty or unsupported `ResultType` as `any`.
- It is always empty for non-Computed entries.

---

## Built-in validator names and params

| DSL call | `ValidatorIR.Name` | `ValidatorIR.Params` |
|---|---|---|
| `cfgx.Email()` | `"email"` | `{}` |
| `cfgx.URL()` | `"url"` | `{}` |
| `cfgx.Port()` | `"port"` | `{}` |
| `cfgx.PortRange(min, max)` | `"port_range"` | `{"min": 1024, "max": 65535}` |
| `cfgx.Base64()` | `"base64"` | `{}` |
| `cfgx.Regex(pattern)` | `"regex"` | `{"pattern": "^[a-z]+$"}` |
| `cfgx.DurationValidator()` | `"duration"` | `{}` |
| `cfgx.Semver()` | `"semver"` | `{}` |
| `cfgx.Enum(vals...)` | `"enum"` | `{"values": ["text","json"]}` |
| `cfgx.Range(min, max)` | `"range"` | `{"min": 0.0, "max": 100.0}` |

---

## Invariants

The parser must guarantee these invariants before returning a `SchemaIR`. Generators may
assume all invariants hold; they must NOT re-validate.

1. All `EntryIR.ID` values are unique within their parent scope (root or object properties)
2. `EntryIR.Type == TypeObject` implies `Properties` is non-empty
3. `EntryIR.Type == TypeList` implies `ElementType` is non-nil
4. `EntryIR.Type == TypeComputed` implies `FlagLong == ""`, `FlagShort == ""`, `Env == ""`
5. `EntryIR.DependsOn.ID` references an existing entry ID in the same schema
6. Each `ConstraintIR.EntryIDs` element references an existing entry ID
7. `Default` value's JSON type is compatible with `EntryIR.Type`:
   - `TypeString`, `TypeDuration` → `string` or `null`
   - `TypeInt` → `number` (integer) or `null`
   - `TypeBool` → `boolean` or `null`
   - `TypeFloat64` → `number` or `null`
   - `TypeList` → `array` or `null`
   - `TypeObject`, `TypeComputed` → `null` (no default)
8. A `Sensitive` entry must NOT have `HasGetter: false` and `Env == ""` simultaneously
   (it would always be empty and there'd be no way to set it)
9. `Required` and a non-nil `Default` on the same entry: parser emits a warning (not error)

---

## JSON example

Below is a complete `SchemaIR` JSON for a minimal schema with one int field and one object:

```json
{
  "entries": [
    {
      "id": "port",
      "type": "int",
      "flag_long": "--port",
      "flag_short": "-p",
      "env": "APP_PORT",
      "default": 8080,
      "description": "HTTP port the server listens on",
      "builtin_validators": [
        {"name": "port_range", "params": {"min": 1024, "max": 65535}}
      ],
      "scope": ["serve"],
      "tags": ["server"]
    },
    {
      "id": "db",
      "type": "object",
      "scope": ["serve", "migrate"],
      "tags": ["database"],
      "properties": [
        {
          "id": "host",
          "type": "string",
          "env": "DB_HOST",
          "default": "localhost"
        },
        {
          "id": "port",
          "type": "int",
          "env": "DB_PORT",
          "default": 5432
        },
        {
          "id": "password",
          "type": "string",
          "env": "DB_PASSWORD",
          "required": true,
          "sensitive": true
        },
        {
          "id": "dsn",
          "type": "computed",
          "has_getter": true
        }
      ]
    }
  ],
  "constraints": [
    {
      "type": "mutually_exclusive",
      "entry_ids": ["json-output", "text-output"]
    }
  ]
}
```

---

## Versioning

The `SchemaIR` format is versioned via a `Version` field (reserved; always `"1"` in v1).
Generators must check the version and return an error for unsupported versions.
This allows forward-compatible format evolution.

Add to `SchemaIR`:

```go
Version string `json:"version"` // always "1" for cfgx v1
```
