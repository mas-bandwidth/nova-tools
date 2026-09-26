package task

import (
	"regexp"
	"strings"
)

// Key is the task hash task:<id>.
func Key(sprint, id string) string { return "task:" + id }

// Field is one HMGET cell: Set is false for a nil reply.
type Field struct {
	Value string
	Set   bool
}

var fieldBoundaryRx = regexp.MustCompile(`\|\s*\**[A-Za-z][A-Za-z0-9-]*:`)

// TitleField returns the text of the `| NAME: ...` segment of a title. A
// segment ends only where the next `| NAME:` begins, so a DONE-WHEN that
// quotes `-run \x27A|B\x27` is not cut at its own pipes.
func TitleField(title, name string) (string, bool) {
	starts := []int{0}
	for _, m := range fieldBoundaryRx.FindAllStringIndex(title, -1) {
		starts = append(starts, m[0])
	}
	for i, s := range starts {
		end := len(title)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		seg := strings.TrimSpace(strings.TrimPrefix(title[s:end], "|"))
		seg = strings.TrimPrefix(seg, "**")
		if rest, ok := strings.CutPrefix(seg, name+":"); ok {
			return strings.TrimSpace(strings.ReplaceAll(rest, "**", "")), true
		}
	}
	return "", false
}
