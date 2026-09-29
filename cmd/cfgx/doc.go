// Package main implements the cfgx CLI binary.
//
// cfgx is the command-line tool for the cfgx configuration library.
// It scaffolds new cobra/viper+cfgx projects, generates typed config code and
// example files from a schema, validates configurations against a schema, and
// generates standalone documentation.
//
// Install:
//
//	go install github.com/epistemer/cfgx/cmd/cfgx@latest
//
// Commands:
//
//	cfgx init <module-path>     scaffold a new cobra/viper+cfgx project
//	cfgx gen                    generate config.gen.go and artifacts from a schema
//	cfgx validate               validate a config file and/or the environment
//	cfgx docs                   generate documentation artifacts only
//
// Without --schema, gen, validate and docs look for a single schema.go
// containing cfgx.Define under the current directory. validate exits with 0 on
// success, 1 on validation errors and 2 on tool errors.
package main
