package testschema

import (
	"time"

	"github.com/example/cfgx"
)

// Schema nests objects three levels deep: server -> tls -> files.
var Schema = cfgx.Define(
	cfgx.String("name").Env("APP_NAME").Default("cfgx"),
	cfgx.Object("server",
		cfgx.String("host").Env("SERVER_HOST").Default("0.0.0.0"),
		cfgx.Duration("timeout").Env("SERVER_TIMEOUT").Default(30*time.Second),
		cfgx.Float64("load_factor").Default(1),
		cfgx.List("origins", cfgx.String("origin")).Env("SERVER_ORIGINS").Default([]string{"a", "b"}),
		cfgx.Object("tls",
			cfgx.Bool("enabled").Env("SERVER_TLS_ENABLED").Default(false),
			cfgx.Int("min_version").Env("SERVER_TLS_MIN_VERSION").Default(12).DependsOn("enabled", true),
			cfgx.Object("files",
				cfgx.String("cert").Env("SERVER_TLS_CERT").DependsOn("server.tls.enabled", true),
				cfgx.String("key").Env("SERVER_TLS_KEY").Sensitive(),
			).Desc("Certificate files"),
		).Desc("TLS settings"),
	).Desc("HTTP server").Tags("server"),
)
