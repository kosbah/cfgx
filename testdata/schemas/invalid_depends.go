package testschema

import "github.com/epistemer/cfgx"

// Schema has a DependsOn referring to an entry that does not exist.
var Schema = cfgx.Define(
	cfgx.Bool("tls").Env("TLS"),
	cfgx.String("cert").Env("CERT").DependsOn("missing", true),
)
