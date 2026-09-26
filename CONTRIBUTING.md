# Contributing to cfgx

## Prerequisites

- Go 1.21+
- `cfgx` CLI: `go install ./cmd/cfgx`
- `golangci-lint`: [installation](https://golangci-lint.run/welcome/install/)

---

## Development workflow

### Setup

```sh
git clone https://github.com/example/cfgx
cd cfgx
go mod download
```

### Build

```sh
go build ./...
```

### Test

```sh
# Unit tests
go test ./...

# With race detector (recommended before submitting a PR)
go test -race ./...

# End-to-end tests (requires cfgx CLI installed)
go install ./cmd/cfgx
go test -tags e2e ./...

# Update golden files after changing a generator
go test ./gen/... -update
```

### Lint

```sh
golangci-lint run ./...
```

---

## Adding a new config property

When adding a new entry property to the DSL (e.g. `.NewProp()`), changes are
required in multiple packages. Follow this checklist:

1. [ ] Add field to the entry struct in `entry.go`
2. [ ] Add builder method with correct return type for chaining
3. [ ] Add field to `ir.EntryIR` in `internal/ir/schema_ir.go`
4. [ ] Update `entry.toIR()` to populate the new IR field
5. [ ] Update `gen/parser/parser.go` to extract the property from AST
6. [ ] Update `gen/codegen` template/code if it affects generated Go
7. [ ] Update `gen/artifacts` generators if it affects artifact output
8. [ ] Add tests in each changed package
9. [ ] Update `testdata/schemas/full.go` fixture to exercise the new property
10. [ ] Update golden files: `go test ./gen/... -update`
11. [ ] Update `docs/design/config-anatomy.md`
12. [ ] Verify `go generate ./examples/webapp/cmd/... && cd examples/webapp && go build ./...`

---

## Adding a new built-in validator

1. Implement in `runtime/validator.go` following the existing pattern
2. Register it in `init()` on `DefaultRegistry`
3. Add the ValidatorIR name and params to `docs/design/schema-ir-spec.md`
4. Add the DSL constructor in `cfgx/entry.go` (returns a typed validator function)
5. Add the AST extraction case in `gen/parser/parser.go`
6. Add the JSON Schema mapping in `gen/artifacts/jsonschema.go`
7. Add tests

---

## Pull request checklist

- [ ] `go test -race ./...` passes
- [ ] `go test -tags e2e ./...` passes
- [ ] `golangci-lint run ./...` clean
- [ ] Golden files updated: `go test ./gen/... -update`
- [ ] `go generate ./examples/webapp/cmd/... && cd examples/webapp && go build ./...` passes
- [ ] `grep -r "cfgx\|viper" examples/webapp/pkg/` returns nothing
- [ ] New exported symbols have godoc comments
- [ ] `docs/design/` updated if you changed the SchemaIR or config anatomy

---

## Code style

- `gofmt` — all files must be formatted
- `go vet` — no issues
- Error handling: always return errors; no panics in library code
  (panics are acceptable only in DSL constructors for programmer errors)
- Comments: all exported symbols have godoc; unexported code is commented when non-obvious
- Tests: table-driven; golden files for generators; `-race` should pass

---

## Architecture

Read [docs/design/architecture.md](docs/design/architecture.md) before making
cross-package changes.

The three hardest invariants to maintain:
1. `SchemaIR` must always be JSON-serializable (no closures)
2. Generated code must never directly import `cfgx` (only `cfgx/runtime`)
3. `examples/webapp/pkg/` must never import `cfgx` or `viper`
