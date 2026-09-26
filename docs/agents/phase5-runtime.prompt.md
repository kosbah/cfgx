# Phase 5: Runtime Package

## Goal

Implement `cfgx/runtime` — the thin runtime package imported by generated `config.gen.go`.
It provides a validator registry with built-in validators and an IsLive watcher.

The package must be lightweight: negligible startup overhead, zero allocations per watcher
tick when no changes occur.

---

## Prerequisites

- Phase 1 complete: `cfgx` DSL package exists (runtime may import cfgx types, not vice versa)
- Read `docs/design/config-anatomy.md` — IsLive, OnChange, Getter, and validator sections
- Familiarity with `sync/atomic`, `context`, `github.com/fsnotify/fsnotify`

---

## Files to create

```
runtime/validator.go       — Registry type + built-in validator constructors
runtime/watcher.go         — Watcher type for IsLive entries
runtime/runtime_test.go    — unit tests + watcher benchmark
```

---

## Dependencies

```
github.com/fsnotify/fsnotify v1.7.0  — file watching for file-backed IsLive configs
```

No other external dependencies.

---

## `runtime/validator.go`

### Registry

```go
// Registry holds named validator functions.
// The global DefaultRegistry is pre-populated with all built-in validators.
var DefaultRegistry = NewRegistry()

type Registry struct {
    mu         sync.RWMutex
    validators map[string]ValidatorFunc
}

// ValidatorFunc validates a value. The argument is always the Go zero-value type
// for the config entry (string for string entries, int for int entries, etc.).
type ValidatorFunc func(val any) error

func NewRegistry() *Registry

// Register adds a named validator. Panics if name is already registered.
func (r *Registry) Register(name string, fn ValidatorFunc)

// Get returns the validator for name, or nil if not found.
func (r *Registry) Get(name string) ValidatorFunc

// ValidateAll runs fn for each entry in entries, using the values from v.
// Returns a combined error if any validators fail.
func (r *Registry) ValidateAll(entries []ir.EntryIR, v *viper.Viper) error
```

### Built-in validators

Register all built-ins in `init()` on `DefaultRegistry`.

Implement each as a constructor function that returns `ValidatorFunc`:

```go
// Email returns a ValidatorFunc that checks for a valid RFC 5322 email address.
// Implemented using net/mail.ParseAddress.
func Email() ValidatorFunc

// URL returns a ValidatorFunc that checks for a valid URL.
// Implemented using url.Parse; scheme must be present.
func URL() ValidatorFunc

// Port returns a ValidatorFunc that checks 1 ≤ v ≤ 65535.
func Port() ValidatorFunc

// PortRange returns a ValidatorFunc that checks min ≤ v ≤ max.
// Panics if min < 1 or max > 65535 or min > max.
func PortRange(min, max int) ValidatorFunc

// Base64 returns a ValidatorFunc that checks if v is valid base64.
// Accepts both standard and URL-safe encoding.
func Base64() ValidatorFunc

// Regex returns a ValidatorFunc that checks if v matches pattern.
// Compiles pattern at construction time; panics if pattern is invalid.
func Regex(pattern string) ValidatorFunc

// Duration returns a ValidatorFunc that checks if v is parseable by time.ParseDuration.
func Duration() ValidatorFunc

// Semver returns a ValidatorFunc that checks for vX.Y.Z format.
// Accepts with or without leading "v". Patch version is required.
func Semver() ValidatorFunc

// Enum returns a ValidatorFunc that checks if v is one of the allowed values.
// Comparison is case-insensitive.
func Enum(values ...string) ValidatorFunc

// Range returns a ValidatorFunc that checks min ≤ v ≤ max for float64 values.
func Range(min, max float64) ValidatorFunc
```

---

## `runtime/watcher.go`

### WatchEntry

