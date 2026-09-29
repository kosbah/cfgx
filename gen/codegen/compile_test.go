package codegen

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// compileCases are built and run as programs in a temporary module. Each
// main.go exercises RegisterFlags, BindViper and Load* and exits non-zero
// (printing the reason) if the generated code misbehaves.
var compileCases = []struct {
	name   string
	schema string
	env    []string
	want   []string // substrings the program output must contain
	main   string
}{
	{
		name:   "full_schema",
		schema: "full.go",
		env:    []string{"SIGNING_KEY=c2lnbmluZy1zZWNyZXQ=", "DB_PASSWORD=hunter2-secret"},
		want:   []string{"ok"},
		main: `package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/example/cfgx"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func check(cond bool, format string, args ...any) {
	if !cond {
		fmt.Printf("FAIL: "+format+"\n", args...)
		os.Exit(1)
	}
}

func main() {
	root := &cobra.Command{Use: "app"}
	RegisterFlags(root, Schema)
	v := viper.New()
	BindViper(v, Schema)
	check(root.PersistentFlags().Parse([]string{"--port", "9000", "--json"}) == nil, "parse flags")

	cfg, err := LoadServe(Schema, v)
	check(err == nil, "LoadServe: %v", err)
	defer cfg.Close()
	check(cfg.Port() == 9000, "Port() = %d", cfg.Port())
	check(cfg.JSONOutput(), "JSONOutput() = false")
	check(cfg.MetricsPort() == 9090, "MetricsPort() = %d", cfg.MetricsPort())
	check(cfg.Name() == "cfgx", "Name() = %q", cfg.Name())
	check(cfg.SampleRate() == 0.5, "SampleRate() = %v", cfg.SampleRate())
	check(cfg.Timeout() == 30*time.Second, "Timeout() = %v", cfg.Timeout())
	check(cfg.LogFormat() == "text", "LogFormat() = %q", cfg.LogFormat())
	check(cfg.Token() == "t", "Token() = %q (from Getter)", cfg.Token())
	check(cfg.SigningKey() == "c2lnbmluZy1zZWNyZXQ=", "SigningKey() = %q", cfg.SigningKey())
	check(reflect.DeepEqual(cfg.Hosts(), []string{"http://a", "http://b"}), "Hosts() = %v", cfg.Hosts())
	db := cfg.Db()
	check(db.Host() == "localhost" && db.Port() == 5432, "Db() = %s:%d", db.Host(), db.Port())
	check(db.Password() == "hunter2-secret", "Db().Password() = %q", db.Password())
	check(db.Pool().MaxOpen() == 10, "Pool().MaxOpen() = %d", db.Pool().MaxOpen())
	check(db.Pool().IdleTimeout() == 5*time.Minute, "Pool().IdleTimeout() = %v", db.Pool().IdleTimeout())
	check(db.DSN() == "", "DSN() = %q", db.DSN())

	// Secrets never show up when configs are printed or marshalled.
	printed := fmt.Sprintf("%v %+v %#v %v %+v", cfg, cfg, cfg, *db, db)
	for _, secret := range []string{"hunter2-secret", "c2lnbmluZy1zZWNyZXQ="} {
		check(!strings.Contains(printed, secret), "secret %q printed: %s", secret, printed)
	}
	s := newSensitiveString("hunter2-secret")
	js, _ := json.Marshal(s)
	check(fmt.Sprintf("%v|%s|%#v", s, js, s) == "[redacted]|\"[redacted]\"|sensitiveString(\"[redacted]\")", "sensitiveString formatting: %v %s %#v", s, js, s)

	mcfg, err := LoadMigrate(Schema, v)
	check(err == nil, "LoadMigrate: %v", err)
	mcfg.Close()
	check(mcfg.Db().Host() == "localhost", "migrate Db().Host() = %q", mcfg.Db().Host())

	// A failing Getter makes Load return its error.
	bad := cfgx.Define(cfgx.String("token").Getter(func() (string, error) { return "", errors.New("vault down") }))
	_, err = Load(bad, v)
	check(err != nil && strings.Contains(err.Error(), "vault down"), "Load with failing getter: %v", err)

	// Validation errors are returned as *cfgx.ValidationErrors.
	v.Set("port", 80)
	_, err = LoadServe(Schema, v)
	var verrs *cfgx.ValidationErrors
	check(errors.As(err, &verrs), "LoadServe with bad port: %v", err)
	fmt.Println("ok")
}
`,
	},
	{
		name:   "is_live",
		schema: "live.go",
		env:    []string{"APP_DEBUG=false", "APP_FEATURE_FLAGS=beta"},
		want:   []string{"debug: false -> true", "ok"},
		main: `package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func check(cond bool, format string, args ...any) {
	if !cond {
		fmt.Printf("FAIL: "+format+"\n", args...)
		os.Exit(1)
	}
}

func main() {
	root := &cobra.Command{Use: "app"}
	RegisterFlags(root, Schema)
	v := viper.New()
	BindViper(v, Schema)
	check(root.PersistentFlags().Parse(nil) == nil, "parse flags")

	cfg, err := LoadServe(Schema, v)
	check(err == nil, "LoadServe: %v", err)
	check(!cfg.Debug(), "Debug() = true, want false")
	check(cfg.LogLevel() == "info", "LogLevel() = %q", cfg.LogLevel())
	check(len(cfg.FeatureFlags()) == 1 && cfg.FeatureFlags()[0] == "beta", "FeatureFlags() = %v", cfg.FeatureFlags())
	check(cfg.APIToken() == "token-from-vault", "APIToken() = %q", cfg.APIToken())
	check(cfg.PollInterval() == 15*time.Second, "PollInterval() = %v", cfg.PollInterval())
	check(cfg.Limits().MaxConns() == 100, "MaxConns() = %d", cfg.Limits().MaxConns())
	check(cfg.Limits().Summary() == "limits", "Summary() = %q", cfg.Limits().Summary())
	printed := fmt.Sprintf("%v %+v", cfg, cfg)
	check(!strings.Contains(printed, "token-from-vault"), "live secret printed: %s", printed)

	// Changing the env var is picked up by the watcher and fires OnChange.
	os.Setenv("APP_DEBUG", "true")
	deadline := time.Now().Add(15 * time.Second)
	for !cfg.Debug() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	check(cfg.Debug(), "Debug() not updated after APP_DEBUG=true")
	cfg.Close()
	cfg.Close()

	rcfg, err := Load(Schema, v)
	check(err == nil, "Load: %v", err)
	rcfg.Close()
	check(rcfg.Debug(), "root Debug() = false")
	fmt.Println("ok")
}
`,
	},
}

