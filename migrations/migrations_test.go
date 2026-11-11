package migrations

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Migrations up to this version ran when the tables were small, in a
// transaction; they are applied and stay as they are.
const lastUncheckedVersion = 48

var (
	createTable = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`)
	createIndex = regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(?:ONLY\s+)?(\w+)`)
)

// An index built on a table that already holds rows locks it against writes
// for the whole build unless it is built CONCURRENTLY, which goose can only
// do outside a transaction (see 00030_fk_indexes.sql).
func TestNewIndexesOnExistingTablesAreConcurrent(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Errorf("%s: name must start with a version number", name)
			continue
		}
		if version <= lastUncheckedVersion {
			continue
		}
		checked++
		raw, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range indexProblems(string(raw)) {
			t.Errorf("%s: %s", name, problem)
		}
	}
	if checked == 0 {
		t.Error("no migration after the unchecked ones was read")
	}
}

func indexProblems(sql string) []string {
	up, _, _ := strings.Cut(sql, "-- +goose Down")
	own := map[string]bool{}
	for _, m := range createTable.FindAllStringSubmatch(up, -1) {
		own[strings.ToLower(m[1])] = true
	}
	var out []string
	for _, m := range createIndex.FindAllStringSubmatch(up, -1) {
		concurrent, index, table := m[1] != "", m[2], strings.ToLower(m[3])
		switch {
		case own[table]: // created here, still empty
		case !concurrent:
			out = append(out, "index "+index+" on existing table "+table+" must be built with CREATE INDEX CONCURRENTLY")
		case !strings.Contains(sql, "-- +goose NO TRANSACTION"):
			out = append(out, "index "+index+" is CONCURRENTLY, so the file needs -- +goose NO TRANSACTION as its first line")
		}
	}
	return out
}

func TestIndexProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		want int
	}{
		"new table":        {"-- +goose Up\nCREATE TABLE t (id INT);\nCREATE INDEX t_idx ON t (id);", 0},
		"existing, locked": {"-- +goose Up\nCREATE UNIQUE INDEX IF NOT EXISTS p_idx ON posts (id);", 1},
		"in a transaction": {"-- +goose Up\nCREATE INDEX CONCURRENTLY p_idx ON posts (id);", 1},
		"correct":          {"-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY IF NOT EXISTS p_idx ON posts (id);", 0},
		"down is ignored":  {"-- +goose Up\nSELECT 1;\n-- +goose Down\nCREATE INDEX p_idx ON posts (id);", 0},
	} {
		if got := indexProblems(tc.sql); len(got) != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
