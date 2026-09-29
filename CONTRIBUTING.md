# Contributing to cfgx

## Prerequisites

- Go 1.21 or newer (CI tests 1.21, 1.22 and 1.23)
- git

Everything else (golangci-lint, the cfgx CLI) runs through `go run` or is built by the tests,
so there's nothing else to install.

---

## Setup

```sh
git clone https://github.com/epistemer/cfgx
cd cfgx
go mod download
go build ./...
```

The repository holds two Go modules:

- the root module, `github.com/epistemer/cfgx`: the library, generators and CLI;
- `examples/webapp`, a separate module that uses cfgx through
  `replace github.com/epistemer/cfgx => ../..`. Run its commands from inside
  `examples/webapp`.

To try the CLI on your own project, install it from the checkout:

```sh
go install ./cmd/cfgx
```

---

## Testing

```sh
# Unit tests
go test ./...

# With the race detector (run this before opening a PR)
go test -race -count=1 ./...

# End-to-end tests: builds the cfgx CLI, runs cfgx gen / go build / cfgx validate
# against examples/webapp, and runs the example's own e2e tests
go test -tags e2e -race -count=1 ./_test/e2e/...

# The example's e2e tests and benchmark on their own
cd examples/webapp
go test -tags e2e -race ./...
go test -tags e2e -run xxx -bench . -benchmem ./cmd
```

### Golden files

`gen/codegen` and `gen/artifacts` compare their output with golden files under
`testdata/golden/`. After an intended change to a generator, regenerate them and review the
diff before committing:

```sh
go test ./gen/... -update
git diff testdata/golden
```

CI runs the same command and fails if it changes any file.

### The example project

`examples/webapp` is regenerated from its schema with `go generate`, which runs this
checkout's CLI via `go run`:

```sh
cd examples/webapp
go generate ./cmd/...
go build ./...
go vet ./...
git diff --exit-code -- . ':!docs/config.1'   # docs/config.1 carries today's date
```

---

## Lint

CI pins golangci-lint **v1.64.8**, because `.golangci.yml` uses the v1 config format
(golangci-lint v2 rejects it). Run the same version locally:

```sh
go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run ./...
```

Also make sure these are clean:

```sh
gofmt -l .
go vet ./...
```

Generated `config.gen.go` files are excluded from linting; `_test.go` files are exempt from
`errcheck` and `godot`.

---

## Pull request checklist

- [ ] `gofmt -l .` prints nothing and `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes
- [ ] `go test -tags e2e -race -count=1 ./_test/e2e/...` passes
- [ ] `go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run ./...` is clean
- [ ] Golden files are up to date: `go test ./gen/... -update` leaves no diff
- [ ] `cd examples/webapp && go generate ./cmd/... && go build ./...` passes and leaves no
      diff except the date line in `docs/config.1`
- [ ] `grep -r "cfgx\|viper" examples/webapp/pkg/` prints nothing
- [ ] New exported symbols have doc comments; every package has a package comment
- [ ] `README.md` and `docs/` describe the code as it is after your change
- [ ] `docs/design/` is updated if you changed the SchemaIR or the config anatomy

---

## Adding a new config property

A new entry property (e.g. `.NewProp()`) touches several packages:

1. [ ] Add the field to the entry type in `entry.go`
2. [ ] Add the builder method, returning the receiver for chaining
3. [ ] Add the field to `ir.EntryIR` in `internal/ir/schema_ir.go`
4. [ ] Populate it in the entry's `toIR()`
5. [ ] Extract it from the AST in `gen/parser/parser.go`
6. [ ] Update `gen/codegen` (template or code) if it affects the generated Go
7. [ ] Update `gen/artifacts` if it affects the example files or docs
8. [ ] Update `runtime.ValidateSchema` if it affects static validation
9. [ ] Add tests in each changed package
10. [ ] Exercise it in `testdata/schemas/full.go`
11. [ ] Regenerate golden files: `go test ./gen/... -update`
12. [ ] Update `docs/design/config-anatomy.md` and `docs/design/schema-ir-spec.md`
13. [ ] Regenerate and build `examples/webapp`

---

## Adding a new built-in validator

1. Implement it once in `internal/validate/validate.go`
2. Add the typed DSL constructor in `validators.go` (root package)
3. Add the `runtime.ValidatorFunc` constructor in `runtime/validator.go`, and register it on
   `DefaultRegistry` (parameterless) or build it from params in `ValidateAll` (parameterized)
4. Map the constructor call to a `ValidatorIR` in `gen/parser/parser.go`
5. Add the JSON Schema mapping in `gen/artifacts/jsonschema.go`, if there is one
6. Document the name and params in `docs/design/schema-ir-spec.md`
7. Add tests, including one that the DSL and runtime versions agree

---

## Code style

- `gofmt`/`goimports` formatting, with `github.com/epistemer/cfgx` imports grouped last
- Errors are returned, not panicked; panics are reserved for programmer errors in DSL
  constructors (for example, an invalid regex passed to `cfgx.Regex`)
- Every exported symbol has a doc comment ending with a period
- Tests are table-driven where it helps; generators use golden files; `-race` must pass

---

## Architecture

Read [docs/design/architecture.md](docs/design/architecture.md) before making cross-package
changes. The invariants that are easiest to break:

1. `SchemaIR` stays JSON-serializable: no closures, only `Has*` flags for them.
2. The root `cfgx` package depends only on the standard library.
3. `examples/webapp/pkg/` never imports cfgx or viper; business logic depends on its own
   interfaces.
4. Generated code is deterministic: the same schema always produces the same bytes.
