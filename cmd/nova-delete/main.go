// nova-delete moves literal paths to quarantine instead of deleting them (SPEC-DELETE.md).
// It takes exactly one literal absolute path and moves it to a dated quarantine folder
// under the allowed root. The sweep verb removes quarantine entries older than a
// specified duration. Exit 0 success, 1 refusal, 2 could not run.
//
// Allowed roots are the system temp directory and paths named by NOVA_DELETE_ROOTS.
package main

import (
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func main() { os.Exit(newTool().Main()) }

const exitCodes = "0 success, 1 refusal, 2 could not run (bad invocation)"

func newTool() *tool.Tool {
	return &tool.Tool{
		Name:      "nova-delete",
		What:      "moves a literal path to quarantine instead of deleting it",
		ExitTable: exitCodes,
		How: "takes exactly one literal absolute path and moves it to a dated quarantine folder\n" +
			"<root>/.quarantine-YYYYMMDD/<basename>.<HHMMSS>.<pid> under the allowed root that holds it.\n" +
			"Allowed roots are: system temp directory and paths named by NOVA_DELETE_ROOTS (colon-separated).\n" +
			"The sweep verb removes quarantine entries older than a specified duration.",
		Verbs: []tool.Verb{
			deleteVerb(),
			sweepVerb(),
		},
	}
}

func deleteVerb() tool.Verb {
	return tool.Verb{
		Name:     "delete",
		Usage:    "nova-delete delete <path>",
		Example:  "delete /tmp/file.txt",
		Effect:   tool.LocalWrite,
		Detail:   "Moves exactly one literal absolute path to a dated quarantine folder under the allowed root.",
		ExitTable: exitCodes,
		Flags:    deleteFlags,
		Run:      deleteRun,
	}
}

func deleteFlags(f *tool.Flags) {
	f.Required("path", "literal absolute path to move to quarantine")
}

func deleteRun(c *tool.Call) *tool.Out {
	path := c.Str("path")
	if path == "" {
		return tool.Refuse("missing path")
	}

	msg, exit, err := Delete(path)
	if err != nil {
		return tool.Refuse(err.Error())
	}

	if exit != 0 {
		return tool.Refuse(msg)
	}

	out := tool.Exit(0)
	out.ItemText("MOVED", msg)
	return out
}

func sweepVerb() tool.Verb {
	return tool.Verb{
		Name:     "sweep",
		Usage:    "nova-delete sweep --older-than <duration>",
		Example:  "sweep --older-than 7d",
		Effect:   tool.LocalWrite,
		Detail:   "Removes quarantine entries older than a specified duration under the allowed roots.",
		ExitTable: exitCodes,
		Flags:    sweepFlags,
		Run:      sweepRun,
	}
}

func sweepFlags(f *tool.Flags) {
	f.String("older-than", "", "remove entries older than this duration")
	f.Check(func(c *tool.Call) {
		if !c.Given("older-than") {
			c.Want("older-than", "duration")
		}
	})
}

func sweepRun(c *tool.Call) *tool.Out {
	olderThanStr := c.Str("older-than")
	if olderThanStr == "" {
		return tool.Refuse("--older-than is required")
	}

	olderThan, err := time.ParseDuration(olderThanStr)
	if err != nil {
		return tool.Refuse(err.Error())
	}

	swept, err := Sweep(olderThan)
	if err != nil {
		return tool.Exit(1)
	}

	out := tool.Exit(0)
	for _, p := range swept {
		out.ItemText("SWEPT", p)
	}
	return out
}
