# Developer Experience Walkthrough

This document walks through the entire cfgx workflow from a developer's perspective,
using a realistic web application as the example. It is the source of truth for how
the DX should feel and is referenced by all phase implementation agents.

---

## Scenario

We are building `myapp` — a Go web application with two cobra commands:
- `serve` — starts an HTTP server
- `migrate` — runs database migrations

The app needs: HTTP port, debug mode (hot-reloadable), database connection details,
an SMTP email config (optional, only validated if email is enabled), and mutual
exclusion between `--json-output` and `--text-output`.

---

## Step 0: Install cfgx

```sh
go install github.com/epistemer/cfgx/cmd/cfgx@latest
```

---

## Step 1: Scaffold a new project

```sh
cfgx init github.com/yourorg/myapp
cd myapp
```

`cfgx init` creates:

```
myapp/
├── go.mod
├── go.sum
├── main.go
├── cmd/
│   ├── root.go
│   ├── serve.go
│   ├── migrate.go
│   ├── schema.go       ← example schema, edit this
│   └── config.gen.go   ← generated from schema.go
├── .env.example        ← generated
├── config.example.yaml ← generated
├── config.schema.json  ← generated
├── docs/config.md, docs/config.1  ← generated
└── pkg/                ← empty; add your business logic here
```

The scaffolded `cmd/schema.go` already has a working example schema with comments, and
`cfgx init` runs `cfgx gen` and `go mod tidy`, so `go run . serve` works right away.

---

## Step 2: Define your configs

Edit `cmd/schema.go`:

```go
package cmd

//go:generate cfgx gen

import (
    "fmt"
    "log"

    "github.com/epistemer/cfgx"
)

var Schema = cfgx.Define(
    // ── Global settings ───────────────────────────────────────────────
    cfgx.String("app-name").
        Env("APP_NAME").Default("myapp").
        Desc("Human-readable application name").
        Tags("general"),

    cfgx.Bool("debug").
        Flag("--debug").Env("APP_DEBUG").Default(false).
        IsLive().OnChange(func(old, new bool) {
            if new {
                log.Println("debug mode enabled")
            }
        }).
        Desc("Enable debug logging; hot-reloadable via APP_DEBUG").
        Tags("general"),

    cfgx.String("output-format").
        Flag("--output-format").Env("APP_OUTPUT_FORMAT").Default("text").
        Validate(cfgx.Enum("text", "json")).
        Desc("Output format").
        Tags("general"),

    cfgx.Bool("json-output").Flag("--json-output").Env("APP_JSON_OUTPUT").Tags("general"),
    cfgx.Bool("text-output").Flag("--text-output").Env("APP_TEXT_OUTPUT").Tags("general"),

    // ── HTTP server settings (serve command only) ──────────────────────
    cfgx.Int("port").
        Flag("--port", "-p").Env("APP_PORT").Default(8080).
        Validate(cfgx.PortRange(1024, 65535)).
        Desc("HTTP port the server listens on").
        Scope("serve").Tags("server"),

    cfgx.Bool("tls-enabled").
        Flag("--tls-enabled").Env("APP_TLS_ENABLED").Default(false).
        Scope("serve").Tags("server", "tls").
        Desc("Enable TLS"),

    cfgx.String("tls-cert-path").
        Flag("--tls-cert").Env("APP_TLS_CERT").
        Required().DependsOn("tls-enabled", true).
        Scope("serve").Tags("server", "tls").
        Desc("Path to TLS certificate (required when tls-enabled=true)"),

    // ── Database (serve + migrate) ─────────────────────────────────────
    cfgx.Object("db",
        cfgx.String("host").Env("DB_HOST").Default("localhost"),
        cfgx.Int("port").Env("DB_PORT").Default(5432),
        cfgx.String("name").Env("DB_NAME").Required().Desc("Database name"),
        cfgx.String("user").Env("DB_USER").Default("postgres"),
        cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required(),
        // parent is the generated *DbConfig holding this entry.
        cfgx.Computed("dsn", func(parent any) string {
            c, ok := parent.(*DbConfig)
            if !ok {
                return ""
            }
            return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
                c.User(), c.Password(), c.Host(), c.Port(), c.Name())
        }),
    ).Scope("serve", "migrate").Tags("database"),

    // ── Email (optional, only validated if email-enabled=true) ────────
    cfgx.Bool("email-enabled").
        Flag("--email-enabled").Env("EMAIL_ENABLED").Default(false).
        Tags("email"),

    cfgx.Object("smtp",
        cfgx.String("host").Env("SMTP_HOST").Default("localhost"),
        cfgx.Int("port").Env("SMTP_PORT").Default(587),
        cfgx.String("user").Env("SMTP_USER").Required(),
        cfgx.String("password").Env("SMTP_PASSWORD").Sensitive().Required(),
    ).DependsOn("email-enabled", true).Tags("email"),

    // ── Schema-level constraints ───────────────────────────────────────
    cfgx.MutuallyExclusive("json-output", "text-output"),
)
```

---

## Step 3: Generate

```sh
go generate ./cmd/...
# or directly, from the module root:
cfgx gen --schema cmd/schema.go
```

Output of `go generate` (which runs in `cmd/`):

```
✓ wrote config.gen.go          (… lines)
✓ wrote ../.env.example        (… lines)
✓ wrote ../config.example.yaml (… lines)
✓ wrote ../config.schema.json  (… lines)
✓ wrote ../docs/config.md      (… lines)
✓ wrote ../docs/config.1       (… lines)
```

