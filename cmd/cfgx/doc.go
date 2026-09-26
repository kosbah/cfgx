// Package main implements the cfgx CLI binary.
//
// cfgx is the command-line tool for the cfgx configuration library.
// It scaffolds new cobra/viper+cfgx projects, generates typed config code and
// example files from a schema, validates configurations against a schema, and
// generates standalone documentation.
//
// Install:
//
//	go install github.com/example/cfgx/cmd/cfgx@latest
//
// Commands:
//
//	cfgx init [module-path]     scaffold a new cobra/viper+cfgx project
//	cfgx gen                    generate typed Go code and artifacts from schema
//	cfgx validate               validate environment/config file against schema
//	cfgx docs                   generate documentation artifacts only
//
// See docs/agents/phase6-cli.prompt.md for the complete specification.
package main
