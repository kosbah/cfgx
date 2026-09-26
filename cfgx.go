package cfgx

import "fmt"

// Define constructs a Schema from the provided entries and constraints.
//
// Items are kept in declaration order. Define panics if an item is nil or if two
// entries share the same ID within the same parent scope (the root or an Object).
// References made by DependsOn and MutuallyExclusive are not checked here; the
// schema parser validates them.
func Define(items ...Item) *Schema {
	s := &Schema{}
	for i, it := range items {
		switch v := it.(type) {
		case nil:
			panic(fmt.Sprintf("cfgx: Define: item %d is nil", i))
		case Entry:
			s.entries = append(s.entries, v)
		case Constraint:
			s.constraints = append(s.constraints, v)
		default:
			panic(fmt.Sprintf("cfgx: Define: unsupported item type %T", it))
		}
	}
	checkDuplicates(s.entries, "")
	return s
}

// checkDuplicates panics if any two entries in the same scope share an ID. It
// recurses into Object properties.
func checkDuplicates(entries []Entry, parentPath string) {
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e == nil {
			panic(fmt.Sprintf("cfgx: nil entry in %s", scopeName(parentPath)))
		}
		id := e.id()
		if _, dup := seen[id]; dup {
			panic(fmt.Sprintf("cfgx: duplicate entry ID %q in %s", id, scopeName(parentPath)))
		}
		seen[id] = struct{}{}
		if o, ok := e.(*ObjectEntry); ok {
			checkDuplicates(o.properties, joinPath(parentPath, id))
		}
	}
}

func scopeName(parentPath string) string {
	if parentPath == "" {
		return "schema root"
	}
	return fmt.Sprintf("object %q", parentPath)
}

func joinPath(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "." + id
}