// TestGeneratedCodeCompiles builds the generated code for the full and
// is_live schemas together with a main package in a temporary module that
// replaces github.com/example/cfgx with this repository, vets it, and runs it.
func TestGeneratedCodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile test in -short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("skipping: go command not found in PATH")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range compileCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			src, err := os.ReadFile(filepath.Join(schemasDir, tc.schema))
			if err != nil {
				t.Fatal(err)
			}
			schemaSrc := strings.Replace(string(src), "package testschema", "package main", 1)
			code, err := Generate(parseFixture(t, tc.schema), Options{Package: "main", Source: tc.schema})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			gomod := "module example.com/cfgxcompile\n\ngo 1.21\n\nrequire github.com/example/cfgx v0.0.0\n\nreplace github.com/example/cfgx => " + repoRoot + "\n"
			files := map[string]string{
				"go.mod":        gomod,
				"schema.go":     schemaSrc,
				"config.gen.go": string(code),
				"main.go":       tc.main,
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			env := append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			run := func(timeout time.Duration, name string, args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				cmd := exec.CommandContext(ctx, name, args...)
				cmd.Dir = dir
				cmd.Env = env
				return cmd.CombinedOutput()
			}
			if out, err := run(5*time.Minute, goBin, "mod", "tidy"); err != nil {
				t.Skipf("skipping: go mod tidy failed (network or module cache unavailable?): %v\n%s", err, out)
			}
			if out, err := run(5*time.Minute, goBin, "build", "-o", "app", "."); err != nil {
				t.Fatalf("go build failed: %v\n%s\n--- config.gen.go ---\n%s", err, out, code)
			}
			if out, err := run(5*time.Minute, goBin, "vet", "."); err != nil {
				t.Fatalf("go vet failed: %v\n%s", err, out)
			}
			env = append(env, tc.env...)
			out, err := run(time.Minute, filepath.Join(dir, "app"))
			if err != nil {
				t.Fatalf("generated program failed: %v\n%s", err, out)
			}
			for _, w := range tc.want {
				if !bytes.Contains(out, []byte(w)) {
					t.Errorf("program output does not contain %q:\n%s", w, out)
				}
			}
		})
	}
}
