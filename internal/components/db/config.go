package db

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Config struct {
	SchemaName string `yaml:"schemaName"`
}

// identifier is a PostgreSQL name that needs no quotes. Lowercase only: PostgreSQL folds an
// unquoted name to lowercase, but a connection string's role name keeps its case.
var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// maxSchemaName leaves room for the longest name the templates derive from the schema, the
// trigger t_<schema>_users_update_modified_time, within PostgreSQL's 63-byte limit, which
// silently truncates longer names.
const maxSchemaName = 63 - len("t__users_update_modified_time")

// reservedWords are the key words PostgreSQL bars from an unquoted schema or database name: its
// RESERVED_KEYWORD and TYPE_FUNC_NAME_KEYWORD entries in src/include/parser/kwlist.h.
var reservedWords = []string{
	"all", "analyse", "analyze", "and", "any", "array", "as", "asc", "asymmetric", "both", "case",
	"cast", "check", "collate", "column", "constraint", "create", "current_catalog", "current_date",
	"current_role", "current_time", "current_timestamp", "current_user", "default", "deferrable",
	"desc", "distinct", "do", "else", "end", "except", "false", "fetch", "for", "foreign", "from",
	"grant", "group", "having", "in", "initially", "intersect", "into", "lateral", "leading",
	"limit", "localtime", "localtimestamp", "not", "null", "offset", "on", "only", "or", "order",
	"placing", "primary", "references", "returning", "select", "session_user", "some", "symmetric",
	"system_user", "table", "then", "to", "trailing", "true", "union", "unique", "user", "using",
	"variadic", "when", "where", "window", "with",
	// TYPE_FUNC_NAME_KEYWORD
	"authorization", "binary", "collation", "concurrently", "cross", "current_schema", "freeze",
	"full", "ilike", "inner", "is", "isnull", "join", "left", "like", "natural", "notnull", "outer",
	"overlaps", "right", "similar", "tablesample", "verbose",
}

// existingNames are schemas and databases every PostgreSQL cluster has, which the templates'
// CREATE SCHEMA and CREATE DATABASE would collide with.
var existingNames = []string{"public", "information_schema", "postgres", "template0", "template1"}

// Validate reports why config cannot generate valid SQL. The schema name goes into the
// templates verbatim.
func (c Config) Validate() error {
	name := c.SchemaName
	switch {
	case name == "":
		return fmt.Errorf("db.schemaName is required")
	case !identifier.MatchString(name):
		return fmt.Errorf("db.schemaName %q must start with a lowercase letter or underscore and contain only lowercase letters, digits, and underscores", name)
	case len(name) > maxSchemaName:
		return fmt.Errorf("db.schemaName %q has %d characters; the limit is %d, which keeps the names derived from it within PostgreSQL's 63", name, len(name), maxSchemaName)
	case slices.Contains(reservedWords, name):
		return fmt.Errorf("db.schemaName %q is a reserved word in PostgreSQL", name)
	case slices.Contains(existingNames, name):
		return fmt.Errorf("db.schemaName %q names a schema or database every PostgreSQL cluster already has", name)
	case strings.HasPrefix(name, "pg_"):
		return fmt.Errorf("db.schemaName %q starts with pg_, which PostgreSQL reserves for system schemas", name)
	}
	return nil
}
