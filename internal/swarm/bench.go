// Benches: a remote bench reached by ssh, named by one row of a TSV table
// (docs/SPEC-SWARM.md "Benches"). This file holds the shared types and the two
// reads every bench caller shares: the table, and the bus's friends list a bench
// name is checked against. Nothing here runs a bench or writes a packet; those are
// the batch and probe callers, in cmd/nova-swarm.
package swarm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Bench is one row of the benches table: a machine that runs slots.
type Bench struct {
	Name    string // one word; the local machine is the row "local"
	Host    string // an ssh alias from the caller's own ssh config, or "local"
	Root    string // the swarm root on that host, absolute there
	Cores   string // a taskset list ("1-15", "2,4,6") or "-" for no pinning
	Harness string // the harness binary on that host, absolute there
	Auth    string // the harness auth file on that host, absolute there, mode 0600
	Wall    string // "sandbox" or "none": what bench probe found
}

// Pinned reports whether the row pins cores (a list) rather than "-". A pinned row
// runs each slot on its own core and requires taskset on the bench's PATH.
func (b Bench) Pinned() bool {
	return b.Cores != "" && b.Cores != "-"
}

// CoresList parses the cores column into the sorted cores it pins, or nil for "-".
// A list that does not parse (an empty item, a non-number, a reversed range) is an
// error, which is how the table refuses it.
func CoresList(cores string) ([]int, error) {
	cores = strings.TrimSpace(cores)
	if cores == "" || cores == "-" {
		return nil, nil
	}
	seen := map[int]bool{}
	var out []int
	for _, item := range strings.Split(cores, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("cores %q has an empty item", cores)
		}
		lo, hi := item, item
		if a, b, ok := strings.Cut(item, "-"); ok {
			lo, hi = strings.TrimSpace(a), strings.TrimSpace(b)
		}
		nlo, err1 := strconv.Atoi(lo)
		nhi, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || nlo < 0 || nhi < nlo {
			return nil, fmt.Errorf("cores %q does not parse", cores)
		}
		for c := nlo; c <= nhi; c++ {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// LoadBenchTable reads --benches TSV, one header line and one row per bench, and
// validates every row: seven columns in order, an absolute root, harness and auth,
// a cores column that is "-" or parses, a wall that is "sandbox" or "none", and a
// name used once. A row that fails names itself in the error.
func LoadBenchTable(path string) ([]Bench, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var benches []Bench
	names := map[string]bool{}
	seenHeader := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 7 {
			return nil, fmt.Errorf("row %q has %d columns, wants 7 (name host root cores harness auth wall)", line, len(cols))
		}
		if !seenHeader {
			seenHeader = true
			continue
		}
		b := Bench{Name: cols[0], Host: cols[1], Root: cols[2], Cores: cols[3], Harness: cols[4], Auth: cols[5], Wall: cols[6]}
		if err := b.validate(names); err != nil {
			return nil, err
		}
		names[b.Name] = true
		benches = append(benches, b)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return benches, nil
}

// validate refuses one row: a name used twice, a host that is empty, a relative
// root, harness or auth, a cores column that does not parse, or a wall that is
// neither word.
func (b Bench) validate(names map[string]bool) error {
	switch {
	case strings.TrimSpace(b.Name) == "":
		return fmt.Errorf("row with no name")
	case names[b.Name]:
		return fmt.Errorf("name %q used twice", b.Name)
	case strings.TrimSpace(b.Host) == "":
		return fmt.Errorf("bench %q has no host", b.Name)
	case b.Root == "" || !filepath.IsAbs(b.Root):
		return fmt.Errorf("bench %q root %q is not absolute", b.Name, b.Root)
	case b.Harness == "" || !filepath.IsAbs(b.Harness):
		return fmt.Errorf("bench %q harness %q is not absolute", b.Name, b.Harness)
	case b.Auth == "" || !filepath.IsAbs(b.Auth):
		return fmt.Errorf("bench %q auth %q is not absolute", b.Name, b.Auth)
	}
	if _, err := CoresList(b.Cores); err != nil {
		return fmt.Errorf("bench %q: %v", b.Name, err)
	}
	if b.Wall != "sandbox" && b.Wall != "none" {
		return fmt.Errorf("bench %q wall %q is neither sandbox nor none", b.Name, b.Wall)
	}
	return nil
}

// FriendNames returns every name, alias and group the bus roster knows, the list a
// bench name is refused against: a friend is never a bench.
func FriendNames(busDir string) ([]string, error) {
	c, err := bus.LoadConfig(busDir)
	if err != nil {
		return nil, err
	}
	return c.KnownNames(), nil
}

// RefuseFriend returns the admission refusal when name is on the bus's friends list
// and empty otherwise. It writes nothing to the bus: it only answers.
func RefuseFriend(name string, friends []string) string {
	for _, f := range friends {
		if f == name {
			return fmt.Sprintf("ADMIT REFUSED bench=%s is a friend", oneline.Field(name))
		}
	}
	return ""
}
