package cmd

//go:generate go run github.com/example/cfgx/cmd/cfgx gen

import (
	"fmt"
	"log"
	"sync/atomic"

	"github.com/example/cfgx"
)

// Schema declares every configuration value of webapp. It is the single
// source of truth: after editing it, run
//
//	go generate ./cmd/...
//
// to regenerate cmd/config.gen.go (typed config structs, RegisterFlags,
// BindViper, Load, LoadServe, LoadMigrate) and the .env.example,
// config.example.yaml, config.schema.json and docs/ files.
var Schema = cfgx.Define(
	// ── Global settings (every command) ───────────────────────────────
	cfgx.String("app-name").
		Env("APP_NAME").Default("webapp").
		Desc("Human-readable application name").
		Tags("general"),

	cfgx.Bool("debug").
		Flag("--debug").Env("APP_DEBUG").Default(false).
		IsLive().OnChange(func(old, new bool) {
		if new {
			log.Println("debug mode enabled")
		}
		if hook := debugHook.Load(); hook != nil {
			(*hook)(old, new)
		}
	}).
		Desc("Enable debug logging; hot-reloadable via APP_DEBUG").
		Tags("general"),

	cfgx.String("output-format").
		Flag("--output-format").Env("APP_OUTPUT_FORMAT").Default("text").
		Validate(cfgx.Enum("text", "json")).
		Desc("Output format").
		Tags("general"),

	cfgx.Bool("json-output").
		Flag("--json-output").Env("APP_JSON_OUTPUT").
		Desc("Print machine-readable JSON output").
		Tags("general"),

	cfgx.Bool("text-output").
		Flag("--text-output").Env("APP_TEXT_OUTPUT").
		Desc("Print human-readable text output").
		Tags("general"),

	cfgx.String("log-file").
		Env("APP_LOG_FILE").
		Deprecated("logs go to stderr; redirect them instead").
		Desc("File to write logs to").
		Tags("general"),

	// ── HTTP server settings (serve command only) ──────────────────────
	cfgx.Int("port").
		Flag("--port", "-p").Env("APP_PORT").Default(8080).
		Validate(cfgx.PortRange(1024, 65535)).
		Desc("HTTP port the server listens on").
		Scope("serve").Tags("server"),

	cfgx.Bool("tls-enabled").
		Flag("--tls-enabled").Env("APP_TLS_ENABLED").Default(false).
		Desc("Enable TLS").
		Scope("serve").Tags("server", "tls"),

	cfgx.String("tls-cert-path").
		Flag("--tls-cert").Env("APP_TLS_CERT").
		Required().DependsOn("tls-enabled", true).
		Desc("Path to the TLS certificate (required when tls-enabled=true)").
		Scope("serve").Tags("server", "tls"),

	// ── Database (serve + migrate) ─────────────────────────────────────
	cfgx.Object("db",
		cfgx.String("host").Env("DB_HOST").Default("localhost").
			Desc("Database host"),
		cfgx.Int("port").Env("DB_PORT").Default(5432).
			Validate(cfgx.Port()).
			Desc("Database port"),
		cfgx.String("name").Env("DB_NAME").Required().
			Desc("Database name"),
		cfgx.String("user").Env("DB_USER").Default("postgres").
			Desc("Database user"),
		cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required().
			Desc("Database password"),
		cfgx.Computed("dsn", func(parent any) string {
			c, ok := parent.(*DbConfig)
			if !ok {
				return ""
			}
			return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
				c.User(), c.Password(), c.Host(), c.Port(), c.Name())
		}).Desc("Connection string derived from the other db settings"),
	).Desc("Database connection settings").
		Scope("serve", "migrate").Tags("database"),

	// ── Email (optional, only validated if email-enabled=true) ────────
	cfgx.Bool("email-enabled").
		Flag("--email-enabled").Env("EMAIL_ENABLED").Default(false).
		Desc("Send notification emails").
		Tags("email"),

	cfgx.Object("smtp",
		cfgx.String("host").Env("SMTP_HOST").Default("localhost").
			Desc("SMTP server host"),
		cfgx.Int("port").Env("SMTP_PORT").Default(587).
			Validate(cfgx.Port()).
			Desc("SMTP server port"),
		cfgx.String("user").Env("SMTP_USER").Required().
			Desc("SMTP user"),
		cfgx.String("password").Env("SMTP_PASSWORD").Sensitive().Required().
			Desc("SMTP password"),
	).Desc("Outgoing mail settings").
		DependsOn("email-enabled", true).Tags("email"),

	// ── Schema-level constraints ───────────────────────────────────────
	cfgx.MutuallyExclusive("json-output", "text-output"),
)

// debugHook, when set, is called by the debug entry's OnChange callback after
// the watcher picks up a new APP_DEBUG value. Tests use it to observe hot
// reloads.
var debugHook atomic.Pointer[func(old, new bool)]