### What `cmd/config.gen.go` looks like (abbreviated)

```go
// Code generated by cfgx. DO NOT EDIT.
// Source: cmd/schema.go

package cmd

import (
    "github.com/epistemer/cfgx"
    "github.com/epistemer/cfgx/runtime"
    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

// Config holds the configuration shared by all commands.
type Config struct {
    appName      string
    debug        atomic.Value // IsLive: updated by the watcher
    outputFormat string
    // ...
    cfgxWatcher *runtime.Watcher
}

func (c *Config) AppName() string { return c.appName }
func (c *Config) Debug() bool     { v, _ := c.debug.Load().(bool); return v }

// ServeConfig holds configuration for the "serve" command. It embeds Config.
type ServeConfig struct {
    Config
    port        int
    tlsEnabled  bool
    tlsCertPath string
    db          DbConfig
}

func (c *ServeConfig) Port() int     { return c.port }
func (c *ServeConfig) Db() *DbConfig { return &c.db }

// MigrateConfig holds configuration for the "migrate" command. It embeds Config.
type MigrateConfig struct {
    Config
    db DbConfig
}

type DbConfig struct {
    host     string
    port     int
    name     string
    user     string
    password sensitiveString // redacts itself in %v, String, JSON and text output
    dsn      func() string
}

func (c *DbConfig) Host() string     { return c.host }
func (c *DbConfig) Password() string { return c.password.value() }
func (c *DbConfig) DSN() string      { return c.dsn() }

func RegisterFlags(cmd *cobra.Command, schema *cfgx.Schema) { /* ... */ }
func BindViper(v *viper.Viper, schema *cfgx.Schema)         { /* ... */ }

func Load(schema *cfgx.Schema, v *viper.Viper) (*Config, error)               { /* ... */ }
func LoadServe(schema *cfgx.Schema, v *viper.Viper) (*ServeConfig, error)     { /* ... */ }
func LoadMigrate(schema *cfgx.Schema, v *viper.Viper) (*MigrateConfig, error) { /* ... */ }

// Close stops the IsLive watcher (generated because the schema has IsLive entries).
func (c *Config) Close() { /* ... */ }
```

Every `Load*` function reads the values from viper, calls Getters, validates with
`schema.ValidateScope(v, "<scope>")` and returns a `*cfgx.ValidationErrors` listing every
problem, then starts the IsLive watcher.

---

## Step 4: Wire cobra (once; rarely changed)

```go
// cmd/root.go
var cfgFile string

func NewRootCmd() *cobra.Command {
    root := &cobra.Command{
        Use:          "myapp",
        Short:        "Example cfgx application",
        SilenceUsage: true,
    }
    root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
    RegisterFlags(root, Schema) // every schema flag, persistent on root

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

```go
// cmd/serve.go
func newServeCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "serve",
        Short: "Start the HTTP server",
        RunE: func(cmd *cobra.Command, args []string) error {
            cfg, err := LoadServe(Schema, viper.GetViper())
            if err != nil {
                return err // *cfgx.ValidationErrors with field paths
            }
            defer cfg.Close() // stops the IsLive watcher
            // *ServeConfig satisfies server.Config, *DbConfig satisfies server.DBConfig
            return server.Run(cfg, cfg.Db())
        },
    }
}
```

---

## Step 5: Business logic stays clean

```go
// pkg/server/server.go
// — zero cfgx or viper imports —

type Config interface {
    Port() int
    TLSEnabled() bool
    TLSCertPath() string
}

type DBConfig interface {
    Host() string
    Port() int
    Name() string
    DSN() string
}

func Run(cfg Config, db DBConfig) error { /* ... */ }
```

The generated `*ServeConfig` satisfies `server.Config` automatically via Go's structural
typing. No annotation, no code-gen step, no cfgx import in this package.

---

## Step 6: Validate in CI

```sh
cfgx validate --env --schema cmd/schema.go
# or with a file:
cfgx validate --config /etc/myapp/config.yaml --schema cmd/schema.go
```

Exit code 0 = valid. Exit code 1 = validation errors printed to stderr. Exit code 2 = tool error.

---

## Step 7: Adding a new config

```go
// In cmd/schema.go, add one entry:
cfgx.Duration("graceful-shutdown-timeout").
    Flag("--shutdown-timeout").Env("APP_SHUTDOWN_TIMEOUT").Default(30*time.Second).
    Validate(func(d time.Duration) error {
        if d < 0 || d > 5*time.Minute {
            return fmt.Errorf("must be between 0s and 5m")
        }
        return nil
    }).
    Scope("serve").Tags("server").
    Desc("Maximum time to wait for in-flight requests to complete on shutdown"),
```

Then:

```sh
go generate ./cmd/...
```

All generated files, `.env.example`, `config.example.yaml`, docs — everything is updated.
The `serve` command's `ServeConfig` now has a `GracefulShutdownTimeout() time.Duration` method.

---

## What does NOT change when adding a config

- `cmd/root.go` — no changes
- `cmd/serve.go` — no changes (unless you want to USE the new config)
- All business logic packages — no changes unless they need the new value
- `go.mod` — no changes

---

## Migration from an existing cobra/viper project

See [docs/migration.md](../migration.md).

Summary:
1. Add cfgx to `go.mod`
2. Create `cmd/schema.go` and move all flag/env definitions into it
3. Run `cfgx gen`
4. Replace manual viper/cobra wiring with `RegisterFlags`, `BindViper` and `Load<Scope>`
5. Move business logic onto its own interfaces
6. Delete hand-written `.env.example` and `config.example.yaml`
