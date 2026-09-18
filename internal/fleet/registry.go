// Package fleet holds the machines registry: the one file that says what each machine in
// the fleet IS, and therefore what may be placed on it.
//
// THE LOCK (Glenn, 2026-09-18): runner hosts are CI-only. No card, no probe and no load may
// be placed on a machine that serves the merge group's shards. A card and a CI shard on one
// host make the shard slow, the gate red and the queue stop -- this was measured all through
// 2026-09-17, when the merge group's darwin legs starved behind the coordination bench's own
// children and nothing landed for twenty minutes at a time.
//
// The registry exists because a bench name reached a machine as a bare string: `--bench
// batman` was a hostname the fill loop would happily ssh to, and nothing in the tools knew
// that batman is six CI runners and not a card bench. Now a bench name is RESOLVED -- every
// verb that puts work on a machine asks the registry first, and a name whose roles lack
// `bench` is refused by name, with the reason and the remedy on the line.
//
// The file is data, tab separated, kept in git beside the lanes file:
//
//	name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes<TAB>provider<TAB>mac
//
// The last two columns arrived after the first seven, so the reader takes a 7, 8 or 9
// column line: seven is the file as it was written on 2026-09-18, eight adds `provider`
// (who runs the machine), nine adds `mac` (`<hardware-address>@<lan-bench>`), which folds
// the old `wake-registry.csv` -- `name,mac,lan-bench` -- into this one file. The WRITER
// always writes nine: a column nobody filled says `-`, so a reader sees that it was
// answered and not forgotten.
//
// It is read WHOLE and validated whole: a partially-read registry is worse than none,
// because the half that read is the half that lets a card through. It is WRITTEN whole
// too, by `nova-pulse fleet registry add` and `set` and by nothing else -- this file was
// edited by hand with sed and python three times on 2026-09-18, and a control file a hand
// edits is a control file nothing validates.
package fleet

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The roles a machine may carry. They are a SET, not a rank: a machine is every one of the
// things its line says it is.
const (
	// RoleBench says cards, probes and load may be placed here. It is the only role that
	// permits work, and every other role is silent about work.
	RoleBench = "bench"
	// RoleRunner says the machine serves the merge group's CI shards.
	RoleRunner = "runner"
	// RoleCoordination says a friend's own window lives here.
	RoleCoordination = "coordination"
	// RoleServices says the stack lives here: Loki, Grafana, Redis.
	RoleServices = "services"
)

// knownRoles is the whole set. A role outside it is a typo, and a typo in this file is how
// a runner host would quietly become a bench, so it is a refusal rather than an ignored
// field.
var knownRoles = map[string]bool{
	RoleBench:        true,
	RoleRunner:       true,
	RoleCoordination: true,
	RoleServices:     true,
}

// The reasons a machine may not take work. They are tokens, not prose, so a loop reading
// the line can branch on one and a person reading it learns the same thing.
const (
	ReasonRunnerHost       = "runner-host"       // it serves the merge group's shards
	ReasonCoordinationHost = "coordination-host" // a friend's window lives there
	ReasonServicesHost     = "services-host"     // the stack lives there and nothing else may
	ReasonNotABench        = "not-a-bench"       // it carries no role that permits work
	ReasonUnknown          = "unknown-machine"   // the registry does not carry the name
)

// allowSharedPrefix is how a machine that is BOTH runner and bench says why. The exception
// is dated because it is meant to end: when the pull worker runs cards in containers, the
// runner role comes off hulk and vision and the note goes with it.
const allowSharedPrefix = "allow-shared="

// ProviderSelf is our own hardware on our own bench. Every machine in the fleet today is
// one, so it is what a line that does not say answers.
const ProviderSelf = "self"

// knownProviders is who may run a machine. It is deliberately small: a provider outside
// the set is a typo far more often than a fleet fact, and a new one is a line in this file
// written the day a machine actually arrives from there -- not a free-text column that
// quietly grows three spellings of the same company.
var knownProviders = map[string]bool{
	ProviderSelf:   true,
	"aws":          true,
	"gcp":          true,
	"azure":        true,
	"hetzner":      true,
	"oracle":       true,
	"digitalocean": true,
}

