// Benches: a remote bench reached by ssh, named by one row of a TSV table
// (docs/SPEC-SWARM.md "Benches"). This file holds the shared types and the reads every
// bench caller shares -- the table, and the bus's friends list a bench name is checked
// against -- plus the remote-run slice the batch caller uses. Nothing here reads a bench
// unless a caller asks for it; the batch and probe callers are in cmd/nova-swarm.
package swarm

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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

// ReadBenchTable reads a benches file: one header line then one tab-separated row per
// bench, the seven columns name, host, root, cores, harness, auth, wall. A row that does
// not parse is refused with its line number, and a name used twice is refused too.
func ReadBenchTable(path string) (map[string]Bench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--benches wants a readable table of one bench per row: %w", err)
	}
	out := map[string]Bench{}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 7 {
			return nil, fmt.Errorf("--benches line %d wants name<TAB>host<TAB>root<TAB>cores<TAB>harness<TAB>auth<TAB>wall, got %d fields", i+1, len(parts))
		}
		if parts[0] == "name" {
			continue // the header row
		}
		name := strings.TrimSpace(parts[0])
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--benches names %s twice", name)
		}
		out[name] = Bench{
			Name:    name,
			Host:    parts[1],
			Root:    parts[2],
			Cores:   parts[3],
			Harness: parts[4],
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--benches names no bench row")
	}
	return out, nil
}

// coreCount reports how many cores a bench's cores column names: the length of a list, the
// breadth of a range, or -1 for "-" (no pinning, an unbounded slot count).
func coreCount(cores string) int {
	if cores == "-" {
		return -1
	}
	if strings.Contains(cores, "-") {
		parts := strings.SplitN(cores, "-", 2)
		lo, errLo := strconv.Atoi(parts[0])
		hi, errHi := strconv.Atoi(parts[1])
		if errLo != nil || errHi != nil || hi < lo {
			return 0
		}
		return hi - lo + 1
	}
	return len(strings.Split(cores, ","))
}

// coreFor resolves the core slot n (1-based) pins to, so slot 3 on "1-15" is core 3 and on
// "2,4,6" is core 6. It returns the core string or an error when n exceeds the cores.
func coreFor(cores string, n int) (string, error) {
	if strings.Contains(cores, "-") {
		parts := strings.SplitN(cores, "-", 2)
		lo, err := strconv.Atoi(parts[0])
		if err != nil {
			return "", fmt.Errorf("cores %q does not name a range", cores)
		}
		return strconv.Itoa(lo + n - 1), nil
	}
	parts := strings.Split(cores, ",")
	if n < 1 || n > len(parts) {
		return "", fmt.Errorf("slot %d exceeds cores %q", n, cores)
	}
	return parts[n-1], nil
}

// scratchName is the on-disk directory a card's files return under: <bench>-<n> for a remote
// bench and plain <n> for the local machine.
func scratchName(c batchCard) string {
	if c.bench != "" {
		return c.bench + "-" + strconv.Itoa(c.slot)
	}
	return strconv.Itoa(c.slot)
}

// remoteRun copies the card to the bench -- the card only, nothing else -- then builds the
// ssh command that runs native there: ssh <host> [taskset -c <core>] <root>/bin/nova-swarm
// native ..., with the ssh child in a process group of its own.
func remoteRun(c batchCard, b Bench, localRoot string, deadline int, logFile *os.File) (*exec.Cmd, error) {
	cardDest := filepath.Join(b.Root, "cards", c.label+".md")
	if err := copyCardToBench(c.cardPath, b, cardDest); err != nil {
		return nil, fmt.Errorf("nova-swarm batch: card %s could not be copied to bench %s: %s",
			oneline.Field(c.label), oneline.Field(b.Name), oneline.Err(err))
	}
	argv := []string{"ssh", b.Host}
	if b.Cores != "-" {
		core, err := coreFor(b.Cores, c.slot)
		if err != nil {
			return nil, fmt.Errorf("ADMIT REFUSED bench=%s slots=%d cores=%s", b.Name, c.slot, b.Cores)
		}
		argv = append(argv, "taskset", "-c", core)
	}
	argv = append(argv,
		filepath.Join(b.Root, "bin", "nova-swarm"), "native",
		"--harness", b.Harness,
		"--model", c.model,
		"--label", c.label,
		"--card", cardDest,
		"--slot", filepath.Join(b.Root, strconv.Itoa(c.slot), "jobs", c.label),
		"--root", b.Root,
		"--deadline", strconv.Itoa(deadline),
	)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+localRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	ownGroup(cmd)
	return cmd, nil
}

// copyCardToBench runs rsync to move the card to the bench's cards directory, the one file
// that crosses before the run.
func copyCardToBench(local string, b Bench, dest string) error {
	cmd := exec.Command("rsync", local, b.Host+":"+dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
