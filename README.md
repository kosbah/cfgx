# cfgx

> **Declarative, type-safe configuration for [cobra](https://github.com/spf13/cobra) + [viper](https://github.com/spf13/viper) applications.**

[![CI](https://github.com/epistemer/cfgx/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/epistemer/cfgx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/epistemer/cfgx.svg)](https://pkg.go.dev/github.com/epistemer/cfgx)
[![Go 1.21+](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://go.dev/)

You declare every setting once, in a Go schema file. `cfgx gen` turns it into typed config
structs, cobra flag registration, viper bindings, one validating `Load` function per command,
and the example files and docs you'd otherwise keep in sync by hand.

> **Status:** pre-release. See [Status & known limitations](#status--known-limitations).

---

## Why cfgx?

cobra and viper are excellent, but they leave a gap between declaring commands and managing
the configuration those commands need. Every cobra/viper project ends up with the same
hand-written boilerplate:

| Pain point | Today | With cfgx |
|---|---|---|
| **Command-driven, not config-driven** | Config wiring is scattered across command files | One declarative schema per project; business-logic packages never import cobra or viper |
| **No type safety** | `viper.GetString("db.password")` everywhere | Generated structs with getter methods; `LoadServe()` returns `(*ServeConfig, error)` |
| **Needless rewiring** | Flags, env binds, validators, `.env.example`, `config.yaml` kept in sync by hand | `cfgx gen` produces all of it from the schema |
| **Adding a setting is painful** | Touch the flag, the viper bind, the struct, the validator and the example files | Add one builder call to the schema and regenerate |

---

## Quick start

### 1. Install the CLI

```sh
go install github.com/epistemer/cfgx/cmd/cfgx@latest
```

> Until the initial PRs are merged into `epistemer/cfgx`, the module can't be fetched from
> the proxy. Clone the repository and run `go install ./cmd/cfgx` instead.

### 2. Scaffold a project

```sh
cfgx init github.com/yourorg/myapp
cd myapp
```

`cfgx init` writes `go.mod`, `main.go`, `cmd/root.go`, `cmd/serve.go`, `cmd/migrate.go`, an
example `cmd/schema.go` and an empty `pkg/`. It then runs `cfgx gen` and `go mod tidy`, so the
project builds right away:

```sh
go run . serve --help   # the flags come from cmd/schema.go
```

### 3. Describe your configuration

Edit `cmd/schema.go`. Everything about a setting lives in one builder chain:

```go
// cmd/schema.go
package cmd

//go:generate cfgx gen

import (
    "fmt"

    "github.com/epistemer/cfgx"
)

var Schema = cfgx.Define(
    // Shared by every command: available as cfg.Debug().
    cfgx.Bool("debug").
        Flag("--debug").Env("APP_DEBUG").Default(false).
        IsLive(). // hot-reloaded while the program runs
        Desc("Enable debug logging"),

    // Only the serve command sees this one: cfg.Port() on *ServeConfig.
    cfgx.Int("port").
        Flag("--port", "-p").Env("APP_PORT").Default(8080).
        Validate(cfgx.PortRange(1024, 65535)).
        Desc("HTTP port the server listens on").
        Scope("serve"),

    // A nested object shared by serve and migrate: cfg.Db().Host(), ...
    cfgx.Object("db",
        cfgx.String("host").Env("DB_HOST").Default("localhost"),
        cfgx.Int("port").Env("DB_PORT").Default(5432).Validate(cfgx.Port()),
        cfgx.String("name").Env("DB_NAME").Required(),
        cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required(),
        // Computed entries get the generated *DbConfig as parent.
        cfgx.Computed("dsn", func(parent any) string {
            c, ok := parent.(*DbConfig)
            if !ok {
                return ""
            }
            return fmt.Sprintf("postgres://app:%s@%s:%d/%s",
                c.Password(), c.Host(), c.Port(), c.Name())
        }),
    ).Scope("serve", "migrate"),
)
```

### 4. Generate

```sh
go generate ./cmd/...
```

`cfgx gen` writes:

| File | Contents |
|---|---|
| `cmd/config.gen.go` | Typed structs, `RegisterFlags`, `BindViper`, `Load`, one `Load<Scope>` per scope, and `Close` when there are IsLive entries |
| `.env.example` | Every env var with its description and default |
| `config.example.yaml` | Full YAML template |
| `config.schema.json` | JSON Schema (draft-07) for editor tooling |
| `docs/config.md` | Markdown config reference |
| `docs/config.1` | Man page fragment |

Commit the generated files and never edit them by hand; rerun `go generate` after every
schema change.

### 5. Wire cobra and viper (once)

The scaffold already does this. `RegisterFlags` adds a persistent flag for every entry that
declares one; `BindViper` binds those flags, the env vars and the defaults:

```go
// cmd/root.go
func NewRootCmd() *cobra.Command {
    root := &cobra.Command{Use: "myapp", SilenceUsage: true}
    root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")

    RegisterFlags(root, Schema)
    cobra.OnInitialize(func() {
        if cfgFile != "" {
            viper.SetConfigFile(cfgFile)
            cobra.CheckErr(viper.ReadInConfig())
        }
        BindViper(viper.GetViper(), Schema)
    })

    root.AddCommand(newServeCmd(), newMigrateCmd())
    return root
}
```

Each command then loads its own typed, validated config:

```go
// cmd/serve.go
RunE: func(cmd *cobra.Command, _ []string) error {
    cfg, err := LoadServe(Schema, viper.GetViper()) // *ServeConfig
    if err != nil {
        return err // *cfgx.ValidationErrors: "db.name: required; ..."
    }
    defer cfg.Close() // stops the IsLive watcher (generated when there are IsLive entries)

    db := database.New(cfg.Db())     // *DbConfig satisfies database.Config
    srv := server.New(cfg, cfg.Db()) // *ServeConfig satisfies server.Config
    if err := db.Ping(cmd.Context()); err != nil {
        return err
    }
    return srv.Run(cmd.Context())
},
```

`LoadServe` reads every value `serve` uses, runs Getters, validates with
`Schema.ValidateScope(v, "serve")` (Required, DependsOn, built-in and custom validators,
MutuallyExclusive) and, if the schema has IsLive entries, starts a watcher for them. A value that only another
command uses can't make `serve` fail.

### 6. Use the getter API

The generated structs have unexported fields and a getter per entry:

```go
cfg.Debug()          // bool: global entry, promoted from the embedded Config
cfg.Port()           // int: scoped to serve
cfg.Db().Host()      // string: nested object
cfg.Db().Password()  // string: the real value, for your code to use
cfg.Db().DSN()       // string: Computed, evaluated on every call
fmt.Println(cfg)     // Sensitive values never show up in %v, JSON or text output
```

IsLive values are stored atomically and updated in the background, so `cfg.Debug()` always
returns the current value.

### 7. Keep business logic clean

Your packages define the interfaces they need. The generated structs satisfy them through
Go's structural typing, so business logic never imports cfgx, cobra or viper:

```go
// pkg/database/database.go
package database

type Config interface {
    Host() string
    Port() int
    Name() string
    Password() string
    DSN() string
}

func New(cfg Config) *DB { /* ... */ }
```

This also makes those packages trivial to test: a small struct with the right methods is a
valid config.

---

## Config properties

```text
cfgx.String("name")           entry types: String | Int | Bool | Float64 | Duration | List | Object | Computed
  .Flag("--name", "-n")       cobra long flag (+ optional short), registered by RegisterFlags
  .Env("APP_NAME")            environment variable, bound by BindViper
  .Default("myapp")           type-safe default (generics)
  .Required()                 must be set to a non-zero value
  .Sensitive()                redacted when printed and in generated example files
  .Desc("...")                help text; used in flags and docs
  .Validate(fn)               custom validator: func(v T) error
  .Validate(cfgx.Email())     built-ins: Email | URL | Port | PortRange | Base64 | Regex |
                              DurationValidator | Semver | Enum | Range
  .IsLive()                   kept up to date by a background watcher
  .OnChange(fn)               called by the watcher: func(old, new T)
  .Getter(fn)                 read the value from code: func() (T, error)
  .Deprecated("use X")        marked Deprecated in docs and on the generated getter
  .Scope("serve", "migrate")  only on the listed commands' config structs
  .Tags("infra", "network")   groups entries in the generated docs
  .DependsOn("tls", true)     only validated when another entry has that value

cfgx.List("hosts", cfgx.String("host"))            list of a scalar type
cfgx.Object("db", ...entries)                      nested; generates a DbConfig struct
cfgx.Computed("dsn", func(parent any) T { ... })   derived from sibling entries
cfgx.MutuallyExclusive("json-output", "text-output")   schema-level constraint
```

Object entries take `Desc`, `Deprecated`, `Scope`, `Tags` and `DependsOn`; Computed entries
take `Desc`, `Tags`, `Scope`, `IsLive` and `OnChange`. The full reference is in
[docs/design/config-anatomy.md](docs/design/config-anatomy.md).

---

## CLI commands

| Command | What it does | Main flags |
|---|---|---|
| `cfgx init <module-path>` | Scaffold a new cobra/viper+cfgx project, generate its config and run `go mod tidy` | `--dir`, `--force`, `--no-tidy` |
| `cfgx gen` | Write `config.gen.go` next to the schema and the artifacts in the module root | `-s/--schema`, `-o/--out-dir`, `-p/--pkg`, `--no-env`, `--no-yaml`, `--no-json-schema`, `--no-docs`, `--dry-run` |
| `cfgx validate` | Check a config file and/or the environment against the schema; one line per key | `-s/--schema`, `-c/--config`, `-e/--env`, `--show-values` |
| `cfgx docs` | Generate only documentation artifacts | `-s/--schema`, `-f/--format md,man,json-schema`, `-o/--out` |

Without `--schema`, `gen`, `validate` and `docs` look for the single `schema.go` containing
`cfgx.Define` under the current directory. `cfgx validate` exits with `0` when the config is
valid, `1` on validation errors and `2` on tool errors, so it drops straight into CI:

```sh
cfgx validate --config config.yaml --env
```

`validate` works from the parsed schema, so it can't run custom `Validate` closures or Getters.

---

## Project layout

```text
github.com/epistemer/cfgx/
├── cfgx.go, schema.go, entry.go, validators.go   DSL: Define, entry builders, Schema, validators
├── runtime/               runtime used by generated code: IsLive Watcher; static validation for the CLI
├── gen/
│   ├── parser/            go/ast-based schema parser → SchemaIR
│   ├── codegen/           SchemaIR → config.gen.go (text/template + go/format)
│   └── artifacts/         SchemaIR → .env.example, YAML, JSON Schema, Markdown, man page
├── cmd/cfgx/              the cfgx CLI (cobra); scaffold/ holds the cfgx init templates
├── internal/
│   ├── ir/                SchemaIR types (the parser ↔ generator contract)
│   ├── convert/           value conversion shared by Load and the CLI
│   └── validate/          built-in validator implementations
├── examples/webapp/       realistic example project (separate Go module)
├── _test/e2e/             end-to-end tests (build tag e2e)
├── testdata/              schema fixtures and golden files
└── docs/                  design docs and the migration guide
```

---

## Status & known limitations

cfgx works end to end (see [examples/webapp](examples/webapp)), but it hasn't had a release
yet. Known limitations:

- **`github.com/epistemer/cfgx` isn't `go install`-able yet.** That only works once the
  initial PRs are merged into `epistemer/cfgx`. Until then, build from a clone and use a
  `replace` directive, as `examples/webapp/go.mod` does.
- **`cfgx validate` isn't scope-aware.** It validates every entry, so a value that only one
  command requires is reported as missing even if you only run another command. The
  generated `Load<Scope>` functions are scope-aware.
- **The IsLive watcher reads the app's viper instance from its own goroutine.** viper isn't
  goroutine-safe, so avoid calling `viper.Set` (or reloading config yourself) while a loaded
  config is live. Call `Close` when you're done with a config that has IsLive entries.
- **`LoadServe` costs about 3.9× as much as reading the same keys from viper by hand** (the
  target was 2×; one run on a 4-core Linux VM measured about 6×, 34 µs against 5.6 µs). It
  also validates every value and starts the watcher, and it runs once at startup. See
  `examples/webapp/cmd/bench_e2e_test.go`.
- **All flags register on the root command.** `RegisterFlags` adds every flag as a persistent
  flag on the command you pass, so `serve --help` also lists flags that only `migrate` uses.
- **`Close` exists only when the schema has IsLive entries.** The commands scaffolded by
  `cfgx init` call `cfg.Close()`, so if you remove every `.IsLive()` entry, remove those
  calls too.
- **The schema parser reports only the first error.** Fix it and rerun `cfgx gen` to see the
  next one.

---

## Compatibility

- Go 1.21+
- cobra v1.8+
- viper v1.19+

---

## Documentation

- [Migrating an existing cobra/viper project](docs/migration.md)
- [Example project](examples/webapp/README.md)
- [Design & Architecture](docs/design/architecture.md)
- [Config Anatomy Reference](docs/design/config-anatomy.md)
- [Developer Experience Walkthrough](docs/design/developer-experience.md)
- [SchemaIR Specification](docs/design/schema-ir-spec.md)
- [Roadmap](ROADMAP.md)
- [Contributing](CONTRIBUTING.md)

---

## License

MIT
