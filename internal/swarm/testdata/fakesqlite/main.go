// The FAKE SQLITE3: a stand-in for the one program the OpenCode usage source runs, on PATH,
// so the reader is tested end to end with no sqlite3, no database and no provider.
//
// A real `sqlite3 -readonly -tabs <db> <query>` prints one line per row, its columns
// tab-separated, a NULL as the empty string. So the fake's database file IS those lines:
// the test (or the fake harness) writes the rows a real sqlite3 would print, and the fake
// prints them back. It fails the way a real one fails -- a message on stderr and a non-zero
// exit -- when the file cannot be read or does not hold a database.
package main

import (
	"fmt"
	"os"
	"strings"
)

// NotADatabase is the first line a test writes to make this fake refuse the file the way
// sqlite3 refuses bytes that are not a database. It is portable, where a mode that refuses
// its owner is a unix fact.
const NotADatabase = "not a database"

func main() {
	// argv is `-readonly -tabs <db> <query>`: the query is last, the database before it.
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: fake sqlite3 wants a database and a query")
		os.Exit(1)
	}
	db := args[len(args)-2]
	readonly := false
	for _, a := range args {
		if a == "-readonly" {
			readonly = true
		}
	}
	if !readonly {
		// The source is READ-ONLY, and this fake is where that is kept true: a caller that
		// forgets the flag gets a failure here rather than a green test.
		fmt.Fprintln(os.Stderr, "Error: fake sqlite3 refuses to open a database for writing")
		os.Exit(1)
	}
	raw, err := os.ReadFile(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: unable to open database \"%s\": %v\n", db, err)
		os.Exit(1)
	}
	body := string(raw)
	if strings.HasPrefix(body, NotADatabase) {
		fmt.Fprintf(os.Stderr, "Error: file is not a database\n")
		os.Exit(1)
	}
	fmt.Print(body)
}
