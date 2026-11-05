package diagram

import "context"

// The relational schema model that live-database introspection produces
// (the TypeScript core's schema/types.ts), and the port that reads a
// database into it. Introspection is read only by construction: fixed
// catalog queries, never user SQL.

// SchemaFK is a foreign key of a column.
type SchemaFK struct {
	Table    string `json:"table"`
	Column   string `json:"column"`
	OnDelete string `json:"onDelete,omitempty"`
	OnUpdate string `json:"onUpdate,omitempty"`
	Name     string `json:"name,omitempty"`
	Group    string `json:"group,omitempty"`
}

// SchemaColumn is a column.
type SchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	PK       bool   `json:"pk"`
	Unique   bool   `json:"unique"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	// Generated is "identity", "autoincrement" or "as (<expr>)".
	Generated string    `json:"generated,omitempty"`
	Comment   string    `json:"comment,omitempty"`
	FK        *SchemaFK `json:"fk,omitempty"`
}

// SchemaIndex is an index on some columns.
type SchemaIndex struct {
	Name    string   `json:"name,omitempty"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// SchemaCheck is a check constraint.
type SchemaCheck struct {
	Name string `json:"name,omitempty"`
	Expr string `json:"expr"`
}

// SchemaTable is a table or a view.
type SchemaTable struct {
	Name    string         `json:"name"`
	Kind    string         `json:"kind"` // table | view
	Columns []SchemaColumn `json:"columns"`
	Indexes []SchemaIndex  `json:"indexes"`
	Checks  []SchemaCheck  `json:"checks"`
}

// SchemaEnum is an enumerated type.
type SchemaEnum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Schema is a database's tables and enums.
type Schema struct {
	Tables []SchemaTable `json:"tables"`
	Enums  []SchemaEnum  `json:"enums"`
}

// Introspected is a schema with what was left out on the way.
type Introspected struct {
	Schema   Schema
	Warnings []string
}

// SchemaCounts summarizes a schema.
type SchemaCounts struct {
	Tables  int `json:"tables"`
	Views   int `json:"views"`
	Columns int `json:"columns"`
	Enums   int `json:"enums"`
}

// Counts totals tables (views apart), views, columns and enums.
func (s Schema) Counts() SchemaCounts {
	var c SchemaCounts
	for _, t := range s.Tables {
		if t.Kind == "view" {
			c.Views++
		} else {
			c.Tables++
		}
		c.Columns += len(t.Columns)
	}
	c.Enums = len(s.Enums)
	return c
}

// Database dialects that can be introspected over the network.
const (
	DialectPostgres = "postgres"
	DialectMySQL    = "mysql"
)

// DBTarget is a live database to read. The credentials are used for that
// one connection: never stored, never logged, scrubbed from errors.
type DBTarget struct {
	Dialect  string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSL      bool
	Schema   string
}

// TargetBlockedError is a target the server refuses to connect to (SSRF
// guard); the message says why.
type TargetBlockedError struct{ Reason string }

func (e *TargetBlockedError) Error() string { return e.Reason }

// IntrospectFailedError is a read that did not work; the message is safe
// to show (credentials removed).
type IntrospectFailedError struct{ Msg string }

func (e *IntrospectFailedError) Error() string { return e.Msg }

// NotSQLiteError is an upload that is not a SQLite database.
type NotSQLiteError struct{}

func (NotSQLiteError) Error() string { return "That file is not a SQLite database" }

// SchemaIntrospector reads a database into the schema model.
type SchemaIntrospector interface {
	// Introspect connects to a live PostgreSQL or MySQL database, read
	// only. It returns *TargetBlockedError or *IntrospectFailedError.
	Introspect(ctx context.Context, t DBTarget) (Introspected, error)
	// IntrospectSQLite reads an uploaded SQLite file, opened read only. It
	// returns NotSQLiteError or *IntrospectFailedError.
	IntrospectSQLite(ctx context.Context, file []byte) (Introspected, error)
}
