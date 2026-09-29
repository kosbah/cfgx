package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/example/cfgx"
	"github.com/example/cfgx/internal/convert"
	"github.com/example/cfgx/internal/ir"
	"github.com/example/cfgx/runtime"
)

// Exit codes of cfgx validate.
const (
	exitInvalid   = 1 // validation errors
	exitToolError = 2 // schema parse failure, missing file, bad flags, ...
)

type validateOptions struct {
	schema     string
	config     string
	env        bool
	showValues bool
}

func newValidateCmd() *cobra.Command {
	var o validateOptions
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a config file and/or the environment against a schema",
		Long: `validate checks configuration values against the schema: Required,
DependsOn, the built-in validators and MutuallyExclusive constraints. Custom
Validate functions and Getters cannot be run statically and are skipped.

Values come from the schema defaults, the config file (--config) and, with
--env, the environment. One line is printed per config key.

Exit codes: 0 = valid, 1 = validation errors, 2 = tool error.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return &exitError{Code: exitToolError, Err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValidate(cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{Code: exitToolError, Err: err}
	})
	f := cmd.Flags()
	f.StringVarP(&o.schema, "schema", "s", "", "path to schema Go file (default: auto-discover)")
	f.StringVarP(&o.config, "config", "c", "", "path to config YAML/JSON/TOML file")
	f.BoolVarP(&o.env, "env", "e", false, "also read from environment variables")
	f.BoolVar(&o.showValues, "show-values", false, "print resolved values (masks sensitive fields)")
	return cmd
}

// leafEntry is a validatable entry with its full dot-separated key.
type leafEntry struct {
	key string
	e   *ir.EntryIR
}

func leaves(entries []ir.EntryIR, parent string, out []leafEntry) []leafEntry {
	for i := range entries {
		e := &entries[i]
		key := e.ID
		if parent != "" {
			key = parent + "." + e.ID
		}
		switch e.Type {
		case ir.TypeObject:
			out = leaves(e.Properties, key, out)
		case ir.TypeComputed:
		default:
			out = append(out, leafEntry{key: key, e: e})
		}
	}
	return out
}

func runValidate(stdout, stderr io.Writer, o validateOptions) error {
	p, err := loadProject(stderr, o.schema, exitToolError)
	if err != nil {
		return err
	}
	ls := leaves(p.ir.Entries, "", nil)

	v := viper.New()
	for _, l := range ls {
		if l.e.Default != nil {
			v.SetDefault(l.key, l.e.Default)
		}
	}
	if o.config != "" {
		if _, err := os.Stat(o.config); err != nil {
			return &exitError{Code: exitToolError, Err: fmt.Errorf("config file: %w", err)}
		}
		v.SetConfigFile(o.config)
		if err := v.ReadInConfig(); err != nil {
			return &exitError{Code: exitToolError, Err: fmt.Errorf("reading config file %s: %w", o.config, err)}
		}
	}
	if o.env {
		v.AutomaticEnv()
		for _, l := range ls {
			if l.e.Env != "" {
				if err := v.BindEnv(l.key, l.e.Env); err != nil {
					return &exitError{Code: exitToolError, Err: err}
				}
			}
		}
	}

	var fieldErrs []cfgx.FieldError
	if err := runtime.ValidateSchema(p.ir, v); err != nil {
		var ve *cfgx.ValidationErrors
		if !errors.As(err, &ve) {
			return &exitError{Code: exitToolError, Err: err}
		}
		fieldErrs = ve.Errors
	}

	used := make([]bool, len(fieldErrs))
	for _, l := range ls {
		failed := false
		for i, fe := range fieldErrs {
			if fe.Field == l.key || strings.HasPrefix(fe.Field, l.key+"[") {
				fmt.Fprintf(stderr, "✗ %s: %s\n", fe.Field, fe.Message)
				used[i], failed = true, true
			}
		}
		if failed {
			continue
		}
		if o.showValues {
			fmt.Fprintf(stdout, "✓ %s = %s\n", l.key, showValue(v, l))
		} else {
			fmt.Fprintf(stdout, "✓ %s\n", l.key)
		}
	}
	for i, fe := range fieldErrs {
		if !used[i] {
			fmt.Fprintf(stderr, "✗ %s: %s\n", fe.Field, fe.Message)
		}
	}
	if n := len(fieldErrs); n > 0 {
		noun := "errors"
		if n == 1 {
			noun = "error"
		}
		fmt.Fprintf(stderr, "\n%d %s found.\n", n, noun)
		return &exitError{Code: exitInvalid}
	}
	return nil
}

// showValue renders the resolved value of l for --show-values. Sensitive
// values are never printed.
func showValue(v *viper.Viper, l leafEntry) string {
	if !v.IsSet(l.key) {
		if l.e.HasGetter {
			return "(from Getter at run time)"
		}
		return "(not set)"
	}
	if l.e.Sensitive {
		return "[redacted]"
	}
	return formatValue(l.e.Type, l.e.ElementType, v.Get(l.key))
}

func formatValue(t ir.EntryType, elem *ir.EntryIR, raw any) string {
	val, err := convert.ToType(t, raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	switch x := val.(type) {
	case string:
		return strconv.Quote(x)
	case time.Duration:
		return x.String()
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if elem != nil {
				parts[i] = formatValue(elem.Type, elem.ElementType, item)
			} else {
				parts[i] = fmt.Sprint(item)
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(val)
}
