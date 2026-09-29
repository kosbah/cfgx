package ir

// Version is the SchemaIR format version produced by cfgx v1.
const Version = "1"

// SchemaIR is the serializable snapshot of a cfgx.Schema.
type SchemaIR struct {
	Version     string         `json:"version"` // always "1" for cfgx v1
	Entries     []EntryIR      `json:"entries"`
	Constraints []ConstraintIR `json:"constraints,omitempty"`
}

// EntryIR represents a single config entry at any nesting level.
type EntryIR struct {
	// Core identity
	ID   string    `json:"id"`   // e.g. "port", "db", "db.host"
	Type EntryType `json:"type"` // see EntryType constants

	// Flag & env
	FlagLong  string `json:"flag_long,omitempty"`  // "--port"
	FlagShort string `json:"flag_short,omitempty"` // "-p"
	Env       string `json:"env,omitempty"`        // "APP_PORT"

	// Value
	Default     any    `json:"default,omitempty"` // literal; type must match Type
	Description string `json:"description,omitempty"`

	// Behaviour flags
	Required   bool   `json:"required,omitempty"`
	Sensitive  bool   `json:"sensitive,omitempty"`
	IsLive     bool   `json:"is_live,omitempty"`
	Deprecated string `json:"deprecated,omitempty"` // non-empty = deprecated; value = message

	// Runtime function sentinels (closures are NOT serialized)
	HasOnChange bool `json:"has_on_change,omitempty"`
	HasGetter   bool `json:"has_getter,omitempty"`
	HasValidate bool `json:"has_validate,omitempty"` // custom; built-ins are in BuiltinValidators

	// Built-in validators (serializable)
	BuiltinValidators []ValidatorIR `json:"builtin_validators,omitempty"`

	// Organisation
	Scope     []string     `json:"scope,omitempty"` // command names; empty = all commands
	Tags      []string     `json:"tags,omitempty"`
	DependsOn *DependsOnIR `json:"depends_on,omitempty"`

	// Nested (Object only)
	Properties []EntryIR `json:"properties,omitempty"`

	// List element type (List only)
	ElementType *EntryIR `json:"element_type,omitempty"`

	// Computed result type (Computed only): the Go type T of Computed[T] as
	// written in source, e.g. "string", "time.Duration", "[]string" or "any".
	// Empty when it cannot be determined statically; generators fall back to any.
	ResultType string `json:"result_type,omitempty"`
}

// EntryType enumerates the supported config entry types.
type EntryType string

// Supported entry types.
const (
	TypeString   EntryType = "string"
	TypeInt      EntryType = "int"
	TypeBool     EntryType = "bool"
	TypeFloat64  EntryType = "float64"
	TypeDuration EntryType = "duration" // stored as time.Duration (int64 nanoseconds)
	TypeList     EntryType = "list"
	TypeObject   EntryType = "object"
	TypeComputed EntryType = "computed"
)

// DependsOnIR describes a conditional activation rule.
type DependsOnIR struct {
	ID    string `json:"id"`    // ID of the controlling entry
	Value any    `json:"value"` // required value to activate this entry
}

// ConstraintIR represents a schema-level constraint.
type ConstraintIR struct {
	Type     ConstraintType `json:"type"`
	EntryIDs []string       `json:"entry_ids"`
}

// ConstraintType enumerates schema-level constraint types.
type ConstraintType string

// Supported constraint types.
const (
	ConstraintMutuallyExclusive ConstraintType = "mutually_exclusive"
)

// ValidatorIR represents a reference to a built-in validator with its parameters.
type ValidatorIR struct {
	Name   string         `json:"name"`             // e.g. "email", "port_range", "enum"
	Params map[string]any `json:"params,omitempty"` // e.g. {"min":1024,"max":65535}
}
