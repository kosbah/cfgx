package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/epistemer/cfgx/internal/convert"
)

// DefaultInterval is the env polling interval used when NewWatcher is given a
// non-positive interval; the watchers started by generated Load functions use
// it. It is a variable so that programs and tests can shorten it. Set it
// before calling NewWatcher or Load, never concurrently with them.
var DefaultInterval = 5 * time.Second

// WatchEntry describes a single IsLive config to be watched.
// Generated code builds these from schema.WatcherEntries().
type WatchEntry struct {
	// Key is the viper key for this entry (e.g. "debug", "db.host").
	Key string

	// Env is the environment variable name (e.g. "APP_DEBUG").
	// If non-empty, the watcher polls it every tick. When its value changes the
	// new value is taken from Getter if set, otherwise parsed from the env
	// string. If the variable becomes unset and there is no Getter, the current
	// value is kept.
	Env string

	// ConfigFile is set if the entry is backed by a config file.
	// If non-empty, fsnotify watches the file's directory; when the file is
	// written or created the watcher runs the reloader (see SetReloader) and
	// then re-reads the entry through Getter. File-backed entries therefore
	// need a Getter to pick up changes.
	ConfigFile string

	// Getter is called to obtain the current value after a change is detected.
	// It may be nil for env-backed entries (the watcher parses the env string).
	// An entry with a Getter but no Env or ConfigFile is polled every tick.
	// If Getter returns an error, the old value is kept.
	Getter func() (any, error)

	// ValuePtr holds the entry's current value and must be a non-nil
	// *atomic.Value, normally pre-loaded with the initial value. New values are
	// converted to the dynamic type already stored there (string, int, bool,
	// float64, time.Duration or []any) before being stored.
	ValuePtr any

	// OnChange is called, from the watcher goroutine, after ValuePtr is
	// updated. old and new are the typed values before and after the change.
	OnChange func(old, new any)
}

// Watcher monitors IsLive config entries and updates them when their source
// changes. Register entries, then call Start; call Stop to shut it down.
type Watcher struct {
	interval time.Duration
	entries  []WatchEntry
	reloader func() error
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	// Built by prepare and owned by the watcher goroutine while running.
	states   []entryState
	envIdx   []int            // entries with Env
	pollIdx  []int            // Getter-only entries, polled every tick
	fileIdx  map[string][]int // cleaned absolute ConfigFile -> entries
	fsw      *fsnotify.Watcher
	reloadFn func() error
}

type entryState struct {
	entry   *WatchEntry
	ptr     *atomic.Value
	lastRaw string // last env value seen
	lastSet bool   // whether the env var was set
}

// NewWatcher creates a Watcher that polls env-var-backed and Getter-only
// entries every interval. A non-positive interval defaults to DefaultInterval.
func NewWatcher(interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Watcher{interval: interval}
}

// Register adds a WatchEntry to the watcher. It must be called before Start
// (or after Stop). It panics if the watcher is running or if entry.ValuePtr is
// not a non-nil *atomic.Value.
func (w *Watcher) Register(entry WatchEntry) {
	if p, ok := entry.ValuePtr.(*atomic.Value); !ok || p == nil {
		panic(fmt.Sprintf("cfgx/runtime: WatchEntry %q: ValuePtr must be a non-nil *atomic.Value, got %T", entry.Key, entry.ValuePtr))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		panic("cfgx/runtime: Watcher.Register called after Start")
	}
	w.entries = append(w.entries, entry)
}

// SetReloader sets the function run when a watched config file changes, before
// file-backed entries are re-read through their Getters. Generated code passes
// the viper instance's ReadInConfig. If it returns an error, no values change.
// It must be called before Start.
func (w *Watcher) SetReloader(fn func() error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reloader = fn
}

// Start launches the watcher goroutine. The goroutine exits when ctx is
// cancelled or Stop is called. Env values are snapshotted synchronously, so a
// change made after Start returns is detected.
//
// If the file watcher cannot be set up, file-backed entries with a Getter fall
// back to being polled every tick.
//
// Start panics if called more than once without a preceding Stop.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		panic("cfgx/runtime: Watcher.Start called twice without Stop")
	}
	w.running = true
	w.prepare()
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.run(ctx)
}

// Stop cancels the watcher and waits for its goroutine to exit. It is a no-op
// if the watcher is not running.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	w.cancel()
	w.wg.Wait()
	w.running = false
	w.cancel = nil
}

