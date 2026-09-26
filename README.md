# cfgx

> **Declarative, type-safe configuration for [cobra](https://github.com/spf13/cobra) + [viper](https://github.com/spf13/viper) applications.**

[![CI](https://github.com/example/cfgx/actions/workflows/ci.yml/badge.svg)](https://github.com/example/cfgx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/example/cfgx.svg)](https://pkg.go.dev/github.com/example/cfgx)
[![Go 1.21+](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://go.dev/)

> ⚠️ **Module path placeholder:** `github.com/example/cfgx` is a working name.
> Update it throughout the repository once the final path is decided.

---

## Why cfgx?

cobra and viper are excellent — but they leave a painful gap between declaring commands and
managing the configurations those commands need. Every cobra/viper project ends up with the
same hand-written boilerplate:

| Pain point | Today | With cfgx |
|---|---|---|
| **Command-driven, not config-driven** | Configs are imperative wiring scattered across command files | One declarative schema per project; business-logic packages never import cobra or viper |
| **No type safety** | `viper.GetString("db.password")` everywhere; `map[string]any` | Generated typed structs with getter methods; `Load()` returns `(*Config, error)` |
| **Needless rewiring** | Re-wire flags, envs, validators, `.env.example`, `config.yaml` by hand | `cfgx gen` produces all of it from the schema |
| **Adding configs is painful** | Touch cobra flags, viper binds, struct, validator, example files — separately | Add one builder call to the schema, re-run `cfgx gen` |

---

## Quick start

### 1 — Install the CLI

```sh
go install github.com/example/cfgx/cmd/cfgx@latest
```

### 2 — Scaffold a new project

```sh
cfgx init github.com/yourorg/myapp
cd myapp
```

### 3 — Describe your configs (once)

```go
// cmd/schema.go
//go:generate cfgx gen

package cmd

import "github.com/example/cfgx"

var Schema = cfgx.Define(
    cfgx.String("app-name").
        Env("APP_NAME").Default("myapp").
        Desc("Human-readable application name"),

    cfgx.Int("port").
        Flag("--port", "-p").Env("APP_PORT").Default(8080).
        Validate(cfgx.PortRange()).
        Desc("HTTP port the server listens on"),

    cfgx.Object("db",
        cfgx.String("host").Env("DB_HOST").Default("localhost"),
        cfgx.Int("port").Env("DB_PORT").Default(5432),
        cfgx.String("name").Env("DB_NAME").Required(),
        cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required(),
        cfgx.Computed("dsn", func(c DbConfig) string {
            return fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
                c.Name(), c.Password(), c.Host(), c.Port(), c.Name())
        }),
    ).Scope("serve"),

    cfgx.Bool("debug").
        Flag("--debug").Env("APP_DEBUG").Default(false).
        IsLive().OnChange(func(old, new bool) {
            if new { log.SetLevel(log.DebugLevel) }
        }).
        Desc("Enable debug logging (hot-reloadable)"),
)
```

### 4 — Generate

```sh
go generate ./cmd/...
```

`cfgx gen` writes:

| File | Contents |
|---|---|
| `cmd/config.gen.go` | Typed structs, `RegisterFlags()`, `BindViper()`, `Load()` |
| `.env.example` | All env vars with descriptions and defaults |
| `config.example.yaml` | Full YAML template |
| `config.schema.json` | JSON Schema (draft-07) for editor tooling |
| `docs/config.md` | Markdown config reference |
| `docs/config.1` | Man page fragment |

### 5 — Wire cobra (once)

```go
// cmd/root.go
func Execute() {
    root := &cobra.Command{Use: "myapp"}
    cmd.RegisterFlags(root, Schema)
    cmd.BindViper(root, viper.GetViper(), Schema)

    root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
        cfg, err := cmd.Load(Schema, viper.GetViper())
        if err != nil {
            return err
        }
        // pass to business logic via the packages' own interfaces
        srv, err := server.New(cfg.Server)
        db, err := database.New(cfg.Db)
        // ...
    }
}
```

### 6 — Business logic stays clean

Your packages define their own interfaces. The generated structs satisfy them automatically
via Go's structural typing — no cfgx import required in business logic.

```go
// pkg/database/database.go — zero cfgx dependency
type Config interface {
    Host() string
    Port() int
    Name() string
    Password() string
    DSN() string
}

func New(cfg Config) *DB { /* ... */ }
```

---

## Config properties

Every config entry supports:

```
cfgx.String("name")          — entry type: String | Int | Bool | Float64 | List | Object | Computed
  .Flag("--name", "-n")      — cobra long flag (+ optional short)
  .Env("APP_NAME")           — viper env binding
  .Default("myapp")          — type-safe default (generics)
  .Required()                — validated non-zero in Load()
  .Sensitive()               — masked in help, logs, example files
  .Desc("...")               — help text
  .Validate(fn)              — inline validator: func(v T) error
  .Validate(cfgx.Email())    — built-in: Email | URL | Port | Base64 | Regex | Duration | Semver | Enum | Range
  .IsLive()                  — re-invoke getter on every access
  .OnChange(fn)              — called by the runtime watcher on value change
  .Getter(fn)                — lazy-load: func() (T, error)
  .Deprecated("use X")       — warning on use; annotated in docs
  .Scope("serve", "migrate") — restrict to specific cobra commands
  .Tags("infra", "network")  — group in generated docs
  .DependsOn("tls", true)    — only validated when another config has a value

cfgx.Object("db", ...props)                — recursive; generates a nested DbConfig struct
cfgx.Computed("dsn", func(c T) string{})   — derived from sibling configs
cfgx.MutuallyExclusive("json-out", "text-out") — schema-level constraint
```

---

## CLI commands

```sh
cfgx init [module-path]               # scaffold new cobra/viper+cfgx project
cfgx gen [-schema path] [-out path]   # regenerate all artifacts from schema
cfgx validate [-config path] [-env]   # validate environment against schema (CI-friendly)
cfgx docs [-format md|man|json-schema] [-out dir]  # generate docs only
```

---

## Project layout

```
github.com/example/cfgx/
├── cfgx.go / schema.go / entry.go   — DSL types and builder chain
├── runtime/                          — thin runtime: validators, IsLive watcher
├── gen/
│   ├── parser/                       — go/ast-based schema parser → SchemaIR
│   ├── codegen/                      — SchemaIR → config.gen.go (text/template)
│   └── artifacts/                    — SchemaIR → .env, yaml, JSON schema, docs, man
├── cmd/cfgx/                         — CLI binary (cobra-based)
├── internal/ir/                      — SchemaIR types (parser ↔ codegen contract)
├── examples/webapp/                  — realistic example project
└── testdata/schemas/                 — schema fixtures for unit tests
```

---

## Compatibility

- Go 1.21+
- cobra v1.8+
- viper v1.19+

---

## Documentation

- [Design & Architecture](docs/design/architecture.md)
- [Config Anatomy Reference](docs/design/config-anatomy.md)
- [Developer Experience Walkthrough](docs/design/developer-experience.md)
- [SchemaIR Specification](docs/design/schema-ir-spec.md)
- [Roadmap](ROADMAP.md)
- [AI Agent Prompts](docs/agents/README.md)
- [Contributing](CONTRIBUTING.md)

---

## License

MIT
