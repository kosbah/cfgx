package codegen

import (
	"go/token"
	"strings"
	"unicode"
	"unicode/utf8"
)

// acronyms are written in upper case when they form a whole word of an ID.
var acronyms = map[string]bool{
	"ID":   true,
	"URL":  true,
	"TLS":  true,
	"HTTP": true,
	"API":  true,
	"JSON": true,
	"SQL":  true,
	"DSN":  true,
}

// splitWords splits an ID (or a dot path of IDs) into words at every
// character that is not a letter or digit.
func splitWords(id string) []string {
	return strings.FieldsFunc(id, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// exportWord capitalizes one word, upper-casing it entirely if it is a known acronym.
func exportWord(w string) string {
	if up := strings.ToUpper(w); acronyms[up] {
		return up
	}
	r, n := utf8.DecodeRuneInString(w)
	return string(unicode.ToUpper(r)) + w[n:]
}

// ToGoName converts a kebab-case or snake_case ID to a PascalCase Go
// identifier. Words that are common acronyms (ID, URL, TLS, HTTP, API, JSON,
// SQL, DSN) are upper-cased; other words only have their first letter
// capitalized.
//
//	"port"          → "Port"
//	"app-name"      → "AppName"
//	"tls-cert-path" → "TLSCertPath"
//	"db"            → "Db"
//	"api_key"       → "APIKey"
//	"dsn"           → "DSN"
//
// The result may not be a valid identifier (e.g. for an ID starting with a
// digit); callers must check it.
func ToGoName(id string) string {
	var b strings.Builder
	for _, w := range splitWords(id) {
		b.WriteString(exportWord(w))
	}
	return b.String()
}

// ToStructName returns the PascalCase struct name for an Object entry.
//
//	"db"   → "DbConfig"
//	"smtp" → "SmtpConfig"
//	"tls"  → "TLSConfig"
func ToStructName(id string) string {
	return ToGoName(id) + "Config"
}

// ToViperKey returns the viper key of the entry id nested under parentPath
// (itself a viper key, or "" at the root).
//
//	ToViperKey("", "port")   → "port"
//	ToViperKey("db", "host") → "db.host"
func ToViperKey(parentPath, id string) string {
	if parentPath == "" {
		return id
	}
	return parentPath + "." + id
}

// toFieldName returns the unexported struct field name for an ID: the Go name
// with its first word in lower case ("tls-cert-path" → "tlsCertPath",
// "dsn" → "dsn"). A Go keyword gets a trailing underscore ("type" → "type_").
func toFieldName(id string) string {
	words := splitWords(id)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	first := words[0]
	if acronyms[strings.ToUpper(first)] {
		b.WriteString(strings.ToLower(first))
	} else {
		r, n := utf8.DecodeRuneInString(first)
		b.WriteRune(unicode.ToLower(r))
		b.WriteString(first[n:])
	}
	for _, w := range words[1:] {
		b.WriteString(exportWord(w))
	}
	name := b.String()
	if token.IsKeyword(name) {
		name += "_"
	}
	return name
}

// isExportedIdent reports whether name is a valid exported Go identifier.
func isExportedIdent(name string) bool {
	return token.IsIdentifier(name) && token.IsExported(name)
}
