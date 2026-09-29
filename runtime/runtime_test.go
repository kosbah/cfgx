package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/epistemer/cfgx"
	"github.com/epistemer/cfgx/gen/parser"
	"github.com/epistemer/cfgx/internal/ir"
)

// ---------------------------------------------------------------------------
// Validators
// ---------------------------------------------------------------------------

func TestValidators(t *testing.T) {
	tests := []struct {
		name string
		fn   ValidatorFunc
		pass []any
		fail []any
	}{
		{"Email", Email(), []any{"user@example.com", "a.b+c@d.io"}, []any{"not-an-email", "", "user@", "Bob <bob@x.com>"}},
		{"URL", URL(), []any{"https://example.com", "postgres://u@h:5432/db", "mailto:a@b.c"}, []any{"", "example.com", "/path"}},
		{"Port", Port(), []any{1, 80, 65535, "8080"}, []any{0, -1, 65536, "abc"}},
		{"PortRange", PortRange(1024, 65535), []any{1024, 8080, 65535}, []any{80, 1023, 65536}},
		{"Base64", Base64(), []any{"", "aGVsbG8=", "+/8=", "-_8="}, []any{"not base64!", "aGVsbG8"}},
		{"Regex", Regex(`^[a-z]+$`), []any{"abc"}, []any{"ABC", "a1", ""}},
		{"Duration", Duration(), []any{"30s", "1h5m", 5 * time.Second}, []any{"", "30", "soon"}},
		{"Semver", Semver(), []any{"1.2.3", "v1.2.3", "1.0.0-rc.1+build.5"}, []any{"1.2", "v1", "01.2.3", "x.y.z"}},
		{"Enum", Enum("text", "json"), []any{"text", "json", "JSON", "Text"}, []any{"", "yaml"}},
		{"Range", Range(0, 1), []any{0.0, 0.5, 1.0, 1}, []any{-0.1, 1.01, "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.pass {
				if err := tt.fn(v); err != nil {
					t.Errorf("%s(%v) = %v, want nil", tt.name, v, err)
				}
			}
			for _, v := range tt.fail {
				if err := tt.fn(v); err == nil {
					t.Errorf("%s(%v) = nil, want error", tt.name, v)
				}
			}
		})
	}
}

func TestConstructorPanics(t *testing.T) {
	tests := map[string]func(){
		"PortRange min<1":   func() { PortRange(0, 10) },
		"PortRange max":     func() { PortRange(1, 65536) },
		"PortRange min>max": func() { PortRange(9000, 8000) },
		"Regex":             func() { Regex("(") },
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn()
		})
	}
}

// TestSharedImplementation checks that cfgx and runtime validators agree.
func TestSharedImplementation(t *testing.T) {
	for _, v := range []string{"text", "JSON", "yaml", ""} {
		c, r := cfgx.Enum("text", "json")(v), Enum("text", "json")(v)
		if (c == nil) != (r == nil) || (c != nil && c.Error() != r.Error()) {
			t.Errorf("Enum(%q): cfgx=%v runtime=%v", v, c, r)
		}
	}
	for _, v := range []string{"https://x", "x", "http://"} {
		if (cfgx.URL()(v) == nil) != (URL()(v) == nil) {
			t.Errorf("URL(%q) disagrees", v)
		}
	}
}

