package testschema

import (
	"errors"
	"strings"
	"time"

	"github.com/epistemer/cfgx"
)

// validateName is a custom validator referenced by identifier.
func validateName(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("name must not be blank")
	}
	return nil
}

// Schema exercises every entry type, every builder method, all ten built-in
// validators, custom validators, DependsOn and MutuallyExclusive.
var Schema = cfgx.Define(
	cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080).
		Desc("HTTP port the server listens on").
		Validate(cfgx.PortRange(1024, 65535)).
		Scope("serve").Tags("server"),
	cfgx.Int("metrics_port").Env("METRICS_PORT").Default(9090).Validate(cfgx.Port()),
	cfgx.String("admin_email").Env("ADMIN_EMAIL").Validate(cfgx.Email()),
	cfgx.String("public_url").Env("PUBLIC_URL").Validate(cfgx.URL()),
	cfgx.String("signing_key").Env("SIGNING_KEY").Required().Sensitive().Validate(cfgx.Base64()),
	cfgx.String("name").Flag("--name").Env("APP_NAME").Default("cfgx").
		Validate(cfgx.Regex(`^[a-z][a-z0-9-]*$`)).
		Validate(validateName),
	cfgx.String("grace").Env("GRACE_PERIOD").Default("10s").Validate(cfgx.DurationValidator()),
	cfgx.String("version").Env("APP_VERSION").
		Validate(cfgx.Semver()).
		Validate(func(v string) error { return nil }),
	cfgx.String("log_format").Flag("--log-format").Env("LOG_FORMAT").Default("text").
		Validate(cfgx.Enum("text", "json")).
		IsLive().
		OnChange(func(old, updated string) {}),
	cfgx.Float64("sample_rate").Env("SAMPLE_RATE").Default(0.5).Validate(cfgx.Range(0, 1)),
	cfgx.Bool("json_output").Flag("--json"),
	cfgx.Bool("text_output").Flag("--text"),
	cfgx.Duration("timeout").Env("TIMEOUT").Default(30*time.Second).IsLive(),
	cfgx.String("token").Sensitive().Getter(func() (string, error) { return "t", nil }),
	cfgx.String("legacy_host").Env("LEGACY_HOST").Deprecated("use db.host instead"),
	cfgx.List("hosts", cfgx.String("host").Validate(cfgx.URL())).
		Flag("--hosts").Env("HOSTS").
		Default([]string{"http://a", "http://b"}).
		Validate(func(v []any) error { return nil }),
	cfgx.Object("db",
		cfgx.String("host").Env("DB_HOST").Default("localhost"),
		cfgx.Int("port").Env("DB_PORT").Default(5432).Validate(cfgx.Port()),
		cfgx.String("password").Env("DB_PASSWORD").Required().Sensitive(),
		cfgx.Bool("tls").Env("DB_TLS").Default(false),
		cfgx.String("ca_file").Env("DB_CA_FILE").DependsOn("tls", true),
		cfgx.Object("pool",
			cfgx.Int("max_open").Env("DB_POOL_MAX_OPEN").Default(10),
			cfgx.Duration("idle_timeout").Env("DB_POOL_IDLE_TIMEOUT").Default(5*time.Minute),
		),
		cfgx.Computed[string]("dsn", func(parent any) string { return "" }).
			Desc("Connection string").
			IsLive().
			OnChange(func(old, updated string) {}),
	).Desc("Database settings").Scope("serve", "migrate").Tags("database"),
	cfgx.String("report_dir").Env("REPORT_DIR").DependsOn("db.tls", true),
	cfgx.MutuallyExclusive("json_output", "text_output"),
)
