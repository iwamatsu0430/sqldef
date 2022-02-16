// Integration test of sqlite3def command.
//
// Test requirement:
//   - go command
//   - `sqlite3` must succeed
package main

import (
	"github.com/k0kubun/sqldef/adapter"
	"github.com/k0kubun/sqldef/adapter/sqlite3"
	"github.com/k0kubun/sqldef/cmd/testutils"
	"github.com/k0kubun/sqldef/schema"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	applyPrefix     = "-- Apply --\n"
	nothingModified = "-- Nothing is modified --\n"
)

func TestApply(t *testing.T) {
	defer testutils.MustExecute("rm", "-f", "sqlite3def_test") // after-test cleanup

	tests, err := testutils.ReadTests("tests/*.yml")
	if err != nil {
		t.Fatal(err)
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Initialize the database with test.Current
			testutils.MustExecute("rm", "-f", "sqlite3def_test")
			db, err := connectDatabase() // re-connection seems needed after rm
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if test.Current != "" {
				ddls, err := testutils.SplitDDLs(schema.GeneratorModeSQLite3, test.Current)
				if err != nil {
					t.Fatal(err)
				}
				err = testutils.RunDDLs(db, ddls)
				if err != nil {
					t.Fatal(err)
				}
			}

			// Main test
			dumpDDLs, err := adapter.DumpDDLs(db)
			if err != nil {
				log.Fatal(err)
			}
			ddls, err := schema.GenerateIdempotentDDLs(schema.GeneratorModeSQLite3, test.Desired, dumpDDLs)
			if err != nil {
				t.Fatal(err)
			}
			expected := test.Output
			actual := testutils.JoinDDLs(ddls)
			if expected != actual {
				t.Errorf("\nexpected:\n```\n%s```\n\nactual:\n```\n%s```", expected, actual)
			}
			err = testutils.RunDDLs(db, ddls)
			if err != nil {
				t.Fatal(err)
			}

			// Test idempotency
			dumpDDLs, err = adapter.DumpDDLs(db)
			if err != nil {
				log.Fatal(err)
			}
			ddls, err = schema.GenerateIdempotentDDLs(schema.GeneratorModeSQLite3, test.Desired, dumpDDLs)
			if err != nil {
				t.Fatal(err)
			}
			if len(ddls) > 0 {
				t.Errorf("expected nothing is modifed, but got:\n```\n%s```", testutils.JoinDDLs(ddls))
			}
		})
	}
}

// TODO: Most of the following tests should be migrated to TestApply

func TestSQLite3defCreateTable(t *testing.T) {
	resetTestDatabase()

	createTable1 := stripHeredoc(`
		CREATE TABLE users (
		  id integer NOT NULL,
		  name text,
		  age integer
		);
		`,
	)
	createTable2 := stripHeredoc(`
		CREATE TABLE bigdata (
		  data integer
		);
		`,
	)

	assertApplyOutput(t, createTable1+createTable2, applyPrefix+createTable1+createTable2)
	assertApplyOutput(t, createTable1+createTable2, nothingModified)

	assertApplyOutput(t, createTable1, applyPrefix+"DROP TABLE `bigdata`;\n")
	assertApplyOutput(t, createTable1, nothingModified)
}

func TestSQLite3defCreateTableQuotes(t *testing.T) {
	resetTestDatabase()

	createTable := stripHeredoc(`
		CREATE TABLE "test_table" (
		  id integer primary key
		);
		`,
	)
	assertApplyOutput(t, createTable, applyPrefix+createTable)
	assertApplyOutput(t, createTable, nothingModified)

	createTable = stripHeredoc(
		"CREATE TABLE `test_table` (\n" +
			"  id integer primary key\n" +
			");\n",
	)
	assertApplyOutput(t, createTable, nothingModified)
}

func TestSQLite3defCreateTableWithAutoincrement(t *testing.T) {
	resetTestDatabase()

	createTable := stripHeredoc(`
		CREATE TABLE users (
		  id integer PRIMARY KEY AUTOINCREMENT,
		  name text,
		  age integer
		);
		`,
	)

	assertApplyOutput(t, createTable, applyPrefix+createTable)
	assertApplyOutput(t, createTable, nothingModified)
}

func TestSQLite3defCreateView(t *testing.T) {
	resetTestDatabase()

	createTable := stripHeredoc(`
		CREATE TABLE users (
		  id integer NOT NULL,
		  name text,
		  age integer
		);
		`,
	)
	assertApplyOutput(t, createTable, applyPrefix+createTable)
	assertApplyOutput(t, createTable, nothingModified)

	createView := stripHeredoc(`
		CREATE VIEW ` + "`view_users`" + ` AS select id from users where age = 1;
		`,
	)
	assertApplyOutput(t, createTable+createView, applyPrefix+createView)
	assertApplyOutput(t, createTable+createView, nothingModified)

	createView = stripHeredoc(`
		CREATE VIEW ` + "`view_users`" + ` AS select id from users where age = 2;
		`,
	)
	dropView := stripHeredoc(`
		DROP VIEW ` + "`view_users`" + `;
		`,
	)
	assertApplyOutput(t, createTable+createView, applyPrefix+dropView+createView)
	assertApplyOutput(t, createTable+createView, nothingModified)

	assertApplyOutput(t, "", applyPrefix+"DROP TABLE `users`;\n"+dropView)
	//assertApplyOutput(t, "", nothingModified)
}

