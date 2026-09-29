# Migrating an existing cobra/viper project to cfgx

This guide moves a working cobra/viper application to cfgx one step at a time. Each step
leaves the program building and running, so you can commit as you go.

It assumes the common layout: a `cmd` package with a root command and subcommands, flags
declared in `init()` or a constructor, `viper.BindPFlag` / `viper.BindEnv` calls, and
`viper.GetString("...")` reads spread through the commands and business logic.

The running example is a small service with a `serve` and a `migrate` command.

---

## Before you start

- Go 1.21+, cobra v1.8+ and viper v1.19+.
- Write down where every setting comes from today. For each one you'll need its viper key,
  flag, env var, default, whether it's required, and which commands use it. `grep -rn
  'viper\.\(Get\|Bind\|SetDefault\)\|Flags()\.' .` finds most of them.
- Get the tests green first, so you can tell a migration mistake from an old bug.

---

## Step 1: Add the dependency and the CLI

```sh
go get github.com/epistemer/cfgx@latest
go install github.com/epistemer/cfgx/cmd/cfgx@latest
```

> Until the initial PRs land in `epistemer/cfgx`, the module can't be fetched. Clone the
> repository next to your project, point your `go.mod` at it and install the CLI from it:
>
> ```sh
> go mod edit -require github.com/epistemer/cfgx@v0.0.0 -replace github.com/epistemer/cfgx=../cfgx
> (cd ../cfgx && go install ./cmd/cfgx)
> ```

---

## Step 2: Write the schema from your existing flags and env vars

Create `cmd/schema.go` in the package that holds your commands. Suppose the hand-written
code looks like this:

```go
// cmd/root.go (before)
func init() {
    rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
    viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
    viper.BindEnv("debug", "APP_DEBUG")
}

// cmd/serve.go (before)
func init() {
    serveCmd.Flags().IntP("port", "p", 8080, "HTTP port")
    viper.BindPFlag("port", serveCmd.Flags().Lookup("port"))
    viper.BindEnv("port", "APP_PORT")

    viper.BindEnv("db.host", "DB_HOST")
    viper.SetDefault("db.host", "localhost")
    viper.BindEnv("db.port", "DB_PORT")
    viper.SetDefault("db.port", 5432)
    viper.BindEnv("db.name", "DB_NAME")
    viper.BindEnv("db.password", "DB_PASSWORD")
}
```

Translate each setting into one builder chain:

```go
// cmd/schema.go
package cmd

//go:generate cfgx gen

import "github.com/epistemer/cfgx"

var Schema = cfgx.Define(
    cfgx.Bool("debug").
        Flag("--debug").Env("APP_DEBUG").Default(false).
        Desc("Enable debug logging"),

    cfgx.Int("port").
        Flag("--port", "-p").Env("APP_PORT").Default(8080).
        Validate(cfgx.PortRange(1024, 65535)).
        Desc("HTTP port").
        Scope("serve"),

    cfgx.Object("db",
        cfgx.String("host").Env("DB_HOST").Default("localhost"),
        cfgx.Int("port").Env("DB_PORT").Default(5432).Validate(cfgx.Port()),
        cfgx.String("name").Env("DB_NAME").Required(),
        cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required(),
    ).Scope("serve", "migrate"),
)
```

How the pieces map:

| Hand-written | Schema |
|---|---|
| viper key `port` | entry ID `"port"`; `db.host` becomes `host` inside `cfgx.Object("db", ...)` |
| `Flags().IntP("port", "p", 8080, "...")` | `.Flag("--port", "-p").Default(8080).Desc("...")` |
| `viper.BindEnv("port", "APP_PORT")` | `.Env("APP_PORT")` |
| `viper.SetDefault(...)` | `.Default(...)` (type-checked against the entry type) |
| `if viper.GetString("db.name") == "" { return err }` | `.Required()` |
| Hand-rolled range, URL, email or enum checks | `.Validate(cfgx.PortRange(...))`, `cfgx.URL()`, `cfgx.Email()`, `cfgx.Enum(...)`, or your own `func(T) error` |
| "Only needed when TLS is on" | `.Required().DependsOn("tls-enabled", true)` |
| A setting that only `serve` reads | `.Scope("serve")` |
| Secrets you mask in logs by hand | `.Sensitive()` |
| Values derived from other settings (DSNs, URLs) | `cfgx.Computed("dsn", func(parent any) string { ... })` |
| Values read from files, vaults or other code | `.Getter(func() (T, error) { ... })` |

Keep the IDs equal to your current viper keys. Config files, env vars and flags then keep
working unchanged, and the generated `config.example.yaml` matches the files you already
ship.

The parser reads the schema statically, so defaults, flags, env names and validator arguments
must be literals. It stops at the first error; fix it and rerun.

---

## Step 3: Generate

```sh
go generate ./cmd/...     # or: cfgx gen
```

This writes `cmd/config.gen.go` next to the schema and the artifacts in the module root:
`.env.example`, `config.example.yaml`, `config.schema.json`, `docs/config.md` and
`docs/config.1`. Pass `--no-env`, `--no-yaml`, `--no-json-schema` or `--no-docs` to skip
any of them.

The generated file declares these package-level names in your `cmd` package: `Config`,
`<Scope>Config` (for example `ServeConfig`), `<Object>Config` (for example `DbConfig`),
`RegisterFlags`, `BindViper`, `Load` and `Load<Scope>`, plus unexported helpers prefixed
`cfgx`. Rename any hand-written identifiers that clash before you build.

At this point nothing uses the generated code yet; `go build ./...` should still pass.

---

## Step 4: Replace the flag and viper wiring

Delete the hand-written `Flags()...`, `viper.BindPFlag`, `viper.BindEnv` and
`viper.SetDefault` calls for every setting that's now in the schema, and call the generated
functions instead:

```go
// cmd/root.go (after)
var cfgFile string

