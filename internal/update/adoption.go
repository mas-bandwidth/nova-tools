package update

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const adoptionHeader = "tool\tfriend\tstate\tversion\tdetail"

var adoptionStates = []string{"evaluated", "useful-now", "tried", "adopted", "declined", "deferred", "unknown", "equivalent"}

func adoptionValid(s string) bool {
	return slices.Contains(adoptionStates, s)
}

type adoption struct {
	Tool, Friend, State, Version, Detail string
}

func loadAdoption(r io.Reader) ([]adoption, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != adoptionHeader {
		return nil, fmt.Errorf("line 1: invalid header (put the header back exactly: %s)", tabbed(adoptionHeader))
	}
	var out []adoption
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 5 {
			return nil, fmt.Errorf("line %d: %d fields, want 5 (use the five-column TSV header)", line, len(f))
		}
		if f[0] == "" || f[1] == "" || f[2] == "" || f[3] == "" || f[4] == "" {
			return nil, fmt.Errorf("line %d: empty field (supply all five fields; version and detail may be -)", line)
		}
		if !adoptionValid(f[2]) {
			return nil, fmt.Errorf("line %d: unknown state %s (use evaluated,useful-now,tried,adopted,declined,deferred,unknown,equivalent)", line, f[2])
		}
		out = append(out, adoption{Tool: f[0], Friend: f[1], State: f[2], Version: f[3], Detail: f[4]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: unreadable adoption file (use lines below 1 MiB)", line)
	}
	return out, nil
}

// adoptionVerb reads the adoption ledger and lists each choice: its one value is
// the count on the first line and an item per choice, the choice's detail its prose.
func adoptionVerb(name string, args []string, out, errs io.Writer) int {
	o := struct {
		file, as string
		max      int
		json     bool
	}{max: 20}
	f := flag.NewFlagSet("adoption", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "the adoption ledger (required): "+adoptionShape)
	f.StringVar(&o.as, "as", "", "list only this friend's choices")
	f.IntVar(&o.max, "max", 20, "choices listed before one MORE line stands for the rest; 0 lists all")
	f.BoolVar(&o.json, "json", false, "print the result as one JSON object instead of lines")
	help := name + " adoption -h"
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		return emit(refused("adoption", help, flagProblem(f, err).Error()), verbflag.BoolAsked(args, "json"), 0, out, errs)
	}
	// Every problem of the invocation in one refusal (STANDARD §2).
	var problems []string
	if o.file == "" {
		problems = append(problems, "missing --file; refusing to guess")
	}
	if o.max < 0 {
		problems = append(problems, fmt.Sprintf("--max wants 0 or more (0 shows all), got %d", o.max))
	}
	if len(f.Args()) != 0 {
		problems = append(problems, fmt.Sprintf("adoption takes no positional arguments, got %q", f.Arg(0)))
	}
	if len(problems) > 0 {
		return emit(refused("adoption", help, strings.Join(problems, "; ")), o.json, 0, out, errs)
	}
	file, err := os.Open(o.file)
	if err != nil {
		return emit(refused("adoption", help, fmt.Sprintf("cannot open %s (supply a readable --file: %s)", o.file, adoptionShape)), o.json, 0, out, errs)
	}
	rows, err := loadAdoption(file)
	_ = file.Close() // ignored: file was opened only to be read
	if err != nil {
		return emit(refused("adoption", help, fmt.Sprintf("%s: %s", o.file, err)), o.json, 0, out, errs)
	}
	selected := []adoption{}
	for _, r := range rows {
		if o.as == "" || r.Friend == o.as {
			selected = append(selected, r)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Tool == selected[j].Tool {
			return selected[i].Friend < selected[j].Friend
		}
		return selected[i].Tool < selected[j].Tool
	})
	res := &tool.Out{Verb: "adoption", Status: tool.OK}
	friends := map[string]bool{}
	for _, r := range selected {
		friends[r.Friend] = true
		res.Item("choice", "tool", r.Tool, "friend", r.Friend, "state", r.State, "version", r.Version, "detail", r.Detail)
	}
	res.Fact("entries", len(selected)).Fact("friends", len(friends)).Fact("file", o.file).Fact("max", o.max)
	return emit(res, o.json, o.max, out, errs)
}

// adoptionShape is what the file adoption --file names holds.
const adoptionShape = "one line per tool choice, five tab-separated fields tool friend state version detail, written by hand"
