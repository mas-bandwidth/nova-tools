package update

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const adoptionHeader = "tool\tfriend\tstate\tversion\tdetail"

var adoptionStates = []string{"evaluated", "useful-now", "tried", "adopted", "declined", "deferred", "unknown", "equivalent"}

func adoptionValid(s string) bool {
	for _, v := range adoptionStates {
		if v == s {
			return true
		}
	}
	return false
}

type adoption struct {
	Tool, Friend, State, Version, Detail string
}

func loadAdoption(r io.Reader) ([]adoption, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != adoptionHeader {
		return nil, fmt.Errorf("line 1: invalid header (put the header back exactly: %s)", adoptionHeader)
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

func adoptionVerb(name string, args []string, stamp string, out, errs io.Writer) int {
	o := struct {
		file, as string
		max      int
	}{max: 20}
	f := flag.NewFlagSet("adoption", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.file, "file", "", "adoption file")
	f.StringVar(&o.as, "as", "", "friend filter")
	f.IntVar(&o.max, "max", 20, "output cap")
	if err := f.Parse(interspersed(f, args)); err != nil {
		return refusal(errs, "ADOPTION", fmt.Errorf("%s (run %s help)", err, name))
	}
	if o.file == "" {
		return refusal(errs, "ADOPTION", fmt.Errorf("missing --file; refusing to guess (supply each named flag; run: %s help)", name))
	}
	if o.max < 0 {
		return refusal(errs, "ADOPTION", fmt.Errorf("invalid bound (use --max >= 0)"))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, "ADOPTION", fmt.Errorf("adoption takes no positional arguments (run %s help)", name))
	}
	file, err := os.Open(o.file)
	if err != nil {
		return refusal(errs, "ADOPTION", fmt.Errorf("cannot open %s (supply a readable --file: one line per tool choice, five tab-separated fields tool friend state version detail, written by hand)", o.file))
	}
	rows, err := loadAdoption(file)
	file.Close()
	if err != nil {
		return refusal(errs, "ADOPTION", fmt.Errorf("%s: %w", o.file, err))
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
	fmt.Fprintf(out, "ADOPTION at=%s file=%s entries=%d max=%d\n", field(stamp), field(o.file), len(selected), o.max)
	group := bounded.Grouped(out, o.max, "ADOPTION", "use --max 0 to show all")
	for _, r := range selected {
		group.Line("choice", fmt.Sprintf("ADOPTION tool=%s friend=%s state=%s version=%s detail=%s", field(r.Tool), field(r.Friend), field(r.State), field(r.Version), oneline.Escape(r.Detail)))
	}
	group.More()
	friends := map[string]bool{}
	for _, r := range selected {
		friends[r.Friend] = true
	}
	fmt.Fprintf(out, "ADOPTION OK entries=%d friends=%d file=%s\n", len(selected), len(friends), field(o.file))
	return 0
}
