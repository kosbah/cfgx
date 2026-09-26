# Phase 6: CLI Tool

## Goal

Implement the `cfgx` binary at `cmd/cfgx/`. It is the user-facing tool that orchestrates
the generator pipeline and scaffolds new projects.

The CLI itself is built with cobra. It must be a standalone binary: install with
`go install github.com/example/cfgx/cmd/cfgx@latest`.

---

## Prerequisites

- Phase 2 complete: `gen/parser.Parse()` works
- Phase 3 complete: `gen/codegen.Generate()` works
- Phase 4 complete: all `gen/artifacts.*` generators work
- Read `docs/design/developer-experience.md` — the canonical user story; the CLI must
  match the workflow described there exactly

---

## Files to create

```
cmd/cfgx/main.go               — entry point; creates root cobra command
cmd/cfgx/cmd_root.go           — root command setup (version flag, etc.)
cmd/cfgx/cmd_init.go           — `cfgx init` subcommand
cmd/cfgx/cmd_gen.go            — `cfgx gen` subcommand
cmd/cfgx/cmd_validate.go       — `cfgx validate` subcommand
cmd/cfgx/cmd_docs.go           — `cfgx docs` subcommand
cmd/cfgx/scaffold/             — embedded scaffold templates
    templates.go               — go:embed for all template files
    templates/
        go.mod.tmpl
        main.go.tmpl
        cmd_root.go.tmpl
        cmd_serve.go.tmpl
        cmd_migrate.go.tmpl
        cmd_schema.go.tmpl
cmd/cfgx/cmd_init_test.go      — integration tests
cmd/cfgx/cmd_gen_test.go
cmd/cfgx/cmd_validate_test.go
cmd/cfgx/cmd_docs_test.go
```

---

## `cfgx init [module-path]`

### Flags

```
--dir string          target directory (default: last element of module path)
--force               overwrite existing files
--no-tidy             skip running go mod tidy after scaffolding
```

### Behaviour

1. Determine target directory from `--dir` or last path segment of `module-path`
2. Check if directory exists; if it does and `--force` is not set, error
3. Create the directory structure
4. For each template in `scaffold/templates/`, render with `text/template` and write
5. Run `go mod tidy` in the target directory (unless `--no-tidy`)
6. Print success message with next steps

### Scaffolded `cmd/schema.go.tmpl`

The scaffolded schema must:
- Import `github.com/example/cfgx`
- Include a `//go:generate cfgx gen` directive
- Define `var Schema = cfgx.Define(...)` with 3-4 example entries (port, debug, db object)
- Have rich comments explaining how to add entries

### Scaffolded `cmd/root.go.tmpl`

Must include:
- `RegisterFlags(root, Schema)` call
- `BindViper` in `cobra.OnInitialize`
- `LoadServe` called in serve command `RunE`
- A comment `// Business logic packages define their own Config interfaces`

### Idempotency

Before writing each file, check if it exists. If it does and `--force` is not set,
print `⚠ skipped cmd/root.go (already exists; use --force to overwrite)` and skip.

---

## `cfgx gen`

### Flags

```
-s, --schema string   path to schema Go file (default: auto-discover)
-o, --out-dir string  output directory for generated artifacts (default: same as schema)
-p, --pkg string      Go package name (default: derived from schema file's package)
--no-env              skip .env.example
--no-yaml             skip config.example.yaml
--no-json-schema      skip config.schema.json
--no-docs             skip docs/config.md and docs/config.1
--dry-run             print what would be generated without writing
```

### Auto-discovery

If `--schema` is not specified, search for the schema file:
1. Look for `**/schema.go` files containing `cfgx.Define`
2. If exactly one is found, use it
3. If zero or multiple are found, error with a helpful message

### Behaviour

```
1. Parse schema file → *ir.SchemaIR
2. Generate Go code → write <out-dir>/<pkg>/config.gen.go
3. (Unless --no-env)         generate → write .env.example
4. (Unless --no-yaml)        generate → write config.example.yaml
5. (Unless --no-json-schema) generate → write config.schema.json
6. (Unless --no-docs)        generate → write docs/config.md, docs/config.1
7. Print summary:
   ✓ wrote cmd/config.gen.go      (312 lines)
   ✓ wrote .env.example           (28 lines)
   ✓ wrote config.example.yaml    (45 lines)
   ✓ wrote config.schema.json     (112 lines)
   ✓ wrote docs/config.md         (67 lines)
   ✓ wrote docs/config.1          (89 lines)
```

