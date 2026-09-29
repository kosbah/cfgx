package testschema

import (
	"log"
	"time"

	"github.com/example/cfgx"
)

// Schema has IsLive entries: env-backed scalars with and without OnChange, a
// live list, a live sensitive secret loaded through a Getter, a scoped live
// entry and a live Computed entry with OnChange.
var Schema = cfgx.Define(
	cfgx.Bool("debug").Flag("--debug").Env("APP_DEBUG").Default(false).
		IsLive().
		OnChange(func(old, updated bool) { log.Printf("debug: %v -> %v", old, updated) }).
		Desc("Enable debug logging; hot-reloadable via APP_DEBUG"),
	cfgx.String("log-level").Env("APP_LOG_LEVEL").Default("info").IsLive(),
	cfgx.List("feature-flags", cfgx.String("flag")).Env("APP_FEATURE_FLAGS").IsLive(),
	cfgx.String("api-token").Sensitive().IsLive().
		Getter(func() (string, error) { return "token-from-vault", nil }),
	cfgx.Duration("poll-interval").Env("APP_POLL_INTERVAL").Default(15*time.Second).
		IsLive().Scope("serve"),
	cfgx.Object("limits",
		cfgx.Int("max-conns").Env("APP_MAX_CONNS").Default(100).IsLive(),
		cfgx.Computed("summary", func(parent any) string { return "limits" }).
			IsLive().
			OnChange(func(old, updated string) {}),
	),
	cfgx.String("legacy-mode").Flag("--legacy-mode").Env("APP_LEGACY_MODE").
		Deprecated("use --log-level instead"),
)