func TestSQLite3defColumnLiteral(t *testing.T) {
	resetTestDatabase()

	createTable := stripHeredoc(`
		CREATE TABLE users (
		  id integer NOT NULL,
		  name text,
		  age integer
		);
		`,
	)
	assertApplyOutput(t, createTable, applyPrefix+createTable)
	assertApplyOutput(t, createTable, nothingModified)
}

func TestSQLite3defDataTypes(t *testing.T) {
	resetTestDatabase()

	// Remaining SQL spec: bit varying, interval, numeric, decimal, real,
	//   smallint, smallserial, xml
	createTable := stripHeredoc(`
		CREATE TABLE users (
		  c_timestamp timestamp,
		  c_integer integer,
		  c_text text
		);
		`,
	)

	assertApplyOutput(t, createTable, applyPrefix+createTable)
	assertApplyOutput(t, createTable, nothingModified) // Label for column type may change. Type will be examined.
}

//
// ----------------------- following tests are for CLI -----------------------
//

func TestSQLite3defDryRun(t *testing.T) {
	resetTestDatabase()
	writeFile("schema.sql", stripHeredoc(`
	    CREATE TABLE users (
	        id integer NOT NULL PRIMARY KEY,
	        age integer
	    );`,
	))

	dryRun := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--dry-run", "--file", "schema.sql")
	apply := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--file", "schema.sql")
	assertEquals(t, dryRun, strings.Replace(apply, "Apply", "dry run", 1))
}

func TestSQLite3defSkipDrop(t *testing.T) {
	resetTestDatabase()
	mustExecute("sqlite3", "sqlite3def_test", stripHeredoc(`
		CREATE TABLE users (
		    id integer NOT NULL PRIMARY KEY,
		    age integer
		);`,
	))

	writeFile("schema.sql", "")

	skipDrop := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--skip-drop", "--file", "schema.sql")
	apply := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--file", "schema.sql")
	assertEquals(t, skipDrop, strings.Replace(apply, "DROP", "-- Skipped: DROP", 1))
}

func TestSQLite3defExport(t *testing.T) {
	resetTestDatabase()
	out := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--export")
	assertEquals(t, out, "-- No table exists --\n")

	mustExecute("sqlite3", "sqlite3def_test", stripHeredoc(`
		CREATE TABLE users (
		    id integer NOT NULL PRIMARY KEY,
		    age integer
		);`,
	))
	out = assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--export")
	assertEquals(t, out, stripHeredoc(`
		CREATE TABLE users (
		    id integer NOT NULL PRIMARY KEY,
		    age integer
		);
		`,
	))
}

func TestSQLite3defHelp(t *testing.T) {
	_, err := execute("./sqlite3def", "--help")
	if err != nil {
		t.Errorf("failed to run --help: %s", err)
	}

	out, err := execute("./sqlite3def")
	if err == nil {
		t.Errorf("no database must be error, but successfully got: %s", out)
	}
}

func TestMain(m *testing.M) {
	resetTestDatabase()
	mustExecute("go", "build")
	status := m.Run()
	_ = os.Remove("sqlite3def")
	_ = os.Remove("sqlite3def_test")
	_ = os.Remove("schema.sql")
	os.Exit(status)
}

func assertApplyOutput(t *testing.T, schema string, expected string) {
	t.Helper()
	writeFile("schema.sql", schema)
	actual := assertedExecute(t, "./sqlite3def", "sqlite3def_test", "--file", "schema.sql")
	assertEquals(t, actual, expected)
}

func mustExecute(command string, args ...string) {
	out, err := execute(command, args...)
	if err != nil {
		log.Printf("failed to execute '%s %s': `%s`", command, strings.Join(args, " "), out)
		log.Fatal(err)
	}
}

func assertedExecute(t *testing.T, command string, args ...string) string {
	t.Helper()
	out, err := execute(command, args...)
	if err != nil {
		t.Errorf("failed to execute '%s %s' (error: '%s'): `%s`", command, strings.Join(args, " "), err, out)
	}
	return out
}

func assertEquals(t *testing.T, actual string, expected string) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected '%s' but got '%s'", expected, actual)
	}
}

func execute(command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func resetTestDatabase() {
	_, err := os.Stat("sqlite3def_test")
	if err == nil {
		err := os.Remove("sqlite3def_test")
		if err != nil {
			log.Fatal(err)
		}
	}
}

func writeFile(path string, content string) {
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	file.Write(([]byte)(content))
}

func stripHeredoc(heredoc string) string {
	heredoc = strings.TrimPrefix(heredoc, "\n")
	re := regexp.MustCompilePOSIX("^\t*")
	return re.ReplaceAllLiteralString(heredoc, "")
}
func connectDatabase() (adapter.Database, error) {
	return sqlite3.NewDatabase(adapter.Config{
		DbName: "sqlite3def_test",
	})
}
