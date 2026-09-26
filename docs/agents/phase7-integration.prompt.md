# Phase 7: Integration Tests & Example Project

## Goal

Build the `examples/webapp` project — a realistic cobra application that uses the full
cfgx workflow. Write end-to-end tests that run the entire pipeline (gen → compile →
validate → load). Benchmark startup time.

---

## Prerequisites

- Phases 1–6 all complete
- Read `docs/design/developer-experience.md` — the example project described there
  is exactly what `examples/webapp` should look like

---

## Files to create

```
examples/webapp/
    README.md                      — how to build and run the example
    go.mod                         — module github.com/example/cfgx/examples/webapp
    main.go
    cmd/
        root.go
        serve.go
        migrate.go
        schema.go                  — the full schema from developer-experience.md
        config.gen.go              — pre-generated; updated by go generate
    pkg/
        server/
            server.go              — defines server.Config interface; zero cfgx import
        database/
            database.go            — defines database.Config interface; zero cfgx import
    testdata/
        valid.env                  — a complete, valid .env file for testing
        invalid_missing.env        — missing required fields
        invalid_types.env          — wrong types

_test/e2e/
    e2e_test.go                    — end-to-end tests (see below)
    benchmark_test.go              — startup benchmarks
```

---

## `examples/webapp` specification

### `cmd/schema.go`

Must include all of the following to exercise the full feature set:
- A global `Bool("debug")` with IsLive and OnChange
- A scoped `Int("port")` with PortRange validator
- A scoped `Object("db")` with a Computed "dsn" field and a Sensitive password
- An `Object("smtp")` with DependsOn("email-enabled", true)
- A `MutuallyExclusive` constraint
- At least one Deprecated entry

### `pkg/server/server.go`

```go
// Package server implements the HTTP server.
// It defines its own Config interface — no cfgx import.
package server

import "context"

// Config is the interface the server expects. The cfgx-generated ServeConfig
// satisfies this interface automatically via Go's structural typing.
type Config interface {
    Port() int
    TLSEnabled() bool
    TLSCertPath() string
}

// DBConfig is the database config interface the server uses.
type DBConfig interface {
    Host() string
    DSN() string
}

// Server is a minimal HTTP server stub.
type Server struct {
    cfg   Config
    dbCfg DBConfig
}

func New(cfg Config, db DBConfig) *Server {
    return &Server{cfg: cfg, dbCfg: db}
}

func (s *Server) Run(ctx context.Context) error {
    // stub: just log and return
    return nil
}
```

### `pkg/database/database.go`

```go
// Package database implements database access.
// It defines its own Config interface — no cfgx import.
package database

// Config is the database config interface. The cfgx-generated DbConfig
// satisfies this interface via Go's structural typing.
type Config interface {
    Host() string
    Port() int
    Name() string
    Password() string
    DSN() string
}

// DB is a minimal database stub.
type DB struct{ cfg Config }

func New(cfg Config) *DB { return &DB{cfg: cfg} }
```

### `cmd/serve.go`

```go
func newServeCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "serve",
        Short: "Start the HTTP server",
        RunE: func(cmd *cobra.Command, args []string) error {
            cfg, err := LoadServe(Schema, viper.GetViper())
            if err != nil {
                return err
            }
            db := database.New(cfg.Db)    // cfg.Db satisfies database.Config
            srv := server.New(cfg, db)    // cfg satisfies server.Config
            return srv.Run(cmd.Context())
        },
    }
}
```

---

## End-to-end tests (`_test/e2e/e2e_test.go`)

> Run with `go test ./... -tags e2e` (use build tag to separate from unit tests)

### Test 1: gen produces valid, compilable Go

```go
func TestGen_ProducesCompilableOutput(t *testing.T) {
    // Copy examples/webapp/cmd/schema.go to a temp dir
    // Run: cfgx gen --schema <tmpdir>/schema.go --pkg cmd --out-dir <tmpdir>
    // Assert exit code 0
    // Assert config.gen.go was written
    // Run: go build <tmpdir>/cmd
    // Assert exit code 0
}
```

