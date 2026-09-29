package testschema

import "github.com/epistemer/cfgx"

// Schema has Deprecated entries.
var Schema = cfgx.Define(
	cfgx.String("host").Env("HOST").Deprecated("use server.host instead"),
	cfgx.Object("server",
		cfgx.String("host").Env("SERVER_HOST"),
	).Deprecated("server block moves to http in v2"),
)