### Error handling

If the schema file has parse errors, print:

```
✗ parse error: cmd/schema.go:15:3: duplicate entry ID "port"
```

Exit code 1. Print all errors, not just the first.

---

## `cfgx validate`

### Flags

```
-s, --schema string    path to schema Go file
-c, --config string    path to config YAML/JSON/TOML file
-e, --env              also read from environment variables
--show-values          print resolved values (masks sensitive fields)
```

### Exit codes

| Condition | Exit code |
|---|---|
| All valid | 0 |
| Validation errors | 1 |
| Tool error (schema parse fail, file not found) | 2 |

### Output format

```
✓ app-name = "myapp"
✓ port = 8080
✗ db.name: required but not set
✗ db.password: required but not set
✗ port: value 70000 out of range 1024–65535

2 errors found.
```

With `--show-values`:
```
✓ app-name = "myapp"
✓ port = 8080
✓ db.host = "localhost"
✓ db.password = [redacted]
```

---

## `cfgx docs`

### Flags

```
-s, --schema string      path to schema Go file
-f, --format string      comma-separated: md,man,json-schema (default: md,man,json-schema)
-o, --out string         output directory (default: docs/)
```

### Behaviour

Run only the requested artifact generators and write to the output directory.

---

## Scaffold templates

Templates use `text/template` syntax. Template data is:

```go
type ScaffoldData struct {
    ModulePath string // e.g. "github.com/yourorg/myapp"
    AppName    string // e.g. "myapp" (last path segment)
    GoVersion  string // e.g. "1.21" (from runtime)
    Year       int    // current year
}
```

The `cmd/schema.go.tmpl` template generates a schema that demonstrates:
- One global Bool ("debug")
- One scoped Int ("port") with Scope("serve")
- One scoped Object ("db") with Scope("serve","migrate")

---

## Test requirements

### Integration tests

Each test creates a temp directory and runs the command.

```go
func TestInit_CreatesProject(t *testing.T) {
    dir := t.TempDir()
    err := runCLI(t, "init", "--dir", dir, "github.com/test/myapp")
    require.NoError(t, err)
    
    // Assert files exist
    assert.FileExists(t, filepath.Join(dir, "go.mod"))
    assert.FileExists(t, filepath.Join(dir, "main.go"))
    assert.FileExists(t, filepath.Join(dir, "cmd", "schema.go"))
    
    // Assert go.mod has correct module path
    content := readFile(t, filepath.Join(dir, "go.mod"))
    assert.Contains(t, content, "github.com/test/myapp")
}

func TestGen_ProducesValidGo(t *testing.T) {
    dir := t.TempDir()
    copyFile(t, "testdata/schemas/full.go", filepath.Join(dir, "cmd", "schema.go"))
    
    err := runCLI(t, "gen", "--schema", filepath.Join(dir, "cmd/schema.go"), "--out-dir", dir)
    require.NoError(t, err)
    
    // Assert config.gen.go was written
    assert.FileExists(t, filepath.Join(dir, "cmd", "config.gen.go"))
    
    // Assert all artifacts were written
    assert.FileExists(t, filepath.Join(dir, ".env.example"))
    assert.FileExists(t, filepath.Join(dir, "config.example.yaml"))
}

func TestValidate_DetectsErrors(t *testing.T) {
    // Run with a config that is missing a required field
    // Assert exit code 1 and the error message contains the field path
}

func TestValidate_AcceptsValidConfig(t *testing.T) {
    // Run with a complete, valid config file
    // Assert exit code 0
}
```

---

## Definition of done

- `cfgx init` creates a compilable project (`go build ./...` in the scaffolded dir succeeds)
- `cfgx gen` on `testdata/schemas/full.go` writes all 6 output files
- `cfgx validate` exits 1 with descriptive errors on an invalid config
- `cfgx validate` exits 0 on a valid config
- All integration tests pass
- `go vet ./cmd/cfgx/...` clean
