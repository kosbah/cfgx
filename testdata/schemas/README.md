# Test schema fixtures

This directory contains Go source files used as parser test fixtures.
Each file is a standalone Go file (not part of a buildable package) that
contains a `cfgx.Define(...)` call with various features.

## Files

| File | Purpose |
|---|---|
| `minimal.go` | One int entry with flag/env/default — the simplest valid schema |
| `full.go` | Exercises all features: all types, all properties, nested objects, constraints |
| `nested.go` | Deeply nested objects (3 levels) to test recursive parsing |
| `invalid_duplicate.go` | Duplicate entry IDs — should trigger ParseError |
| `invalid_depends.go` | DependsOn references a non-existent ID — should trigger ParseError |
| `computed.go` | Schema with Computed entries — HasGetter must be true, no Flag/Env |
| `sensitive.go` | Schema with Sensitive entries — must appear in IR with Sensitive=true |
| `deprecated.go` | Schema with Deprecated entries — message must be extracted |
| `scoped.go` | Schema with Scoped entries — scope array must be extracted |
| `live.go` | Schema with IsLive entries (env-backed, Getter-backed, list, scoped, Computed with OnChange) — codegen golden and compile tests |

## Usage in tests

```go
schema, err := parser.Parse("testdata/schemas/minimal.go")
require.NoError(t, err)
require.Equal(t, "port", schema.Entries[0].ID)
```
