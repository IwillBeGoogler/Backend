package http

// Schema tools: reverse-engineer a live PostgreSQL / MySQL database, or an
// uploaded SQLite file, into the schema model. Read only, a browser
// session only (a token cannot reach these). The credentials are used for
// that one connection and never stored or logged.

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

func (h *Handler) schemaRoutes(mux dgRouter) {
	mux.HandleFunc("GET "+DiagramPrefix+"/schema", h.dgSchemaSQL)
	if h.Schema == nil {
		return
	}
	mux.HandleFunc("POST "+DiagramPrefix+"/schema/introspect", h.dgIntrospect)
	mux.HandleFunc("POST "+DiagramPrefix+"/schema/introspect/sqlite", h.dgIntrospectSQLite)
}

func introspectJSON(r diagram.Introspected) map[string]any {
	return map[string]any{"schema": r.Schema, "warnings": warnList(r.Warnings), "counts": r.Schema.Counts()}
}

func introspectFail(w http.ResponseWriter, r *http.Request, err error) {
	var blocked *diagram.TargetBlockedError
	var failed *diagram.IntrospectFailedError
	var notSQLite diagram.NotSQLiteError
	switch {
	case errors.As(err, &blocked):
		dgFail(w, r, http.StatusUnprocessableEntity, "TARGET_BLOCKED", blocked.Reason, nil)
	case errors.As(err, &failed):
		dgFail(w, r, http.StatusUnprocessableEntity, "INTROSPECT_FAILED", failed.Msg, nil)
	case errors.As(err, &notSQLite):
		dgFail(w, r, http.StatusUnprocessableEntity, "NOT_SQLITE", notSQLite.Error(), nil)
	default:
		dgError(w, r, err)
	}
}

// dgIntrospect reverse-engineers a live database into the schema model.
//
// Body: {"dialect": "postgres"|"mysql", "host", "port"?, "user",
// "password"?, "database", "ssl"?, "schema"?}. Response: {"schema":
// {tables, enums}, "warnings", "counts": {tables, views, columns, enums}}.
// Private, loopback and link-local targets are 422 TARGET_BLOCKED unless
// SCHEMA_ALLOW_PRIVATE is set; a connection or query failure is 422
// INTROSPECT_FAILED (credentials removed). Ten calls a minute. Only fixed
// catalog queries run, in a read-only transaction with a statement timeout.
//
// spector:tags diagrams
func (h *Handler) dgIntrospect(w http.ResponseWriter, r *http.Request) {
	if tokenFrom(r.Context()) != nil {
		dgFail(w, r, http.StatusForbidden, "TOKEN_SCOPE", "This endpoint cannot be used with an access token", nil)
		return
	}
	if _, ok := h.dgUser(w, r); !ok || !h.dgAllow(w, r, "dg-introspect", 10, time.Minute) {
		return
	}
	var b struct {
		Dialect  string `json:"dialect"`
		Host     string `json:"host"`
		Port     *int   `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
		SSL      bool   `json:"ssl"`
		Schema   string `json:"schema"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	bad := func(msg string) { dgValidation(w, r, "body", msg) }
	b.Host = strings.TrimSpace(b.Host)
	switch {
	case b.Dialect != diagram.DialectPostgres && b.Dialect != diagram.DialectMySQL:
		bad("dialect: must be postgres or mysql")
	case b.Host == "" || len(b.Host) > 255:
		bad("host: 1-255 characters")
	case b.Port != nil && (*b.Port < 1 || *b.Port > 65535):
		bad("port: must be between 1 and 65535")
	case b.User == "" || utf8.RuneCountInString(b.User) > 128:
		bad("user: 1-128 characters")
	case utf8.RuneCountInString(b.Password) > 256:
		bad("password: at most 256 characters")
	case b.Database == "" || utf8.RuneCountInString(b.Database) > 128:
		bad("database: 1-128 characters")
	case utf8.RuneCountInString(b.Schema) > 128:
		bad("schema: at most 128 characters")
	default:
		t := diagram.DBTarget{Dialect: b.Dialect, Host: b.Host, User: b.User, Password: b.Password, Database: b.Database, SSL: b.SSL, Schema: b.Schema}
		if b.Port != nil {
			t.Port = *b.Port
		}
		res, err := h.Schema.Introspect(r.Context(), t)
		if err != nil {
			introspectFail(w, r, err)
			return
		}
		dgWrite(w, http.StatusOK, introspectJSON(res))
	}
}

// dgIntrospectSQLite reverse-engineers an uploaded SQLite database file
// (multipart field "file", at most the .sdd upload limit). The file is
// opened read only and deleted right after. 422 NOT_SQLITE for any other
// file. Same response as POST /schema/introspect. Ten calls a minute.
//
// spector:tags diagrams
func (h *Handler) dgIntrospectSQLite(w http.ResponseWriter, r *http.Request) {
	if tokenFrom(r.Context()) != nil {
		dgFail(w, r, http.StatusForbidden, "TOKEN_SCOPE", "This endpoint cannot be used with an access token", nil)
		return
	}
	if _, ok := h.dgUser(w, r); !ok || !h.dgAllow(w, r, "dg-introspect", 10, time.Minute) {
		return
	}
	_, data, ok := h.readSDD(w, r)
	if !ok {
		return
	}
	res, err := h.Schema.IntrospectSQLite(r.Context(), data)
	if err != nil {
		introspectFail(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, introspectJSON(res))
}

// dgSchemaSQL serves the app's own database schema as SQL DDL
// (application/sql) for the ER import. Admin only (diagrams.manage);
// ?prefix=dg_ keeps only the tables and enums starting with it.
//
// spector:tags diagrams
func (h *Handler) dgSchemaSQL(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.dgUser(w, r); !ok {
		return
	}
	prefix := r.URL.Query().Get("prefix")
	if len(prefix) > 63 {
		dgValidation(w, r, "query", "prefix: must be at most 63 characters")
		return
	}
	ddl, err := h.Diagrams.AdminSchemaSQL(r.Context(), prefix)
	switch {
	case errors.Is(err, domain.ErrForbidden):
		dgFail(w, r, http.StatusForbidden, "FORBIDDEN", "Administrator access required", nil)
		return
	case errors.Is(err, domain.ErrNotFound):
		dgFail(w, r, http.StatusNotFound, "NOT_FOUND", "Schema export is not available", nil)
		return
	case err != nil:
		dgError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/sql; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ddl))
}
