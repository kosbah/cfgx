---
name: cfgx-regen
description: >
  Regenerate config.gen.go and all cfgx artifacts after the schema file changes.
  Use when: user modifies schema.go, user says "regenerate", "run cfgx gen",
  "update generated files", "my config.gen.go is out of date".
---

# cfgx-regen skill

## When to use

Invoke this skill after any change to a `cfgx.Define(...)` schema file, or when
the user explicitly asks to regenerate. Common triggers:
- "Regenerate the config"
- "Run cfgx gen"
- "Update the generated files"
- "My config.gen.go is stale"
- After applying the `cfgx-add-config` skill

---

## Steps

### 1. Locate the schema file

Find the file containing `cfgx.Define(`. It is typically `cmd/schema.go`.
Check for a `//go:generate cfgx gen` directive in the file — this is the canonical
way to run generation.

### 2. Run generation

**Preferred:** use the `go generate` directive:
```sh
go generate ./cmd/...
```

**Alternative:** call cfgx gen directly:
```sh
cfgx gen --schema cmd/schema.go --pkg cmd
```

If `cfgx` is not installed:
```sh
go run github.com/epistemer/cfgx/cmd/cfgx@latest gen --schema cmd/schema.go
```

### 3. Report results

Show the user which files were written or updated. Show a concise diff of `config.gen.go`
to highlight what changed (new methods, changed defaults, etc.).

Example output to show the user:

```
Regenerated:
  ✓ cmd/config.gen.go      — added RedisURL() string method to Config
  ✓ .env.example           — added REDIS_URL entry
  ✓ config.example.yaml    — added redis-url field
  ✓ config.schema.json     — updated
  ✓ docs/config.md         — added Redis row to Cache section
  ✓ docs/config.1          — updated
```

### 4. Validate compilation

After generation, verify the generated code compiles:
```sh
go build ./...
```

If it fails, show the compile errors. Common causes:
- A `Computed` entry's function signature changed
- The package name in `--pkg` doesn't match the schema file's package declaration
- An `import` is missing in the user's non-generated code

### 5. Handle errors

If `cfgx gen` fails with a parse error:
- Show the error with file/line/column
- Locate the problematic line in the schema file
- Suggest the fix (e.g. "duplicate entry ID" → rename one of them)

---

## Checklist before marking done

- [ ] `go generate ./cmd/...` exited 0
- [ ] `go build ./...` exited 0
- [ ] `cmd/config.gen.go` is present and contains the expected new method(s)
- [ ] `.env.example` contains the expected new entry
