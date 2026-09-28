package main

import (
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"io"
	"strings"
)

// printVerbHelp prints the specific usage lines for a verb or family from the main usage string,
// then exits 2.
func printVerbHelp(stderr io.Writer, verb string) int {
	prefix1 := "  nova-work " + verb + " "
	prefix2 := "  nova-work " + verb + "\t"
	exact := "  nova-work " + verb

	lines := strings.Split(usage, "\n")
	capturing := false
	found := false
	for _, line := range lines {
		if capturing && strings.HasPrefix(line, "                           ") {
			fmt.Fprintln(stderr, line)
			continue
		}

		if strings.HasPrefix(line, prefix1) || strings.HasPrefix(line, prefix2) || line == exact {
			capturing = true
			found = true
			fmt.Fprintln(stderr, line)
		} else if capturing && strings.HasPrefix(line, "  nova-work ") {
			capturing = false
		}
	}
	if !found {
		if verb == "proving-run" {
			fmt.Fprintln(stderr, "  nova-work proving-run --nova-tools <path> [--schema <path>] [--sprint <path>] [--batch-size <n>] [--interrupt]")
			return 2
		}
		fmt.Fprintf(stderr, "nova-work: no specific help for %q\n", oneline.Field(verb))
	}
	return 2
}
