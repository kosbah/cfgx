package testschema

import "github.com/example/cfgx"

var Schema = cfgx.Define(
	cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080).Desc("HTTP port"),
)
