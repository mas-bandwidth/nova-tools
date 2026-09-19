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
//	name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes
//
// It is read WHOLE and validated whole: a partially-read registry is worse than none,
// because the half that read is the half that lets a card through.
package fleet

import (
	"fmt"
	"os"
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

// certifiedPrefix is how a machine's notes say it has been through certification: the
// machine was provisioned, probed and proven to carry a real card end to end, and the day
// it was is on the line. It is the field the fill pool is derived from -- an uncertified
// bench may be named by hand, but no tick puts a card on it by default.
const certifiedPrefix = "certified="

// Machine is one line of the registry.
type Machine struct {
	Name  string   // the bench name every verb's --bench takes
	SSH   string   // the ssh target; an alias in ~/.ssh/config or a host
	OS    string   // linux, darwin, windows
	Arch  string   // x64, amd64, arm64
	Roles []string // sorted, unique, every one from knownRoles
	Seat  string   // the nova-secrets seat on the machine; "" when it carries none
	Cores int      // whole cores, as the machine counts them
	Notes string   // free text; "" when the line said `-`
	Line  int      // the line of the file this came from, for a refusal that can be found
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

// Certified reads the certification out of the notes: `certified=<YYYY-MM-DD>`, with
// anything after the date free text (the report it was written from, usually). It answers
// the date and whether the machine carries one at all.
func (m Machine) Certified() (date string, ok bool) {
	i := strings.Index(m.Notes, certifiedPrefix)
	if i < 0 {
		return "", false
	}
	rest := strings.TrimSpace(m.Notes[i+len(certifiedPrefix):])
	date = rest
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		date = rest[:j]
	}
	if !isDate(date) {
		return "", false
	}
	return date, true
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

// Registry is the whole machines file, read and validated.
type Registry struct {
	path     string
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

// CertifiedBenchNames is THE POOL: every machine that may take work AND has been certified,
// in file order. It is what a verb fills when the caller names no bench, so a bench joins
// the fleet's work by its registry row and by nothing else -- no list in any Go file, no
// edit to any tool, no release.
func (r *Registry) CertifiedBenchNames() []string {
	out := make([]string, 0, len(r.machines))
	for _, m := range r.WithRole(RoleBench) {
		if _, ok := m.Certified(); ok {
			out = append(out, m.Name)
		}
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
	reg := &Registry{path: path, byName: map[string]int{}}
	for i, line := range strings.Split(string(raw), "\n") {
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
			return nil, fmt.Errorf("%s line %d: %s is named twice; one line per machine",
				path, n, m.Name)
		}
		reg.byName[m.Name] = len(reg.machines)
		reg.machines = append(reg.machines, m)
	}
	if len(reg.machines) == 0 {
		return nil, fmt.Errorf("%s names no machine; refusing to guess (a machines file is name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes)", path)
	}
	return reg, nil
}

// machineFields is the shape of one line. Seven, always: a column nobody filled says `-`,
// so a reader can see at a glance that it was answered and not forgotten.
const machineFields = 7

// readMachine reads and validates one line.
func readMachine(line string, n int) (Machine, error) {
	f := strings.Split(line, "\t")
	if len(f) != machineFields {
		return Machine{}, fmt.Errorf("wants %d tab-separated fields name, ssh, os/arch, roles, seat, cores, notes; got %d",
			machineFields, len(f))
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

// undash reads a column whose `-` means "none".
func undash(s string) string {
	t := strings.TrimSpace(s)
	if t == "-" {
		return ""
	}
	return t
}