// prepare builds the per-entry state. Called with w.mu held.
func (w *Watcher) prepare() {
	w.states = make([]entryState, len(w.entries))
	w.envIdx, w.pollIdx, w.fileIdx = nil, nil, nil
	w.reloadFn = w.reloader
	w.fsw = nil
	for i := range w.entries {
		e := &w.entries[i]
		st := &w.states[i]
		st.entry = e
		st.ptr = e.ValuePtr.(*atomic.Value) //nolint:errcheck // type checked by Register
		if e.Env != "" {
			st.lastRaw, st.lastSet = os.LookupEnv(e.Env)
			w.envIdx = append(w.envIdx, i)
		}
		if e.ConfigFile != "" {
			if w.fileIdx == nil {
				w.fileIdx = map[string][]int{}
			}
			p := e.ConfigFile
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			p = filepath.Clean(p)
			w.fileIdx[p] = append(w.fileIdx[p], i)
		}
		if e.Env == "" && e.ConfigFile == "" && e.Getter != nil {
			w.pollIdx = append(w.pollIdx, i)
		}
	}
	if len(w.fileIdx) > 0 {
		w.fsw = w.watchFiles()
	}
}

// watchFiles creates an fsnotify watcher on the directory of each config file.
// On failure it returns nil and moves file-backed entries with a Getter to
// polling.
func (w *Watcher) watchFiles() *fsnotify.Watcher {
	fsw, err := fsnotify.NewWatcher()
	if err == nil {
		dirs := map[string]bool{}
		for p := range w.fileIdx {
			d := filepath.Dir(p)
			if dirs[d] {
				continue
			}
			dirs[d] = true
			if err = fsw.Add(d); err != nil {
				break
			}
		}
		if err == nil {
			return fsw
		}
		fsw.Close()
	}
	for _, idx := range w.fileIdx {
		for _, i := range idx {
			e := w.states[i].entry
			if e.Getter != nil && e.Env == "" {
				w.pollIdx = append(w.pollIdx, i)
			}
		}
	}
	w.fileIdx = nil
	return nil
}

func (w *Watcher) run(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	var (
		events <-chan fsnotify.Event
		errs   <-chan error
	)
	if w.fsw != nil {
		defer w.fsw.Close()
		events, errs = w.fsw.Events, w.fsw.Errors
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick()
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			w.handleFileEvent(ev)
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		}
	}
}

// tick polls every env-backed and Getter-only entry once. It does not
// allocate when no env value has changed.
func (w *Watcher) tick() {
	for _, i := range w.envIdx {
		st := &w.states[i]
		cur, set := os.LookupEnv(st.entry.Env)
		if cur == st.lastRaw && set == st.lastSet {
			continue
		}
		w.envChanged(st, cur, set)
	}
	for _, i := range w.pollIdx {
		w.refresh(&w.states[i])
	}
}

// envChanged handles a changed env value. On failure the recorded raw value is
// left alone so the change is retried on the next tick.
func (w *Watcher) envChanged(st *entryState, cur string, set bool) {
	e := st.entry
	var newVal any
	switch {
	case e.Getter != nil:
		v, err := e.Getter()
		if err != nil {
			return
		}
		newVal = v
	case !set:
		st.lastRaw, st.lastSet = cur, set
		return
	default:
		newVal = cur
	}
	if w.apply(st, newVal) {
		st.lastRaw, st.lastSet = cur, set
	}
}

// refresh re-reads st through its Getter and applies the result.
func (w *Watcher) refresh(st *entryState) {
	v, err := st.entry.Getter()
	if err != nil {
		return
	}
	w.apply(st, v)
}

// apply converts newVal to the stored type, stores it if it differs from the
// current value and fires OnChange. It reports false if newVal could not be
// converted.
func (w *Watcher) apply(st *entryState, newVal any) bool {
	if newVal == nil {
		return false
	}
	old := st.ptr.Load()
	if old != nil {
		v, err := convert.Like(old, newVal)
		if err != nil || reflect.TypeOf(v) != reflect.TypeOf(old) {
			return false
		}
		newVal = v
		if reflect.DeepEqual(old, newVal) {
			return true
		}
	}
	st.ptr.Store(newVal)
	if st.entry.OnChange != nil {
		st.entry.OnChange(old, newVal)
	}
	return true
}

// handleFileEvent reloads the config and re-reads file-backed entries when a
// watched file is written or created.
func (w *Watcher) handleFileEvent(ev fsnotify.Event) {
	if !ev.Has(fsnotify.Write) && !ev.Has(fsnotify.Create) {
		return
	}
	name := ev.Name
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	idx, ok := w.fileIdx[filepath.Clean(name)]
	if !ok {
		return
	}
	if w.reloadFn != nil {
		if err := w.reloadFn(); err != nil {
			return
		}
	}
	for _, i := range idx {
		if w.states[i].entry.Getter != nil {
			w.refresh(&w.states[i])
		}
	}
}
