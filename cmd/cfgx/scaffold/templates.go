package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.New("").Option("missingkey=error").ParseFS(templateFS, "templates/*.tmpl"))

// ScaffoldData is the data passed to every template.
type ScaffoldData struct {
	ModulePath string // e.g. "github.com/yourorg/myapp"
	AppName    string // e.g. "myapp" (last path segment)
	GoVersion  string // e.g. "1.21"
	Year       int    // current year

	// CfgxVersion is the version of github.com/epistemer/cfgx required in go.mod.
	CfgxVersion string
	// CfgxReplace, if set, is a go.mod replace target for github.com/epistemer/cfgx
	// (a local directory), already quoted if needed.
	CfgxReplace string
}

// File is one scaffolded file.
type File struct {
	Path     string // output path relative to the project root, slash-separated
	Template string // template file name under templates/
}

// Files lists the scaffolded files in write order.
var Files = []File{
	{Path: "go.mod", Template: "go.mod.tmpl"},
	{Path: "main.go", Template: "main.go.tmpl"},
	{Path: "cmd/root.go", Template: "cmd_root.go.tmpl"},
	{Path: "cmd/serve.go", Template: "cmd_serve.go.tmpl"},
	{Path: "cmd/migrate.go", Template: "cmd_migrate.go.tmpl"},
	{Path: "cmd/schema.go", Template: "cmd_schema.go.tmpl"},
}

// Dirs lists the directories created empty.
var Dirs = []string{"pkg"}

// SchemaPath is the path of the scaffolded schema file.
const SchemaPath = "cmd/schema.go"

// Render executes f's template with data. Go files are gofmt-formatted.
func Render(f File, data ScaffoldData) ([]byte, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, f.Template, data); err != nil {
		return nil, fmt.Errorf("scaffold: rendering %s: %w", f.Path, err)
	}
	if !strings.HasSuffix(f.Path, ".go") {
		return buf.Bytes(), nil
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("scaffold: formatting %s: %w", f.Path, err)
	}
	return out, nil
}
