# Copilot Instructions for cfgx

This file provides custom instructions for GitHub Copilot (and other AI coding agents)
working in this repository.

---

## Project overview

`cfgx` is a Go library + CLI tool that provides a declarative, type-safe configuration
layer for [cobra](https://github.com/spf13/cobra) + [viper](https://github.com/spf13/viper)
applications. It solves four pain points: (1) command-driven wiring, (2) lack of type safety,
(3) boilerplate, (4) painful config additions.

**Module path:** `github.com/example/cfgx` (placeholder — update when finalized)

---

## Architecture

```
cfgx/                 — DSL (developer-facing; no external deps)
cfgx/internal/ir/     — SchemaIR (data contract between parser and generators)
cfgx/runtime/         — thin runtime (validators, IsLive watcher; fsnotify dep)
cfgx/gen/parser/      — go/ast-based schema parser
cfgx/gen/codegen/     — text/template-based Go code generator
cfgx/gen/artifacts/   — non-Go artifact generators
cfgx/cmd/cfgx/        — CLI binary
```

Read `docs/design/architecture.md` before making cross-package changes.

---

## Documentation you must read before implementing

| Task | Document |
|---|---|
| Any DSL change | `docs/design/config-anatomy.md` |
| Parser or codegen change | `docs/design/schema-ir-spec.md` |
| Full feature understanding | `docs/design/developer-experience.md` |
| Phase-specific work | `docs/agents/phase<N>-*.prompt.md` |

---

## Go version and style

- Target **Go 1.21+**. Use generics where they improve type safety (especially in the DSL).
- Follow standard Go style: `gofmt`, `go vet`, `staticcheck`.
- Use `errors.Is` / `errors.As` for error inspection; never compare error strings.
- Return errors; never panic in library code (panics are acceptable only in DSL constructors
  for programmer errors, e.g. duplicate entry ID in `cfgx.Define`).
- All exported symbols must have godoc comments.
- No init() functions except in `runtime/validator.go` for built-in registration.

---

## Testing requirements

- Every package must have tests. Aim for ≥ 80% coverage.
- Use **table-driven tests** (`[]struct{ ... }`) for all non-trivial functions.
- Generator packages (`gen/codegen`, `gen/artifacts`) must use **golden file tests**.
  - Golden files live in `testdata/golden/`
  - Use `-update` flag to regenerate: `go test ./... -update`
- Use `go test -race` to detect data races (especially important for `runtime/watcher.go`).
- Do NOT use third-party test frameworks. Use only `testing`, `testing/quick`, and if
  absolutely necessary `github.com/stretchr/testify` (but prefer standard library).

---

## Key invariants — never violate these

1. **SchemaIR has no closures.** The `internal/ir` types must be JSON-serializable. Any
   function reference is represented by a boolean sentinel (`HasValidate`, `HasOnChange`,
   `HasGetter`). The parser produces IR; the generators consume IR. No generator should
   need to inspect the original `cfgx.Schema` value.

2. **Generated code never imports cfgx directly** — only `cfgx/runtime` (for IsLive entries).
   If a generated file needs `cfgx.*`, that's an architecture violation.

3. **Business logic packages must never import cfgx or viper.** `examples/webapp/pkg/`
   contains zero cfgx/viper imports. This is enforced by a test:
   `grep -r "cfgx\|viper" examples/webapp/pkg/` must return nothing.

4. **Sensitive fields must never appear unmasked in any string representation** — not in
   `fmt.Printf`, not in `%v`, not in logs, not in JSON marshaling.

5. **The parser uses only `go/ast` — no `go run`, no compilation.** If you need a value
   that cannot be extracted statically, mark it as `HasXxx: true` and handle it at runtime.

6. **Generated Go code must pass `go/format.Source()`** before being written to disk.
   Never write unformatted Go source.

---

## Common patterns

### Adding a new config property

1. Add the property to the relevant entry struct in `entry.go`
2. Add the builder method to the entry's builder chain
3. Add the corresponding field to `ir.EntryIR` in `internal/ir/schema_ir.go`
4. Update `entry.toIR()` to populate the new IR field
5. Update `gen/parser/parser.go` to extract the new property from AST
6. Update `gen/codegen` template if the property affects generated Go code
7. Update `gen/artifacts` generators if the property affects artifact output
8. Add tests for each changed package
9. Run `go generate ./examples/webapp/cmd/...` to verify the pipeline still works

### Running the full pipeline

```sh
# Parse + generate
cfgx gen --schema examples/webapp/cmd/schema.go --pkg cmd --out-dir examples/webapp

# Verify generated code compiles
cd examples/webapp && go build ./...

# Run all tests including e2e
go test ./... -tags e2e -race -count=1
```

### Updating golden files

```sh
go test ./gen/... -update
```

---

## Dependency rules

| Package | Allowed imports |
|---|---|
| `cfgx` (root) | stdlib only |
| `internal/ir` | stdlib only |
| `gen/parser` | stdlib + `internal/ir` |
| `gen/codegen` | stdlib + `internal/ir` + `golang.org/x/tools/imports` (optional) |
| `gen/artifacts` | stdlib + `internal/ir` |
| `runtime` | stdlib + `github.com/fsnotify/fsnotify` |
| `cmd/cfgx` | stdlib + all `gen/*` packages + `cfgx` + `cobra` |

**Never add cobra or viper as imports to `cfgx` (root package) or `internal/ir`.**

---

## Error message conventions

- Errors from the parser: `"path/to/file.go:10:5: message"` (use `ParseError` type)
- Errors from `Load()`: `FieldError{Field: "db.password", Message: "required but not set"}`
- CLI errors: printed to stderr with a `✗` prefix; tool errors vs. validation errors must
  use different exit codes (1 = validation, 2 = tool)

---

## What NOT to change without discussion

- The `SchemaIR` JSON format (breaking change for any stored IR files)
- The builder method names and signatures (breaking change for users' schema files)
- The generated file structure (breaking change for users' hand-edited cobra wiring)
- The `sensitiveString` type behaviour (security invariant)