```go
// WatchEntry describes a single IsLive config to be watched.
// Generated code calls schema.WatcherEntries() to get these.
type WatchEntry struct {
    // Key is the viper key for this entry (e.g. "debug", "db.host").
    Key string

    // Env is the environment variable name (e.g. "APP_DEBUG").
    // If non-empty, the watcher polls os.Getenv(Env) for changes.
    Env string

    // ConfigFile is set if the entry is backed by a config file (via viper).
    // If non-empty, fsnotify watches the directory for changes.
    ConfigFile string

    // Getter is called to obtain the current value after a change is detected.
    // May be nil for simple env-var-backed entries (watcher reads directly).
    Getter func() (any, error)

    // ValuePtr is a pointer to the field in the generated Config struct.
    // The watcher writes the new value here atomically.
    ValuePtr any // *string, *int, *bool, etc.

    // OnChange is called after ValuePtr is updated.
    // old and new are the values before and after the change.
    OnChange func(old, new any)
}
```

### Watcher

```go
// Watcher monitors IsLive config entries and updates them when their source changes.
type Watcher struct {
    interval time.Duration
    entries  []WatchEntry
    mu       sync.Mutex
    cancel   context.CancelFunc
    wg       sync.WaitGroup
    fsw      *fsnotify.Watcher // nil if no file-backed entries
}

// NewWatcher creates a Watcher that polls env-var-backed entries every interval.
// A zero interval defaults to 5 * time.Second.
func NewWatcher(interval time.Duration) *Watcher

// Register adds a WatchEntry to the watcher.
// Must be called before Start.
func (w *Watcher) Register(entry WatchEntry)

// Start launches the watcher goroutine.
// ctx controls the watcher lifetime; the goroutine exits when ctx is cancelled
// or Stop is called.
// Panics if called more than once without a preceding Stop.
func (w *Watcher) Start(ctx context.Context)

// Stop cancels the watcher and waits for the goroutine to exit.
func (w *Watcher) Stop()
```

### Watcher internals

**Goroutine behaviour:**

```
loop:
  select {
  case <-ticker.C:
      for each env-backed WatchEntry:
          current := os.Getenv(entry.Env)
          if current != stored[entry.Key]:
              newVal := parseToType(current, entry)
              atomicStore(entry.ValuePtr, newVal)
              if entry.OnChange != nil:
                  entry.OnChange(stored[entry.Key], current)
              stored[entry.Key] = current
  case event := <-fsw.Events:
      // handle file change events for file-backed entries
  case <-ctx.Done():
      break loop
  }
```

**Atomic storage:**

Use `sync/atomic` where possible. For types not directly supported by `sync/atomic`,
use a `sync.RWMutex` per entry. The IsLive getter on the generated Config struct must
hold a reader lock when reading.

**File-backed entries:**

When any `WatchEntry.ConfigFile` is set, create a `fsnotify.Watcher` and watch the
file's directory. On `WRITE` or `CREATE` events, re-read the config file via viper and
update all file-backed entries.

---

## Test requirements

### Validator tests

Table-driven tests for every built-in validator:

```go
func TestEmail(t *testing.T) {
    fn := Email()
    tests := []struct{ input string; wantErr bool }{
        {"user@example.com", false},
        {"not-an-email", true},
        {"", true},
        {"user@", true},
    }
    // ...
}
```

### Watcher tests

```go
func TestWatcher_EnvChange(t *testing.T) {
    // Set an env var
    t.Setenv("TEST_WATCHER_VAL", "initial")
    
    var val atomic.Value
    val.Store("initial")
    
    changed := make(chan struct{}, 1)
    w := NewWatcher(10 * time.Millisecond) // fast for testing
    w.Register(WatchEntry{
        Key: "test",
        Env: "TEST_WATCHER_VAL",
        ValuePtr: &val,
        OnChange: func(old, new any) { changed <- struct{}{} },
    })
    
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    w.Start(ctx)
    
    t.Setenv("TEST_WATCHER_VAL", "changed")
    
    select {
    case <-changed:
        // success
    case <-ctx.Done():
        t.Fatal("watcher did not detect change within timeout")
    }
    
    w.Stop()
}
```

### Benchmark

```go
func BenchmarkWatcher_NoChange(b *testing.B) {
    // Register 10 env-var entries; env vars are unchanged
    // Benchmark one tick (poll 10 entries)
    // Expected: no allocations per tick
}
```

---

## Definition of done

- All validator tests pass
- Watcher detects env var changes within 2 tick intervals in tests
- `BenchmarkWatcher_NoChange` shows 0 allocations/op
- `go vet ./runtime/...` clean
- `go test -race ./runtime/...` passes (no data races)
