package ws

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// ReadIDs reads the task ids a verb's --ids argument names through the one
// Parse (verbflag.ResolveIDs, #4399 round 5): "@<file>" reads the file
// ("@-" reads in), one or more ids per line split on whitespace or commas,
// '#' to the end of a line a comment; anything else is the ids themselves,
// comma- or space-separated. Duplicates are dropped, first occurrence kept,
// so the order is the file's. No verb calls it: verbflag.Parse resolves
// --ids before the verb runs, and a verb reads the list with verbflag.List.
func ReadIDs(arg string, in io.Reader) ([]string, error) {
	return verbflag.ResolveIDs(arg, in)
}