### Test 2: validate accepts valid config

```go
func TestValidate_AcceptsValidConfig(t *testing.T) {
    // Load examples/webapp/testdata/valid.env
    // Run: cfgx validate --schema ... --env (with env vars from valid.env)
    // Assert exit code 0
}
```

### Test 3: validate rejects missing required fields

```go
func TestValidate_RejectsMissingRequired(t *testing.T) {
    // Load examples/webapp/testdata/invalid_missing.env
    // Run: cfgx validate --schema ... --env
    // Assert exit code 1
    // Assert output mentions "db.name" and "db.password"
}
```

### Test 4: Load() correctly populates all fields

```go
func TestLoad_PopulatesAllFields(t *testing.T) {
    // Set up viper with known values
    v := viper.New()
    v.Set("port", 9090)
    v.Set("db.host", "testhost")
    v.Set("db.name", "testdb")
    v.Set("db.password", "secret")
    v.Set("db.user", "admin")
    
    cfg, err := LoadServe(Schema, v)
    require.NoError(t, err)
    
    assert.Equal(t, 9090, cfg.Port())
    assert.Equal(t, "testhost", cfg.Db.Host())
    assert.Equal(t, "testdb", cfg.Db.Name())
    assert.Equal(t, "[redacted]", cfg.Db.Password().String()) // Sensitive
    assert.Contains(t, cfg.Db.DSN(), "testhost")              // Computed
}
```

### Test 5: IsLive watcher detects env var change

```go
func TestIsLive_DetectsChange(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping watcher test in -short mode")
    }
    
    t.Setenv("APP_DEBUG", "false")
    
    v := viper.New()
    v.AutomaticEnv()
    cfg, err := LoadServe(Schema, v)
    require.NoError(t, err)
    require.False(t, cfg.Debug())
    
    changed := make(chan bool, 1)
    // Capture the OnChange callback
    // (This requires the generated code to expose a way to inject the callback in tests,
    //  or the test modifies Schema.debug.onChangeFn before calling Load)
    
    t.Setenv("APP_DEBUG", "true")
    
    // Wait up to 3 watcher intervals
    select {
    case val := <-changed:
        assert.True(t, val)
    case <-time.After(150 * time.Millisecond): // 3 × 50ms interval
        t.Fatal("IsLive watcher did not detect change")
    }
}
```

---

## Benchmarks (`_test/e2e/benchmark_test.go`)

```go
// BenchmarkLoad measures the time to call LoadServe() from a pre-populated viper.
// Baseline comparison: manually reading the same fields directly from viper.
func BenchmarkLoad_cfgx(b *testing.B) {
    v := setupViper()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := LoadServe(Schema, v)
        if err != nil { b.Fatal(err) }
    }
}

func BenchmarkLoad_baseline(b *testing.B) {
    v := setupViper()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        // Manually read the same fields as LoadServe
        _ = v.GetInt("port")
        _ = v.GetBool("debug")
        _ = v.GetString("db.host")
        _ = v.GetString("db.password")
        // ...
    }
}
```

**Expected result:** cfgx overhead ≤ 2× baseline for a schema with 10 entries.

---

## `examples/webapp/README.md`

Must include:
- Prerequisites (Go 1.21+, cfgx CLI installed)
- `go generate ./cmd/...` step
- `go build ./...` step
- How to run with a config: `APP_PORT=9090 DB_NAME=mydb DB_PASSWORD=secret ./webapp serve`
- A note that `pkg/server` and `pkg/database` import zero cfgx code
- Link to the cfgx documentation

---

## Definition of done

- `cd examples/webapp && go build ./...` succeeds
- All e2e tests pass: `go test ./... -tags e2e`
- `go test -race ./... -tags e2e` passes
- Benchmark shows cfgx overhead ≤ 2× baseline
- `pkg/server/server.go` and `pkg/database/database.go` contain zero imports of cfgx or viper
  (verify with `grep -r "cfgx\|viper" examples/webapp/pkg/` returning nothing)
