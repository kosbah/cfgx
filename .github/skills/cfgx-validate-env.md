---
name: cfgx-validate-env
description: >
  Validate the current environment or a config file against the cfgx schema.
  Use when: user wants to check if their .env or config.yaml is valid,
  "validate my config", "check if my environment is correct",
  "why is my app failing to start with a config error".
---

# cfgx-validate-env skill

## When to use

Invoke this skill when the user wants to validate their configuration. Common triggers:
- "Validate my config"
- "Check if my .env is correct"
- "Why is the app failing to start with a config error"
- "Is my config.yaml valid"
- Running in CI to catch config mistakes early

---

## Steps

### 1. Determine the validation source

Ask what the user wants to validate against:
- Environment variables (`--env`)
- A YAML/JSON/TOML config file (`--config path/to/config.yaml`)
- Both (`--env --config ...`)

If neither is specified, default to `--env` (most common in development).

### 2. Locate the schema file

Find the `cfgx.Define(...)` schema file (typically `cmd/schema.go`).

### 3. Run validation

```sh
# Validate against environment variables
cfgx validate --schema cmd/schema.go --env

# Validate against a config file
cfgx validate --schema cmd/schema.go --config ./config.yaml

# Both (env overrides file, as in viper precedence)
cfgx validate --schema cmd/schema.go --config ./config.yaml --env

# Show resolved values (masks sensitive fields)
cfgx validate --schema cmd/schema.go --env --show-values
```

### 4. Interpret the results

**Exit code 0 — all valid:**
```
✓ app-name = "myapp"
✓ port = 8080
✓ db.host = "localhost"
✓ db.name = "mydb"
✓ db.password = [redacted]

All 8 config entries valid.
```

**Exit code 1 — validation errors:**
```
✗ db.name: required but not set
✗ db.password: required but not set
✗ port: value 70000 out of range 1024–65535

3 errors found.
```

For each error, help the user understand what to set:

| Error | Likely fix |
|---|---|
| `required but not set` | Set the corresponding env var or add to config file |
| `out of range` | Check the value; see `docs/config.md` for valid range |
| `invalid email` | The value doesn't look like an email address |
| `invalid url` | The value is not a valid URL (must include scheme: `https://...`) |
| `mutually exclusive` | Only one of the listed flags may be set at a time |

**Exit code 2 — tool error:**
- Schema file not found → check the `--schema` path
- Schema parse error → the schema.go file has a syntax or DSL error; fix it and re-run

### 5. For CI use

Recommend adding to CI pipeline:

```yaml
# .github/workflows/validate-config.yml
- name: Validate config schema
  run: cfgx validate --schema cmd/schema.go --env
  env:
    DB_NAME: ${{ secrets.DB_NAME }}
    DB_PASSWORD: ${{ secrets.DB_PASSWORD }}
    APP_PORT: "8080"
```

---

## Notes

- Sensitive values are never printed; they appear as `[redacted]` even with `--show-values`
- `cfgx validate` respects the same precedence as viper: flags > env > config file > defaults
- Deprecated entries generate a warning (exit code 0) but not an error
- Entries with `.DependsOn()` are only validated if the controlling entry has the required value
