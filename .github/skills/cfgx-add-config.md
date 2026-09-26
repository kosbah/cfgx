---
name: cfgx-add-config
description: >
  Add a new config entry to the cfgx schema DSL.
  Use when: user says "add a config for X", "I need a new setting for Y",
  "add a flag for Z", "expose ENV_VAR_NAME as a config".
---

# cfgx-add-config skill

## When to use

Invoke this skill when the user wants to add a new configuration entry to their
`cfgx.Define(...)` schema. Common triggers:
- "Add a config for X"
- "I need a new setting for Y"
- "Add a flag --foo"
- "Expose ENV_VAR as a setting"
- "Add a database timeout config"

---

## Steps

### 1. Locate the schema file

Find the file containing `cfgx.Define(`. It is typically `cmd/schema.go`.
If multiple candidates exist, ask the user which one.

### 2. Determine the entry type

Based on what the user describes, determine the entry type:

| User describes | Entry type |
|---|---|
| A flag/env that is true/false | `cfgx.Bool` |
| A number (port, timeout, count) | `cfgx.Int` or `cfgx.Float64` |
| A time duration (e.g. "30s", "5m") | `cfgx.Duration` |
| Text (name, path, URL, address) | `cfgx.String` |
| A group of related settings | `cfgx.Object` |
| A value derived from others | `cfgx.Computed` |

### 3. Determine the builder chain

Ask yourself (do NOT ask the user unless truly ambiguous):
- **Flag name:** derive from the ID (`port` → `--port`); use the user's stated name if given
- **Short flag:** add only if the user mentions it; common: `-p` for port, `-c` for config
- **Env name:** derive from the ID (`app-port` → `APP_PORT`); use the user's stated name
- **Default:** use a sensible default (e.g. `0` for port, `false` for bool, `""` for string)
- **Required:** set if the user says "required" or "must be set" or the value has no sensible default
- **Sensitive:** set if the value is a password, token, API key, secret, or private key
- **Description:** derive a clear one-liner from context; ask only if completely unclear
- **Scope:** if the schema has scopes and the config is command-specific, add `.Scope("serve")`
- **Tags:** add to an existing tag group if one fits; otherwise omit

### 4. Write the entry

Add the new entry to the `cfgx.Define(...)` call in logical order:
- After related entries (e.g. a new `db.*` field → add inside the `db` Object)
- Or at the end of its tag group

Example additions:

```go
// Adding a new string config
cfgx.String("redis-url").
    Flag("--redis-url").Env("REDIS_URL").
    Validate(cfgx.URL()).
    Desc("Redis connection URL").
    Tags("cache"),

// Adding a timeout duration
cfgx.Duration("request-timeout").
    Flag("--request-timeout").Env("APP_REQUEST_TIMEOUT").Default(30 * time.Second).
    Validate(cfgx.Range(0, 300)).
    Scope("serve").Tags("server").
    Desc("Maximum duration for a single HTTP request"),

// Adding to an existing Object
// Inside cfgx.Object("db", ...):
cfgx.Int("max-connections").Env("DB_MAX_CONNECTIONS").Default(25).
    Validate(cfgx.Range(1, 1000)).
    Desc("Maximum number of open database connections"),
```

### 5. Remind about regeneration

After making the change, always tell the user:

```
Added `cfgx.String("redis-url")` to cmd/schema.go.

Run `go generate ./cmd/...` (or `cfgx gen`) to update:
- cmd/config.gen.go  — adds RedisURL() string method
- .env.example       — adds REDIS_URL entry
- config.example.yaml
- docs/config.md
```

---

## Do NOT

- Do NOT add a config without checking if it already exists (search for the env name or ID first)
- Do NOT add duplicate IDs — they cause a panic in `cfgx.Define()`
- Do NOT set both `.Required()` and `.Default(val)` on the same entry (warn the user)
- Do NOT add a Sensitive flag without a corresponding `.Required()` (sensitive fields with
  defaults are usually a mistake)