func NewRootCmd() *cobra.Command {
    root := &cobra.Command{Use: "myapp", SilenceUsage: true}
    root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")

    RegisterFlags(root, Schema) // every schema flag, as a persistent flag on root
    cobra.OnInitialize(func() {
        if cfgFile != "" {
            viper.SetConfigFile(cfgFile)
            cobra.CheckErr(viper.ReadInConfig())
        }
        BindViper(viper.GetViper(), Schema) // flags, env vars and defaults
    })

    root.AddCommand(newServeCmd(), newMigrateCmd())
    return root
}
```

Then replace the scattered `viper.Get*` reads with one typed, validated load per command:

```go
// cmd/serve.go (after)
func newServeCmd() *cobra.Command {
    return &cobra.Command{
        Use: "serve",
        RunE: func(cmd *cobra.Command, _ []string) error {
            cfg, err := LoadServe(Schema, viper.GetViper())
            if err != nil {
                return err // lists every invalid or missing value with its key
            }

            log.Printf("listening on :%d", cfg.Port())
            return run(cmd.Context(), cfg)
        },
    }
}
```

Things to know:

- `RegisterFlags` registers every flag as a **persistent flag on the command you pass**, so
  a flag that used to live only on `serve` now also shows up in `migrate --help`. The value
  is only read and validated by the commands in the entry's `Scope`.
- `LoadServe` validates only the entries `serve` can see: unscoped entries plus those whose
  `Scope` includes `"serve"`. `migrate` doesn't fail because a serve-only value is missing.
- Commands without a scope of their own use `Load`, which returns the shared `*Config`.
- If you mark entries `.IsLive()`, the load functions also start a background watcher that
  keeps those values current, and the generated config gets a `Close` method. Call
  `defer cfg.Close()` right after loading to stop it. Without IsLive entries there is no
  `Close` method.

Run the program with your usual flags, env vars and config files and compare the behavior.
`go run . serve --help` should list the same flags as before.

---

## Step 5: Move business logic onto interfaces

Business logic that calls `viper.GetString` or takes a hand-written config struct should
declare the small interface it needs instead:

```go
// pkg/database/database.go
package database

type Config interface {
    Host() string
    Port() int
    Name() string
    Password() string
}

func Open(ctx context.Context, cfg Config) (*DB, error) { /* ... */ }
```

The generated `*DbConfig` has exactly these methods, so the call site is just
`database.Open(ctx, cfg.Db())`. No adapter is needed, and the package no longer imports
viper or cfgx. In tests, any small struct with those methods works as a config.

Check that nothing slipped through:

```sh
grep -rn 'spf13/viper\|epistemer/cfgx' pkg/   # should print nothing
```

---

## Step 6: Delete the hand-written example files

The generated `.env.example`, `config.example.yaml` and `docs/config.md` now describe every
setting. Delete your old hand-maintained versions (or let the generated ones overwrite
them) and point your README at the generated files.

Commit the generated files. From now on, adding a setting means adding one builder call to
`cmd/schema.go` and running `go generate ./cmd/...`.

---

## Step 7: Validate in CI (optional)

`cfgx validate` checks a config file and/or the environment against the schema without
running the program:

```sh
cfgx validate --config deploy/production.yaml --env
```

It exits with `1` on validation errors and `2` on tool errors. It isn't scope-aware: it
reports every required entry, including ones only another command uses. It also can't run
custom `Validate` closures or Getters, so the generated `Load<Scope>` functions remain the
final check.

---

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `flag redefined: port` | A hand-written flag with the same name is still registered. Delete it; `RegisterFlags` owns it now. |
| A value from the config file is ignored | The entry ID doesn't match the key in the file. Nested keys are `object.id` (for example `db.host`). |
| `cfgx gen` reports a parse error | A builder argument isn't a literal, or the file doesn't contain exactly one `cfgx.Define(...)` call. The error has `file:line:col`. |
| `Required` fails for a value set to `0`, `false` or `""` | `Required` treats the zero value as missing. Use a `Default` instead, or a custom `Validate`. |
| `cfgx gen` finds several schemas | Pass `--schema path/to/schema.go`. |
