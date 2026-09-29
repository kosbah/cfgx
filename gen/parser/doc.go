// Package parser implements the go/ast-based schema parser for cfgx.
//
// It reads a Go source file containing a cfgx.Define(...) call and extracts
// the schema into a [ir.SchemaIR] intermediate representation using static
// analysis only — no compilation, no go run.
//
// Usage:
//
//	schema, err := parser.Parse("cmd/schema.go")
//	if err != nil {
//	    var parseErr *parser.ParseError
//	    if errors.As(err, &parseErr) {
//	        fmt.Fprintf(os.Stderr, "%s\n", parseErr) // file.go:10:5: message
//	    }
//	}
//
// [ParseWithWarnings] also returns non-fatal [Warning] values (currently: an
// entry that is both Required and has a Default). Parsing stops at the
// first error, so a schema with several problems is reported one at a time.
//
// See docs/agents/phase2-parser.prompt.md for the complete specification.
package parser
