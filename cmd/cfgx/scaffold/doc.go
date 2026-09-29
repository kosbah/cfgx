// Package scaffold holds the project templates written by `cfgx init`.
//
// The templates are embedded from templates/*.tmpl and rendered with
// [ScaffoldData]. [Files] lists what `cfgx init` writes: go.mod, main.go,
// cmd/root.go, cmd/serve.go, cmd/migrate.go and the example schema at
// [SchemaPath]; [Dirs] lists the directories created empty (pkg/). [Render]
// executes one template and gofmt-formats Go output.
//
// The scaffolded schema carries a `//go:generate cfgx gen` directive, so the
// cfgx CLI must be on PATH for `go generate ./cmd/...` in a new project.
package scaffold