func TestRegistry(t *testing.T) {
	for _, name := range []string{"email", "url", "port", "base64", "duration", "semver"} {
		if DefaultRegistry.Get(name) == nil {
			t.Errorf("DefaultRegistry missing %q", name)
		}
	}
	r := NewRegistry()
	if r.Get("x") != nil {
		t.Fatal("empty registry returned a validator")
	}
	r.Register("x", func(any) error { return nil })
	if r.Get("x") == nil {
		t.Fatal("Get after Register returned nil")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	r.Register("x", func(any) error { return nil })
}

// ---------------------------------------------------------------------------
// ValidateAll
// ---------------------------------------------------------------------------

type mapSource map[string]any

func (m mapSource) Get(key string) any    { return m[key] }
func (m mapSource) IsSet(key string) bool { _, ok := m[key]; return ok }
func (m mapSource) with(k string, v any) mapSource {
	out := mapSource{}
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func fullIR(t *testing.T) []ir.EntryIR {
	t.Helper()
	s, err := parser.Parse(filepath.Join("..", "testdata", "schemas", "full.go"))
	if err != nil {
		t.Fatalf("parse full.go: %v", err)
	}
	return s.Entries
}

var validFull = mapSource{
	"port":         "8080",
	"metrics_port": 9090,
	"admin_email":  "ops@example.com",
	"public_url":   "https://example.com",
	"signing_key":  "aGVsbG8=",
	"name":         "cfgx",
	"grace":        "10s",
	"version":      "v1.2.3",
	"log_format":   "JSON",
	"sample_rate":  0.5,
	"timeout":      "30s",
	"hosts":        []any{"http://a", "http://b"},
	"db.port":      5432,
	"db.password":  "secret",
}

func TestValidateAll_Valid(t *testing.T) {
	if err := ValidateAll(fullIR(t), validFull); err != nil {
		t.Fatalf("ValidateAll = %v, want nil", err)
	}
	// Unset keys are skipped.
	if err := ValidateAll(fullIR(t), mapSource{}); err != nil {
		t.Fatalf("ValidateAll(empty) = %v, want nil", err)
	}
}

func TestValidateAll_Invalid(t *testing.T) {
	bad := map[string]any{
		"port":         "80",           // port_range
		"metrics_port": 0,              // port
		"admin_email":  "nope",         // email
		"public_url":   "example.com",  // url
		"signing_key":  "!!notbase64",  // base64
		"name":         "Bad Name",     // regex
		"grace":        "soon",         // duration
		"version":      "1.2",          // semver
		"log_format":   "yaml",         // enum
		"sample_rate":  2.5,            // range
		"hosts[1]":     "no-scheme",    // url on list element
		"db.port":      70000,          // port in nested object
		"timeout":      "notaduration", // conversion failure
	}
	src := validFull
	var want []string
	for k, v := range bad {
		if k == "hosts[1]" {
			src = src.with("hosts", []any{"http://a", v})
		} else {
			src = src.with(k, v)
		}
		want = append(want, k)
	}
	err := ValidateAll(fullIR(t), src)
	var ve *cfgx.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("ValidateAll = %v, want *cfgx.ValidationErrors", err)
	}
	var got []string
	for _, fe := range ve.Errors {
		got = append(got, fe.Field)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("error keys = %v\nwant %v\nerr: %v", got, want, err)
	}
	if strings.Contains(err.Error(), "!!notbase64") {
		t.Errorf("sensitive value leaked: %v", err)
	}
}

func TestValidateAll_ParameterizedIR(t *testing.T) {
	entries := []ir.EntryIR{{
		ID:   "port",
		Type: ir.TypeInt,
		BuiltinValidators: []ir.ValidatorIR{{
			Name:   "port_range",
			Params: map[string]any{"min": 1024, "max": 65535},
		}},
	}}
	err := ValidateAll(entries, mapSource{"port": "80"})
	if err == nil || !strings.Contains(err.Error(), "port:") {
		t.Fatalf("ValidateAll = %v, want error naming key port", err)
	}
	// JSON-decoded params (float64 and []any) are accepted too.
	entries = []ir.EntryIR{
		{ID: "p", Type: ir.TypeInt, BuiltinValidators: []ir.ValidatorIR{{Name: "port_range", Params: map[string]any{"min": 1024.0, "max": 2048.0}}}},
		{ID: "f", Type: ir.TypeString, BuiltinValidators: []ir.ValidatorIR{{Name: "enum", Params: map[string]any{"values": []any{"a", "b"}}}}},
		{ID: "r", Type: ir.TypeFloat64, BuiltinValidators: []ir.ValidatorIR{{Name: "range", Params: map[string]any{"min": 0.0, "max": 1.0}}}},
		{ID: "x", Type: ir.TypeString, BuiltinValidators: []ir.ValidatorIR{{Name: "regex", Params: map[string]any{"pattern": "^x+$"}}}},
	}
	if err := ValidateAll(entries, mapSource{"p": 1500, "f": "B", "r": "0.3", "x": "xx"}); err != nil {
		t.Fatalf("ValidateAll(json params) = %v, want nil", err)
	}
	// Bad params and unknown names are reported, not panics.
	entries = []ir.EntryIR{
		{ID: "a", Type: ir.TypeInt, BuiltinValidators: []ir.ValidatorIR{{Name: "port_range", Params: map[string]any{"min": 0, "max": 10}}}},
		{ID: "b", Type: ir.TypeString, BuiltinValidators: []ir.ValidatorIR{{Name: "nope"}}},
	}
	var ve *cfgx.ValidationErrors
	if err := ValidateAll(entries, mapSource{"a": 5, "b": "x"}); !errors.As(err, &ve) || len(ve.Errors) != 2 {
		t.Fatalf("ValidateAll(bad params) = %v, want 2 errors", err)
	}
}

// ---------------------------------------------------------------------------
// Watcher
// ---------------------------------------------------------------------------

type change struct{ old, new any }

func startWatcher(t *testing.T, w *Watcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	t.Cleanup(func() {
		w.Stop()
		cancel()
	})
}

func waitChange(t *testing.T, ch <-chan change) change {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not detect change within timeout")
		return change{}
	}
}

