package testschema

import "github.com/epistemer/cfgx"

// Schema declares "port" twice at the root: the parser must reject it.
var Schema = cfgx.Define(
	cfgx.Int("port").Env("APP_PORT").Default(8080),
	cfgx.Int("port").Env("APP_PORT_2").Default(9090),
)
