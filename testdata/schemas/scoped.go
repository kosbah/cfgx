package testschema

import "github.com/epistemer/cfgx"

// Schema has entries scoped to specific commands.
var Schema = cfgx.Define(
	cfgx.Int("port").Env("PORT").Scope("serve"),
	cfgx.Object("db",
		cfgx.String("url").Env("DB_URL"),
	).Scope("serve", "migrate"),
	cfgx.Bool("verbose").Flag("--verbose", "-v"),
)
