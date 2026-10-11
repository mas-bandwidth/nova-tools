// example.go holds the example verb: its flags, its run and the helpers only it uses.

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// cmdExample writes the example pages into a directory of the caller's: a local write and the
// only write this tool makes. A page already there with the same bytes is kept; one with other
// bytes is never replaced, and the run refuses before writing anything.
// It implements SPEC.md's nova-self-talk example contract; NoReplace publishes whole pages.
func cmdExample(args []string, stdout, stderr io.Writer) int {
	asJSON := verbflag.BoolAsked(args, "json")
	fset := verbflag.New("example")
	dry := fset.Bool("dry-run", false, "print what would be written and write nothing")
	fset.Bool("json", false, "print the result as one JSON object on stdout instead of a line")
	if err := verbflag.Parse(fset, args); err != nil {
		return refuse(stdout, stderr, asJSON, "example", "", oneline.Cap(err.Error(), oneline.TailBytes)+"; the flags are --dry-run, --json")
	}
	if fset.NArg() != 1 {
		return refuse(stdout, stderr, asJSON, "example", "",
			fmt.Sprintf("takes one directory to write the pages into, got %d arguments: nova-self-talk example ./pages", fset.NArg()))
	}
	dir := fset.Arg(0)
	if oneline.Escape(dir) != dir {
		return refuse(stdout, stderr, asJSON, "example", "",
			"directory path contains characters the one-line output must escape, so its follow-up command would not name the same path")
	}
	entries, err := pages.ReadDir("testdata/example-pages")
	if err != nil {
		return refuse(stdout, stderr, asJSON, "example", "", "the pages built into this binary cannot be read: "+err.Error())
	}
	// Every problem is found before anything is written; the strings are concatenated, and
	// refuse escapes each one where it prints it.
	var write, kept, problems []string
	bodies := map[string][]byte{}
	for _, e := range entries {
		body, err := pages.ReadFile(path.Join("testdata/example-pages", e.Name()))
		if err != nil {
			return refuse(stdout, stderr, asJSON, "example", "", "the pages built into this binary cannot be read: "+err.Error())
		}
		target := filepath.Join(dir, e.Name())
		switch have, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(have, body):
			kept = append(kept, e.Name())
		case err == nil:
			problems = append(problems, oneline.Quote(target)+" exists with other content and is never replaced; name an empty directory")
		case os.IsNotExist(err):
			write, bodies[e.Name()] = append(write, e.Name()), body
		default:
			problems = append(problems, "cannot read "+oneline.Quote(target)+" to compare: "+reason(err))
		}
	}
	if len(problems) > 0 {
		return refuse(stdout, stderr, asJSON, "example", "", problems...)
	}
	if !*dry {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refuse(stdout, stderr, asJSON, "example", "", "cannot make "+oneline.Quote(dir)+": "+reason(err))
		}
		for _, name := range write {
			if err := atomicfile.WriteFile(filepath.Join(dir, name), bodies[name], 0o644, atomicfile.NoReplace()); err != nil {
				return refuse(stdout, stderr, asJSON, "example", "", "cannot write "+oneline.Quote(filepath.Join(dir, name))+": "+reason(err))
			}
		}
	}
	wrote, next := "wrote", exampleNext("", filepath.Join(dir, "journal.md"))
	if *dry {
		wrote, next = "would-write", exampleNext("example", dir)
	}
	if asJSON {
		o := tool.Done()
		o.Verb, o.Remedy = "example", next
		o.Fact("dir", dir).Fact(wrote, strings.Join(write, ",")).Fact("kept", strings.Join(kept, ","))
		o.Render(stdout, true)
		return 0
	}
	fmt.Fprintf(stdout, "EXAMPLE OK dir=%s %s=%s kept=%s; run: %s\n", oneline.Field(dir), oneline.Field(wrote),
		oneline.Field(cmp.Or(strings.Join(write, ","), "-")), oneline.Field(cmp.Or(strings.Join(kept, ","), "-")), oneline.Escape(next))
	return 0
}

// exampleNext implements SPEC.md §2's runnable next-command rule: it keeps the path one
// POSIX-shell argument, and ends flags when the path begins with a dash.
func exampleNext(verb, file string) string {
	command := "nova-self-talk"
	if verb != "" {
		command += " " + verb
	}
	if strings.HasPrefix(file, "-") {
		command += " --"
	}
	return command + " " + oneline.ShellWord(file)
}
