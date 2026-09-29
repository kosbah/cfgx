//go:build e2e

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/example/cfgx"
	"github.com/example/cfgx/runtime"
)

// watchInterval is the IsLive polling interval used by these tests.
const watchInterval = 50 * time.Millisecond

func TestMain(m *testing.M) {
	// Set before any Load starts a watcher (DefaultInterval must not change
	// while watchers are being created).
	runtime.DefaultInterval = watchInterval
	os.Exit(m.Run())
}

// dbViper returns a viper instance holding the required db settings.
func dbViper() *viper.Viper {
	v := viper.New()
	v.Set("db.host", "testhost")
	v.Set("db.name", "testdb")
	v.Set("db.password", "secret")
	v.Set("db.user", "admin")
	return v
}

// fieldsOf returns the Field of every FieldError in err, which must be a
// *cfgx.ValidationErrors.
func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	var ve *cfgx.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("error %v (%T) is not a *cfgx.ValidationErrors", err, err)
	}
	var out []string
	for _, fe := range ve.Errors {
		out = append(out, fe.Field)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestLoad_PopulatesAllFields(t *testing.T) {
	v := dbViper()
	v.Set("port", 9090)

	cfg, err := LoadServe(Schema, v)
	if err != nil {
		t.Fatalf("LoadServe: %v", err)
	}
	defer cfg.Close()

	if got := cfg.Port(); got != 9090 {
		t.Errorf("Port() = %d, want 9090", got)
	}
	db := cfg.Db()
	if got := db.Host(); got != "testhost" {
		t.Errorf("Db().Host() = %q, want testhost", got)
	}
	if got := db.Name(); got != "testdb" {
		t.Errorf("Db().Name() = %q, want testdb", got)
	}
	if got := db.User(); got != "admin" {
		t.Errorf("Db().User() = %q, want admin", got)
	}
	if got := db.Password(); got != "secret" {
		t.Errorf("Db().Password() = %q, want secret", got)
	}
	if dsn := db.DSN(); !strings.Contains(dsn, "testhost") || !strings.Contains(dsn, "testdb") {
		t.Errorf("Db().DSN() = %q, want it to contain testhost and testdb", dsn)
	}

	// The secret never shows up when the config is printed or marshalled.
	printed := fmt.Sprintf("%v %+v %#v %+v %#v", cfg, cfg, cfg, *db, db)
	if strings.Contains(printed, "secret") {
		t.Errorf("secret printed: %s", printed)
	}
	for _, x := range []any{cfg, db} {
		js, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("json.Marshal(%T): %v", x, err)
		}
		if strings.Contains(string(js), "secret") {
			t.Errorf("secret in JSON of %T: %s", x, js)
		}
	}
}

func TestLoad_Scoped(t *testing.T) {
	// tls-cert-path is required by serve once tls-enabled is true, and is
	// not used by migrate.
	v := dbViper()
	v.Set("tls-enabled", true)

	mcfg, err := LoadMigrate(Schema, v)
	if err != nil {
		t.Fatalf("LoadMigrate: %v, want success (tls-cert-path is serve-only)", err)
	}
	mcfg.Close()

	_, err = LoadServe(Schema, v)
	if err == nil {
		t.Fatal("LoadServe succeeded, want an error for tls-cert-path")
	}
	if fields := fieldsOf(t, err); !contains(fields, "tls-cert-path") {
		t.Errorf("LoadServe error fields = %q, want tls-cert-path", fields)
	}

	// Load (shared settings only) ignores the scoped db settings entirely.
	cfg, err := Load(Schema, viper.New())
	if err != nil {
		t.Fatalf("Load with no db settings: %v, want success", err)
	}
	cfg.Close()
	if _, err := LoadMigrate(Schema, viper.New()); err == nil {
		t.Error("LoadMigrate with no db settings succeeded, want db.name/db.password errors")
	}
}

func TestLoad_DependsOn(t *testing.T) {
	v := dbViper()
	v.Set("email-enabled", false)
	cfg, err := LoadServe(Schema, v)
	if err != nil {
		t.Fatalf("LoadServe with email disabled: %v, want success", err)
	}
	cfg.Close()

	v.Set("email-enabled", true)
	_, err = LoadServe(Schema, v)
	if err == nil {
		t.Fatal("LoadServe with email enabled and no smtp settings succeeded")
	}
	fields := fieldsOf(t, err)
	for _, want := range []string{"smtp.user", "smtp.password"} {
		if !contains(fields, want) {
			t.Errorf("error fields = %q, want %s", fields, want)
		}
	}
	for _, f := range fields {
		if !strings.HasPrefix(f, "smtp.") {
			t.Errorf("unexpected error field %q", f)
		}
	}
}

func TestIsLive_DetectsChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping watcher test in -short mode")
	}
	t.Setenv("APP_DEBUG", "false")

	changed := make(chan bool, 1)
	hook := func(_, new bool) {
		select {
		case changed <- new:
		default:
		}
	}
	debugHook.Store(&hook)
	defer debugHook.Store(nil)

	v := dbViper()
	BindViper(v, Schema)
	cfg, err := LoadServe(Schema, v)
	if err != nil {
		t.Fatalf("LoadServe: %v", err)
	}
	defer cfg.Close()
	if cfg.Debug() {
		t.Fatal("Debug() = true, want false")
	}

	t.Setenv("APP_DEBUG", "true")

	// Wait up to 3 watcher intervals.
	select {
	case val := <-changed:
		if !val {
			t.Errorf("OnChange new value = false, want true")
		}
	case <-time.After(3 * watchInterval):
		t.Fatal("IsLive watcher did not detect the APP_DEBUG change")
	}
	if !cfg.Debug() {
		t.Error("Debug() = false after the change, want true")
	}
}
