package testschema

import "github.com/example/cfgx"

// Schema has Sensitive entries, each settable through Env or a Getter.
var Schema = cfgx.Define(
	cfgx.String("api_key").Env("API_KEY").Required().Sensitive(),
	cfgx.String("session_secret").Sensitive().Getter(func() (string, error) { return "s", nil }),
	cfgx.String("user").Env("APP_USER"),
)
