// Package codegen implements the Go source code generator for cfgx.
//
// It takes a [ir.SchemaIR] and produces a formatted Go source file (config.gen.go)
// containing typed config structs, getter methods, RegisterFlags, BindViper, and Load
// functions.
//
// The generator uses text/template for all code emission and always passes output
// through go/format.Source() before returning. It never uses fmt.Sprintf to construct
// Go source directly.
//
// See docs/agents/phase3-codegen.prompt.md for the complete specification.
// See gen/codegen/templates/config.go.tmpl for the Go source template.
package codegen