func noChange(t *testing.T, ch <-chan change, d time.Duration) {
	t.Helper()
	select {
	case c := <-ch:
		t.Fatalf("unexpected OnChange(%v, %v)", c.old, c.new)
	case <-time.After(d):
	}
}

func TestWatcher_EnvChange(t *testing.T) {
	t.Setenv("TEST_WATCHER_VAL", "a")
	var val atomic.Value
	val.Store("a")
	ch := make(chan change, 4)
	w := NewWatcher(10 * time.Millisecond)
	w.Register(WatchEntry{
		Key:      "test",
		Env:      "TEST_WATCHER_VAL",
		ValuePtr: &val,
		OnChange: func(old, new any) { ch <- change{old, new} },
	})
	startWatcher(t, w)

	os.Setenv("TEST_WATCHER_VAL", "b")
	c := waitChange(t, ch)
	if c.old != "a" || c.new != "b" {
		t.Errorf("OnChange(%v, %v), want (a, b)", c.old, c.new)
	}
	if got := val.Load(); got != "b" {
		t.Errorf("value = %v, want b", got)
	}
	noChange(t, ch, 50*time.Millisecond)
}

func TestWatcher_DetectsWithinTwoTicks(t *testing.T) {
	const tick = 100 * time.Millisecond
	t.Setenv("TEST_WATCHER_FAST", "a")
	var val atomic.Value
	val.Store("a")
	ch := make(chan change, 1)
	w := NewWatcher(tick)
	w.Register(WatchEntry{Env: "TEST_WATCHER_FAST", ValuePtr: &val, OnChange: func(o, n any) { ch <- change{o, n} }})
	startWatcher(t, w)
	os.Setenv("TEST_WATCHER_FAST", "b")
	start := time.Now()
	waitChange(t, ch)
	if el := time.Since(start); el > 2*tick+50*time.Millisecond {
		t.Errorf("change detected after %v, want within 2 ticks (%v)", el, 2*tick)
	}
}

