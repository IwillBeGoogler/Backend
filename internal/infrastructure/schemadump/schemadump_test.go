package schemadump

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	s := Schema{
		Enums: []Enum{{Name: "dg_role", Values: []string{"owner", "it's"}, Comment: "who"}},
		Tables: []Table{
			{Name: "dg_files", Columns: []Column{
				{Name: "id", Type: "text", NotNull: true},
				{Name: "project_id", Type: "text", NotNull: true},
				{Name: "role", Type: "dg_role", Default: "'owner'::dg_role", Comment: "the role"},
			}, Constraints: []Constraint{
				{Name: "dg_files_pkey", Def: "PRIMARY KEY (id)"},
				{Name: "dg_files_project_id_fkey", Def: "FOREIGN KEY (project_id) REFERENCES dg_projects(id) ON DELETE CASCADE"},
				{Name: "dg_files_id_check", Def: "CHECK ((length(id) > 0))", Comment: "non-empty"},
			}, Indexes: []Index{{Name: "dg_files_project_idx", Def: "CREATE INDEX dg_files_project_idx ON public.dg_files USING btree (project_id)"}},
				Comment: "files"},
			{Name: "User", Columns: []Column{{Name: "id", Type: "integer", NotNull: true, Default: "0"}}},
		},
	}
	got := Render(s)
	for _, want := range []string{
		"CREATE TYPE dg_role AS ENUM ('owner', 'it''s');\n",
		"COMMENT ON TYPE dg_role IS 'who';\n",
		"CREATE TABLE dg_files (\n    id text NOT NULL,\n    project_id text NOT NULL,\n    role dg_role DEFAULT 'owner'::dg_role,\n",
		"    CONSTRAINT dg_files_pkey PRIMARY KEY (id),\n",
		"    CONSTRAINT dg_files_project_id_fkey FOREIGN KEY (project_id) REFERENCES dg_projects(id) ON DELETE CASCADE,\n    CONSTRAINT dg_files_id_check CHECK ((length(id) > 0))\n);\n",
		"CREATE INDEX dg_files_project_idx ON public.dg_files USING btree (project_id);\n",
		"COMMENT ON TABLE dg_files IS 'files';\n",
		"COMMENT ON COLUMN dg_files.role IS 'the role';\n",
		"COMMENT ON CONSTRAINT dg_files_id_check ON dg_files IS 'non-empty';\n",
		"CREATE TABLE \"User\" (\n    id integer NOT NULL DEFAULT 0\n);\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "CREATE TYPE") > strings.Index(got, "CREATE TABLE") {
		t.Error("enums must come before tables")
	}
	if strings.Index(got, `"User"`) > strings.Index(got, "dg_files") {
		t.Error("tables must be sorted by name")
	}
	if Render(s) != got {
		t.Error("output is not deterministic")
	}
}

func TestRenderEmpty(t *testing.T) {
	if got := Render(Schema{}); got != "" {
		t.Errorf("Render(empty) = %q", got)
	}
}

func TestQuoteIdent(t *testing.T) {
	for in, want := range map[string]string{"users": "users", "dg_x1": "dg_x1", "1a": `"1a"`, "A": `"A"`, "user": `"user"`, `a"b`: `"a""b"`, "": `""`} {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
