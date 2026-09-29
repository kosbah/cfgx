# webapp — cfgx example project

A realistic cobra/viper application demonstrating the full cfgx workflow:
one schema (`cmd/schema.go`) → `cfgx gen` → typed, validated config for each
command.

## Commands

- `webapp serve` — start the HTTP server (stub)
- `webapp migrate` — run database migrations (stub)

## What the schema shows

`cmd/schema.go` declares every setting with `cfgx.Define()` and exercises the
whole feature set:

| Feature | Entry |
|---|---|
| Global, hot-reloadable (`IsLive` + `OnChange`) | `debug` (`APP_DEBUG`) |
| Scoped to `serve`, with `PortRange` validator | `port` (`APP_PORT`, `--port`/`-p`) |
| Scoped object (`serve` + `migrate`) with a `Computed` field and a `Sensitive` field | `db` (`db.dsn`, `db.password`) |
| Conditionally required (`DependsOn`) | `tls-cert-path` (when `tls-enabled=true`), the `smtp` object (when `email-enabled=true`) |
| `MutuallyExclusive` constraint | `json-output` / `text-output` |
| `Deprecated` entry | `log-file` |

Each command validates only what it uses: `LoadMigrate` does not fail on
serve-only settings such as `tls-cert-path`.

## Prerequisites

- Go 1.21+
- The cfgx CLI (optional — `go generate` runs it from this repository via
  `go run`):
  ```sh
  go install github.com/example/cfgx/cmd/cfgx@latest
  ```

`go.mod` replaces `github.com/example/cfgx` with the repository root
(`../..`), so the example always builds against the local cfgx sources.

## Getting started

1. Generate the configuration code and artifacts (run after every change to
   `cmd/schema.go`):
   ```sh
   go generate ./cmd/...
   ```
   This writes `cmd/config.gen.go`, `.env.example`, `config.example.yaml`,
   `config.schema.json`, `docs/config.md` and `docs/config.1`. The generated
   files are committed; do not edit them by hand.

2. Build:
   ```sh
   go build ./...
   go build -o webapp .
   ```

3. Run with a config — `db.name` and `db.password` are required:
   ```sh
   APP_PORT=9090 DB_NAME=mydb DB_PASSWORD=secret ./webapp serve
   DB_NAME=mydb DB_PASSWORD=secret ./webapp migrate
   ```
   Missing or invalid values are reported with their field paths:
   ```text
   $ ./webapp serve
   Error: 2 config validation errors: db.name: required; db.password: required
   ```
   Values can also come from flags (`./webapp serve --port 9090`) or a config
   file (`./webapp serve --config config.yaml`; see `config.example.yaml`).

4. Validate a configuration without running the app:
   ```sh
   cfgx validate --schema cmd/schema.go --env           # environment
   cfgx validate --schema cmd/schema.go --config config.yaml
   ```

## Business logic imports zero cfgx code

`pkg/server` and `pkg/database` define their own `Config` interfaces
(`Port() int`, `DSN() string`, ...). The generated `*ServeConfig` and
`*DbConfig` satisfy them through Go's structural typing, so these packages
import neither cfgx nor viper:

```sh
grep -r "cfgx\|viper" pkg/
# prints nothing
```

## Tests and benchmark

The end-to-end tests carry the `e2e` build tag:

```sh
go test -tags e2e -race ./...                          # Load, scopes, DependsOn, IsLive
go test -tags e2e -run xxx -bench . -benchmem ./cmd    # LoadServe vs. plain viper reads
```

From the repository root, `go test -tags e2e ./_test/e2e/...` additionally
builds the cfgx CLI and runs `cfgx gen` and `cfgx validate` against this
project.

## Documentation

- Generated configuration reference: [docs/config.md](docs/config.md)
- cfgx: [README](../../README.md) and
  [developer experience walkthrough](../../docs/design/developer-experience.md)