func TestWatcher_TypedEnv(t *testing.T) {
	t.Setenv("TEST_WATCHER_INT", "8080")
	t.Setenv("TEST_WATCHER_DUR", "1s")
	var port, dur atomic.Value
	port.Store(8080)
	dur.Store(time.Second)
	ch := make(chan change, 4)
	w := NewWatcher(10 * time.Millisecond)
	w.Register(WatchEntry{Key: "port", Env: "TEST_WATCHER_INT", ValuePtr: &port, OnChange: func(o, n any) { ch <- change{o, n} }})
	w.Register(WatchEntry{Key: "dur", Env: "TEST_WATCHER_DUR", ValuePtr: &dur, OnChange: func(o, n any) { ch <- change{o, n} }})
	startWatcher(t, w)

	os.Setenv("TEST_WATCHER_INT", "9090")
	if c := waitChange(t, ch); c.old != 8080 || c.new != 9090 {
		t.Errorf("OnChange(%#v, %#v), want (8080, 9090)", c.old, c.new)
	}
	if got := port.Load(); got != 9090 {
		t.Errorf("port = %#v, want int(9090)", got)
	}
	os.Setenv("TEST_WATCHER_DUR", "2s")
	if c := waitChange(t, ch); c.new != 2*time.Second {
		t.Errorf("OnChange new = %#v, want 2s", c.new)
	}
}

func TestWatcher_UnparseableEnv(t *testing.T) {
	t.Setenv("TEST_WATCHER_BAD", "8080")
	var port atomic.Value
	port.Store(8080)
	ch := make(chan change, 4)
	w := NewWatcher(10 * time.Millisecond)
	w.Register(WatchEntry{Env: "TEST_WATCHER_BAD", ValuePtr: &port, OnChange: func(o, n any) { ch <- change{o, n} }})
	startWatcher(t, w)

	os.Setenv("TEST_WATCHER_BAD", "abc")
	noChange(t, ch, 60*time.Millisecond)
	if got := port.Load(); got != 8080 {
		t.Errorf("port = %v, want unchanged 8080", got)
	}
	// Retried on later ticks: a valid value is picked up.
	os.Setenv("TEST_WATCHER_BAD", "9090")
	if c := waitChange(t, ch); c.new != 9090 {
		t.Errorf("OnChange new = %v, want 9090", c.new)
	}
}

func TestWatcher_EnvWithGetter(t *testing.T) {
	t.Setenv("TEST_WATCHER_G", "x")
	var val atomic.Value
	val.Store("from-getter-1")
	var n atomic.Int32
	ch := make(chan change, 4)
	w := NewWatcher(10 * time.Millisecond)
	w.Register(WatchEntry{
		Env:      "TEST_WATCHER_G",
		Getter:   func() (any, error) { return "from-getter-" + strconv.Itoa(int(n.Add(1))+1), nil },
		ValuePtr: &val,
		OnChange: func(o, n any) { ch <- change{o, n} },
	})
	startWatcher(t, w)
	os.Setenv("TEST_WATCHER_G", "y")
	if c := waitChange(t, ch); c.new != "from-getter-2" {
		t.Errorf("OnChange new = %v, want Getter value", c.new)
	}
	noChange(t, ch, 50*time.Millisecond) // Getter not polled while env is unchanged
}

func TestWatcher_GetterOnly(t *testing.T) {
	var (
		mu   sync.Mutex
		cur  any = "v1"
		gerr error
	)
	set := func(v any, err error) { mu.Lock(); cur, gerr = v, err; mu.Unlock() }
	var val atomic.Value
	val.Store("v1")
	ch := make(chan change, 4)
	w := NewWatcher(10 * time.Millisecond)
	w.Register(WatchEntry{
		Key:      "g",
		Getter:   func() (any, error) { mu.Lock(); defer mu.Unlock(); return cur, gerr },
		ValuePtr: &val,
		OnChange: func(o, n any) { ch <- change{o, n} },
	})
	startWatcher(t, w)
	noChange(t, ch, 40*time.Millisecond)

	set("v2", nil)
	if c := waitChange(t, ch); c.old != "v1" || c.new != "v2" {
		t.Errorf("OnChange(%v, %v), want (v1, v2)", c.old, c.new)
	}
	set("v3", errors.New("boom"))
	noChange(t, ch, 40*time.Millisecond)
	if got := val.Load(); got != "v2" {
		t.Errorf("value after Getter error = %v, want v2", got)
	}
}