// ProviderList is the whole set, sorted, as a refusal prints it.
func ProviderList() string {
	out := make([]string, 0, len(knownProviders))
	for p := range knownProviders {
		out = append(out, p)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// hostAlias is a plain host alias: what ssh and a wake packet's lan-bench may be, and
// never anything a shell could read as syntax.
var hostAlias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Machine is one line of the registry.
type Machine struct {
	Name     string   // the bench name every verb's --bench takes
	SSH      string   // the ssh target; an alias in ~/.ssh/config or a host
	OS       string   // linux, darwin, windows
	Arch     string   // x64, amd64, arm64
	Roles    []string // sorted, unique, every one from knownRoles
	Seat     string   // the nova-secrets seat on the machine; "" when it carries none
	Cores    int      // whole cores, as the machine counts them
	Notes    string   // free text; "" when the line said `-`
	Provider string   // who runs the machine; ProviderSelf when the line does not say
	MAC      string   // the wake address, as net.ParseMAC read it; "" when it never sleeps
	LAN      string   // the machine the wake packet is broadcast from; "" with no MAC
	Line     int      // the line of the file this came from, for a refusal that can be found
}

// Sleeps says whether the machine carries a wake address, and so may be woken.
func (m Machine) Sleeps() bool { return m.MAC != "" }

// MACField is the mac column as the file writes it: `<hardware-address>@<lan-bench>`, or
// `-` when the machine never sleeps.
func (m Machine) MACField() string {
	if m.MAC == "" {
		return "-"
	}
	return m.MAC + "@" + m.LAN
}

// Row is the machine as one tab-separated line of the file. It always writes all nine
// columns: the reader takes the shorter shapes for one release, the writer never does,
// because an elided column is a column nobody can tell from a forgotten one.
func (m Machine) Row() string {
	return strings.Join([]string{
		m.Name, m.SSH, m.OS + "/" + m.Arch, m.RoleList(), dash(m.Seat),
		strconv.Itoa(m.Cores), dash(m.Notes), dash(m.Provider), m.MACField(),
	}, "\t")
}

// HasRole says whether the machine carries one role.
func (m Machine) HasRole(role string) bool {
	for _, r := range m.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// RoleList is the roles as the file writes them: comma separated, sorted.
func (m Machine) RoleList() string { return strings.Join(m.Roles, ",") }

// AllowShared reads the dated exception out of the notes:
// `allow-shared=<YYYY-MM-DD> <why>`. It returns the date, the reason and whether one is
// there at all.
func (m Machine) AllowShared() (date, why string, ok bool) {
	i := strings.Index(m.Notes, allowSharedPrefix)
	if i < 0 {
		return "", "", false
	}
	rest := strings.TrimSpace(m.Notes[i+len(allowSharedPrefix):])
	date, why, cut := strings.Cut(rest, " ")
	if !cut {
		return "", "", false
	}
	why = strings.TrimSpace(why)
	if why == "" || !isDate(date) {
		return "", "", false
	}
	return date, why, true
}

// isDate reads YYYY-MM-DD and nothing else. The exception must carry the day it was made,
// so a reader can ask how long it has stood.
func isDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Refusal is what a verb prints when a machine may not take the work it was handed. It
// carries the machine, the reason as a token, and the remedy written the way it would be
// typed.
type Refusal struct {
	Name   string
	Reason string
	Remedy string
}

func (e *Refusal) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Name, e.Reason, e.Remedy)
}

// Line is the one line a verb prints, under its own event token:
//
//	FILL REFUSED bench=batman reason=runner-host remedy="..."
func (e *Refusal) Line(token string) string {
	return fmt.Sprintf("%s REFUSED bench=%s reason=%s remedy=%s",
		token, oneline.Field(e.Name), oneline.Field(e.Reason), oneline.Quote(e.Remedy))
}

// Registry is the whole machines file, read and validated. It keeps the file's own lines
// as well as the machines, so a verb that writes one row leaves the header, the comments
// and every other row exactly as they were.
type Registry struct {
	path     string
	lines    []string // the file, split on \n, without the trailing empty element
	machines []Machine
	byName   map[string]int
}

// Path is the file this registry was read from, so a refusal can name it.
func (r *Registry) Path() string { return r.path }

// Machines is every machine, in file order.
func (r *Registry) Machines() []Machine { return r.machines }

// WithRole is every machine carrying one role, in file order. An unknown role lists
// nothing, which is the honest answer: no machine carries it.
func (r *Registry) WithRole(role string) []Machine {
	var out []Machine
	for _, m := range r.machines {
		if m.HasRole(role) {
			out = append(out, m)
		}
	}
	return out
}

// Lookup finds one machine by name.
func (r *Registry) Lookup(name string) (Machine, bool) {
	i, ok := r.byName[strings.TrimSpace(name)]
	if !ok {
		return Machine{}, false
	}
	return r.machines[i], true
}

// BenchNames is every name a verb may put work on, in file order.
func (r *Registry) BenchNames() []string {
	out := make([]string, 0, len(r.machines))
	for _, m := range r.WithRole(RoleBench) {
		out = append(out, m.Name)
	}
	return out
}

// RequireBench is THE guard. It answers nil when the named machine may take work, and a
// *Refusal naming the reason and the remedy when it may not -- an unknown name, a CI runner
// host, the coordination bench, a services host.
//
// Every verb that reaches a machine calls this before it reaches one. The check is on the
// NAME, before any ssh, so a refused machine is never even connected to.
func (r *Registry) RequireBench(name string) error {
	name = strings.TrimSpace(name)
	m, ok := r.Lookup(name)
	if !ok {
		return &Refusal{
			Name:   name,
			Reason: ReasonUnknown,
			Remedy: fmt.Sprintf("%s does not carry %s; add it, or name a bench: %s",
				r.path, dash(name), r.benchList()),
		}
	}
	if m.HasRole(RoleBench) {
		return nil
	}
	return &Refusal{
		Name:   name,
		Reason: notBenchReason(m),
		Remedy: fmt.Sprintf("%s is %s in %s and may take no card, probe or load; name a bench: %s",
			m.Name, m.RoleList(), r.path, r.benchList()),
	}
}

// notBenchReason names what the machine is instead of a bench. A runner host is named first
// whatever else it carries, because the lock is about the merge group's shards: the Studio
// coordinates AND serves the lisp leg, and it is the leg that makes a card on it a red gate.
func notBenchReason(m Machine) string {
	switch {
	case m.HasRole(RoleRunner):
		return ReasonRunnerHost
	case m.HasRole(RoleCoordination):
		return ReasonCoordinationHost
	case m.HasRole(RoleServices):
		return ReasonServicesHost
	}
	return ReasonNotABench
}

// benchList is the benches, as a remedy prints them.
func (r *Registry) benchList() string {
	names := r.BenchNames()
	if len(names) == 0 {
		return "(the registry names no bench at all)"
	}
	return strings.Join(names, ", ")
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// ReadRegistry reads and validates the machines file whole. Every refusal names the file,
// the line and what the line should have said; nothing is guessed and nothing is skipped,
// because the half of a registry that reads is the half that lets a card through.
func ReadRegistry(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the machines registry %s: %w", path, err)
	}
	return parseRegistry(path, string(raw))
}

// parseRegistry is the one reader. Every writer renders the whole file and hands it back
// here before a byte reaches the disk, so a row a verb wrote is held to exactly the rules
// a row a person wrote is held to.
func parseRegistry(path, raw string) (*Registry, error) {
	reg := &Registry{path: path, byName: map[string]int{}}
	reg.lines = strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(reg.lines) == 1 && reg.lines[0] == "" {
		reg.lines = nil
	}
	for i, line := range reg.lines {
		n := i + 1
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m, err := readMachine(strings.TrimRight(line, "\r\n"), n)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		if _, twice := reg.byName[m.Name]; twice {
			return nil, fmt.Errorf("%s line %d: %s is named twice; one line per machine (give the new machine its own name, or run: nova-pulse fleet registry set --machines %s --name %s …)",
				path, n, m.Name, path, m.Name)
		}
		reg.byName[m.Name] = len(reg.machines)
		reg.machines = append(reg.machines, m)
	}
	if len(reg.machines) == 0 {
		return nil, fmt.Errorf("%s names no machine; refusing to guess (a machines file is name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes<TAB>provider<TAB>mac; run: nova-pulse fleet registry add --machines %s --name <n> --ssh <user@host> --os <goos/goarch> --roles <a,b> --seat <s|-> --cores <n> --notes <text>)", path, path)
	}
	// The lan-bench is a machine of this same fleet, so it is checked once the whole file
	// has read and the order of the rows cannot matter. This is the check the old
	// wake-registry.csv could never make: it named `hulk` as a bare string and nothing knew
	// whether hulk existed.
	for _, m := range reg.machines {
		if m.LAN == "" {
			continue
		}
		if _, ok := reg.byName[m.LAN]; !ok {
			return nil, fmt.Errorf("%s line %d: %s is woken from the lan-bench %s, which %s does not name; give a machine in this file (the machines are %s)",
				path, m.Line, m.Name, m.LAN, path, strings.Join(reg.names(), ", "))
		}
	}
	return reg, nil
}

// names is every machine name, in file order, as a refusal lists them.
func (r *Registry) names() []string {
	out := make([]string, 0, len(r.machines))
	for _, m := range r.machines {
		out = append(out, m.Name)
	}
	return out
}

// The shape of one line. Seven columns is the file as it was first written, eight adds
// `provider` and nine adds `mac`; the reader takes all three for one release and the
// writer always writes nine. A column nobody filled says `-`, so a reader can see at a
// glance that it was answered and not forgotten.
const (
	machineFieldsMin = 7
	machineFieldsMax = 9
)

// readMachine reads and validates one line.
func readMachine(line string, n int) (Machine, error) {
	f := strings.Split(line, "\t")
	if len(f) < machineFieldsMin || len(f) > machineFieldsMax {
		return Machine{}, fmt.Errorf("wants %d to %d tab-separated fields name, ssh, os/arch, roles, seat, cores, notes, provider, mac; got %d (write every column, `-` where there is nothing; run: nova-pulse help)",
			machineFieldsMin, machineFieldsMax, len(f))
	}
	m := Machine{
		Name: strings.TrimSpace(f[0]),
		SSH:  strings.TrimSpace(f[1]),
		Seat: undash(f[4]),
		Line: n,
	}
	if m.Name == "" {
		return Machine{}, fmt.Errorf("has no machine name")
	}
	if !hostAlias.MatchString(m.Name) {
		return Machine{}, fmt.Errorf("the machine name %q is not a plain host alias; give a name of letters, digits, dot, dash and underscore", m.Name)
	}
	if m.SSH == "" {
		return Machine{}, fmt.Errorf("%s has no ssh target; give the alias ssh would take", m.Name)
	}
	goos, arch, cut := strings.Cut(strings.TrimSpace(f[2]), "/")
	if !cut || strings.TrimSpace(goos) == "" || strings.TrimSpace(arch) == "" {
		return Machine{}, fmt.Errorf("%s wants os/arch such as linux/x64 or darwin/arm64, got %q", m.Name, strings.TrimSpace(f[2]))
	}
	m.OS, m.Arch = strings.TrimSpace(goos), strings.TrimSpace(arch)

	roles, err := readRoles(f[3])
	if err != nil {
		return Machine{}, fmt.Errorf("%s %w", m.Name, err)
	}
	m.Roles = roles

	cores, err := strconv.Atoi(strings.TrimSpace(f[5]))
	if err != nil || cores < 1 {
		return Machine{}, fmt.Errorf("%s wants whole cores as a number above zero, got %q", m.Name, strings.TrimSpace(f[5]))
	}
	m.Cores = cores
	m.Notes = undash(f[6])

	m.Provider = ProviderSelf
	if len(f) > 7 {
		if p := undash(f[7]); p != "" {
			if !knownProviders[p] {
				return Machine{}, fmt.Errorf("%s carries the unknown provider %q; the providers are %s (a machine we own is %s; add a provider to internal/fleet the day a machine arrives from a new one)",
					m.Name, p, ProviderList(), ProviderSelf)
			}
			m.Provider = p
		}
	}

	if len(f) > 8 {
		mac, lan, err := readMAC(undash(f[8]))
		if err != nil {
			return Machine{}, fmt.Errorf("%s %w", m.Name, err)
		}
		m.MAC, m.LAN = mac, lan
	}

	// The one rule the file itself enforces: a machine that is both a CI runner host and a
	// card bench is the exception the lock permits for now, and it must say why and when.
	if m.HasRole(RoleBench) && m.HasRole(RoleRunner) {
		if _, _, ok := m.AllowShared(); !ok {
			return Machine{}, fmt.Errorf(
				"%s is both %s and %s; the lock (Glenn 2026-09-18) is that runner hosts are CI-only, so a shared machine must carry the dated exception in its notes: `%s<YYYY-MM-DD> <why it is shared and what ends it>`",
				m.Name, RoleRunner, RoleBench, allowSharedPrefix)
		}
	}
	return m, nil
}

// readRoles reads the roles set: comma separated, at least one, each known, none twice.
func readRoles(field string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(field, ",") {
		role := strings.TrimSpace(part)
		if role == "" {
			continue
		}
		if !knownRoles[role] {
			return nil, fmt.Errorf("carries the unknown role %q; the roles are %s, %s, %s, %s",
				role, RoleBench, RoleRunner, RoleCoordination, RoleServices)
		}
		if seen[role] {
			return nil, fmt.Errorf("names the role %q twice; roles are a set", role)
		}
		seen[role] = true
		out = append(out, role)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("carries no role; every machine is at least one of %s, %s, %s, %s",
			RoleBench, RoleRunner, RoleCoordination, RoleServices)
	}
	sort.Strings(out)
	return out, nil
}

// readMAC reads the wake column: `<hardware-address>@<lan-bench>`, or nothing at all when
// the machine never sleeps. Both halves are required together, because a mac with no
// lan-bench is a machine nothing can wake -- the packet is a LAN broadcast and it has to
// leave from somewhere. The address is normalised to what net.ParseMAC read, so one file
// cannot carry two spellings of one machine's hardware.
func readMAC(field string) (mac, lan string, err error) {
	if field == "" {
		return "", "", nil
	}
	addr, bench, cut := strings.Cut(field, "@")
	addr, bench = strings.TrimSpace(addr), strings.TrimSpace(bench)
	if !cut || addr == "" || bench == "" {
		return "", "", fmt.Errorf("wants the wake column as <hardware-address>@<lan-bench>, got %q; give the machine the packet is broadcast from, or `-` when it never sleeps", field)
	}
	hw, perr := net.ParseMAC(addr)
	if perr != nil {
		return "", "", fmt.Errorf("has the wake address %q, which is not a hardware address: %v; give six bytes such as d0:81:7a:d8:3a:ec", addr, perr)
	}
	if len(hw) != 6 {
		return "", "", fmt.Errorf("has the wake address %q, which is %d bytes; give the six-byte address", addr, len(hw))
	}
	if !hostAlias.MatchString(bench) {
		return "", "", fmt.Errorf("is woken from %q, which is not a plain host alias; name the machine the packet is broadcast from", bench)
	}
	return hw.String(), bench, nil
}

// undash reads a column whose `-` means "none".
func undash(s string) string {
	t := strings.TrimSpace(s)
	if t == "-" {
		return ""
	}
	return t
}