func TestWatcher_FileChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		mu       sync.Mutex
		loaded   = "one"
		failLoad bool
	)
	reloader := func() error {
		mu.Lock()
		defer mu.Unlock()
		if failLoad {
			return errors.New("reload failed")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		loaded = string(b)
		return nil
	}
	var val atomic.Value
	val.Store("one")
	ch := make(chan change, 8)
	w := NewWatcher(time.Hour) // no polling: only fsnotify can deliver changes
	w.SetReloader(reloader)
	w.Register(WatchEntry{
		Key:        "k",
		ConfigFile: file,
		Getter:     func() (any, error) { mu.Lock(); defer mu.Unlock(); return loaded, nil },
		ValuePtr:   &val,
		OnChange:   func(o, n any) { ch <- change{o, n} },
	})
	startWatcher(t, w)

	if err := os.WriteFile(file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := waitChange(t, ch); c.old != "one" || c.new != "two" {
		t.Errorf("OnChange(%v, %v), want (one, two)", c.old, c.new)
	}

	mu.Lock()
	failLoad = true
	mu.Unlock()
	if err := os.WriteFile(file, []byte("three"), 0o600); err != nil {
		t.Fatal(err)
	}
	noChange(t, ch, 200*time.Millisecond)
	if got := val.Load(); got != "two" {
		t.Errorf("value after reload error = %v, want two", got)
	}
}

func TestWatcher_DoubleStartPanics(t *testing.T) {
	w := NewWatcher(time.Hour)
	startWatcher(t, w)
	defer func() {
		if recover() == nil {
			t.Fatal("second Start did not panic")
		}
	}()
	w.Start(context.Background())
}

func TestWatcher_RestartAfterStop(t *testing.T) {
	w := NewWatcher(time.Hour)
	w.Start(context.Background())
	w.Stop()
	w.Start(context.Background())
	w.Stop()
	w.Stop() // no-op
}

func TestWatcher_RegisterRequiresAtomicValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register with *string did not panic")
		}
	}()
	s := "x"
	NewWatcher(0).Register(WatchEntry{Key: "k", ValuePtr: &s})
}

func TestWatcher_DefaultInterval(t *testing.T) {
	if w := NewWatcher(0); w.interval != DefaultInterval {
		t.Errorf("interval = %v, want %v", w.interval, DefaultInterval)
	}
}

// newNoChangeWatcher registers n unchanged env entries and prepares the
// watcher's state without starting its goroutine.
func newNoChangeWatcher(tb testing.TB, n int) *Watcher {
	w := NewWatcher(time.Hour)
	for i := 0; i < n; i++ {
		name := "CFGX_BENCH_" + strconv.Itoa(i)
		if err := os.Setenv(name, "value-"+strconv.Itoa(i)); err != nil {
			tb.Fatal(err)
		}
		v := &atomic.Value{}
		v.Store("value-" + strconv.Itoa(i))
		w.Register(WatchEntry{Key: name, Env: name, ValuePtr: v, OnChange: func(o, n any) {}})
	}
	w.mu.Lock()
	w.prepare()
	w.mu.Unlock()
	return w
}

func TestWatcher_NoChangeZeroAllocs(t *testing.T) {
	w := newNoChangeWatcher(t, 10)
	if allocs := testing.AllocsPerRun(100, w.tick); allocs != 0 {
		t.Errorf("tick allocs = %v, want 0", allocs)
	}
}

func BenchmarkWatcher_NoChange(b *testing.B) {
	w := newNoChangeWatcher(b, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.tick()
	}
}
