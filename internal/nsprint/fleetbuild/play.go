package fleetbuild

// Play (#4356 item C) is `nova-sprint fleet play <tag> [--limit a,b]
// [--dry-run]`: one rowan-tools fleet play (<tag>.yml in the play
// directory, tools for tools.yml) run through the verb with fleet release's
// runner and argv (PlayArgv, PlayEnv: the machines registry as inventory),
// never by hand. In order:
//
//	CLONE    the rowan-tools clone holding the play directory must be clean
//	         (git status --porcelain empty, untracked files included) and not
//	         behind its upstream (git fetch, then git rev-list --count
//	         HEAD..@{u} is 0); either refuses with the git command that fixes
//	         it, before the play runs. Its HEAD is the receipt's sha.
//	PLAY     ansible-playbook -i inventory.py <tag>.yml --forks 16 --diff
//	         [--limit a,b] [--check], with the profile_roles callback on so
//	         the output carries each role's time (ROLES RECAP)
//	RECEIPTS ParsePlay reads ansible's own output: one FLEET PLAY <bench>
//	         <role> ok|changed|failed ms=<n> line per bench and per role, in
//	         the recap's bench order and the roles' run order
//	STORE    per bench, one HSET of bench:<b>:play {at, tag, sha, role,
//	         result}: role the last role the bench ran, result ok or
//	         failed:<role> (the role it stopped in). The table reads it: a
//	         bench whose result is failed:<role> shows `behind: <role>` on its
//	         row, and fleet doctor reads the same hash. --dry-run is
//	         ansible's --check: it prints the receipts and writes nothing.
//
// The last line is FLEET PLAY OK|FAIL tag=<t> sha=<sha12> benches=<n>
// failed=<bench:role,...>|-.

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// ProfileCallback is the ansible.posix callback that prints each role's
	// time in a ROLES RECAP after the PLAY RECAP.
	ProfileCallback = "ansible.posix.profile_roles"
	// FactsRole names the Gathering Facts task (where an unreachable bench
	// stops); TasksRole names a play's own tasks, outside any role.
	FactsRole = "facts"
	TasksRole = "tasks"
)

// PlayKey is the bench's play receipt hash.
func PlayKey(bench string) string { return "bench:" + bench + ":play" }

// PlayFailedPrefix starts a receipt result naming the role a bench stopped in.
const PlayFailedPrefix = "failed:"

// BehindRole is the role a play receipt's result says the bench stopped in,
// "" when the result is ok (or not a failure).
func BehindRole(result string) string {
	if strings.HasPrefix(result, PlayFailedPrefix) {
		return strings.TrimPrefix(result, PlayFailedPrefix)
	}
	return ""
}

var (
	playTagRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	taskHeaderRe  = regexp.MustCompile(`^(?:TASK|RUNNING HANDLER) \[(.*)\] \*+\s*$`)
	hostResultRe  = regexp.MustCompile(`^(ok|changed|skipping|fatal|failed): \[([^\]]+)\]`)
	recapRowRe    = regexp.MustCompile(`^(\S+)\s+:\s+ok=\d+\s+changed=\d+\s+unreachable=(\d+)\s+failed=(\d+)`)
	roleTimingRe  = regexp.MustCompile(`^(\S+) -+ (\d+(?:\.\d+)?)s\s*$`)
	playSectionRe = regexp.MustCompile(`^(PLAY RECAP|ROLES RECAP) \*`)
)

// RoleRun is one bench's run of one role.
type RoleRun struct {
	Host, Role string
	State      string // ok, changed or failed
	MS         int64  // the role's time from the ROLES RECAP; -1 when the output has none
}

// Line is the per-bench, per-role receipt.
func (r RoleRun) Line() string {
	ms := "-"
	if r.MS >= 0 {
		ms = strconv.FormatInt(r.MS, 10)
	}
	return fmt.Sprintf("FLEET PLAY %s %s %s ms=%s", r.Host, r.Role, r.State, ms)
}

// HostPlay is one bench's whole play.
type HostPlay struct {
	Host   string
	Roles  []RoleRun
	Failed string // the role the bench stopped in; "" when it did not
}

// Last is the last role the bench ran ("" when it ran none).
func (h HostPlay) Last() string {
	if len(h.Roles) == 0 {
		return ""
	}
	return h.Roles[len(h.Roles)-1].Role
}

// Result is the receipt's result field: ok or failed:<role>.
func (h HostPlay) Result() string {
	if h.Failed != "" {
		return PlayFailedPrefix + h.Failed
	}
	return "ok"
}

// roleOf is the role a task header names: the part before " : " (ansible
// prints role tasks as `<role> : <task>`), FactsRole for Gathering Facts,
// TasksRole for a play's own task.
func roleOf(task string) string {
	if i := strings.Index(task, " : "); i > 0 {
		return task[:i]
	}
	if task == "Gathering Facts" {
		return FactsRole
	}
	return TasksRole
}

// ParsePlay reads ansible-playbook's default output (the profile_roles
// callback on) into one HostPlay per bench, in the PLAY RECAP's order. A
// task result line (ok, changed, skipping, fatal, failed: [<bench>]) marks
// the role of the task header above it; a fatal followed by `...ignoring` is
// no failure. The PLAY RECAP decides whether a bench failed: failed or
// unreachable above 0 is a stop (at the first failed role, else the last role
// it ran), and a clean row turns a rescued failure back into ok. The ROLES
// RECAP gives each role's time; a play's own tasks are summed under
// TasksRole. Output with no PLAY RECAP has no benches.
func ParsePlay(out string) []HostPlay {
	type key struct{ host, role string }
	type cell struct{ changed, failed bool }
	var (
		section  string
		role     = TasksRole
		cells    = map[key]*cell{}
		order    = map[string][]string{} // host -> roles in run order
		seen     = map[string]bool{}     // roles named by a task header
		pending  *cell                   // the last failure, until the next result
		recap    []string
		stopped  = map[string]bool{}
		timings  = map[string]float64{}
		hasTimes bool
	)
	for _, raw := range strings.Split(out, "\n") {
		l := strings.TrimRight(raw, " \r")
		if m := playSectionRe.FindStringSubmatch(l); m != nil {
			section = m[1]
			continue
		}
		switch section {
		case "PLAY RECAP":
			if m := recapRowRe.FindStringSubmatch(l); m != nil {
				recap = append(recap, m[1])
				stopped[m[1]] = m[2] != "0" || m[3] != "0"
			}
			continue
		case "ROLES RECAP":
			if m := roleTimingRe.FindStringSubmatch(l); m != nil && m[1] != "total" {
				s, _ := strconv.ParseFloat(m[2], 64)
				timings[m[1]] += s
				hasTimes = true
			}
			continue
		}
		if m := taskHeaderRe.FindStringSubmatch(l); m != nil {
			role, pending = roleOf(m[1]), nil
			seen[role] = true
			continue
		}
		if l == "...ignoring" {
			if pending != nil {
				pending.failed = false
			}
			pending = nil
			continue
		}
		m := hostResultRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		host, _, _ := strings.Cut(m[2], " -> ")
		k := key{host, role}
		c := cells[k]
		if c == nil {
			c = &cell{}
			cells[k] = c
			order[host] = append(order[host], role)
		}
		pending = nil
		switch m[1] {
		case "changed":
			c.changed = true
		case "fatal", "failed":
			c.failed = true
			pending = c
		}
	}

	ms := map[string]int64{}
	for name, s := range timings {
		r := name
		switch {
		case seen[name] && name != FactsRole && name != TasksRole:
		case name == "gather_facts" || strings.HasSuffix(name, ".gather_facts"):
			r = FactsRole
		default:
			r = TasksRole
		}
		ms[r] += int64(math.Round(s * 1000))
	}

	hosts := make([]HostPlay, 0, len(recap))
	for _, host := range recap {
		h := HostPlay{Host: host}
		for _, r := range order[host] {
			c := cells[key{host, r}]
			state := "ok"
			switch {
			case c.failed && stopped[host]:
				state = "failed"
				if h.Failed == "" {
					h.Failed = r
				}
			case c.changed:
				state = "changed"
			}
			t := int64(-1)
			if v, ok := ms[r]; ok && hasTimes {
				t = v
			}
			h.Roles = append(h.Roles, RoleRun{Host: host, Role: r, State: state, MS: t})
		}
		if stopped[host] && h.Failed == "" {
			h.Failed = h.Last()
			if h.Failed == "" {
				h.Failed = FactsRole
			}
		}
		hosts = append(hosts, h)
	}
	return hosts
}

// Play is one fleet play run through the verb.
type Play struct {
	Runner   ExecRunner
	Client   *redis.Client // the receipts; nil only with DryRun
	PlayDir  string
	Tag      string // the play's name: tools runs tools.yml
	Registry string // the machines registry path the play's inventory reads
	Machines []Machine
	Limit    []string // --limit: registry machines only
	DryRun   bool     // ansible --check; no receipt is written
	Now      func() time.Time
	Out      io.Writer
}

// PlayResult is what one play did.
type PlayResult struct {
	Tag, Sha string
	Hosts    []HostPlay
	Err      string // the play failed with no bench to blame (no PLAY RECAP)
	DryRun   bool
}

// Failed is every bench that stopped, as <bench>:<role>.
func (r PlayResult) Failed() []string {
	var out []string
	for _, h := range r.Hosts {
		if h.Failed != "" {
			out = append(out, h.Host+":"+h.Failed)
		}
	}
	return out
}

// OK is true when the play ran on at least one bench and none stopped.
func (r PlayResult) OK() bool { return r.Err == "" && len(r.Hosts) > 0 && len(r.Failed()) == 0 }

// Line is the play's last receipt.
func (r PlayResult) Line() string {
	word := "OK"
	if !r.OK() {
		word = "FAIL"
	}
	failed := "-"
	if f := r.Failed(); len(f) > 0 {
		failed = strings.Join(f, ",")
	}
	sha := r.Sha
	if len(sha) > 12 {
		sha = sha[:12]
	}
	line := fmt.Sprintf("FLEET PLAY %s tag=%s sha=%s benches=%d failed=%s", word, r.Tag, sha, len(r.Hosts), failed)
	if r.DryRun {
		line += " check=yes"
	}
	return line
}

func (p *Play) printf(format string, a ...any) {
	if p.Out != nil {
		fmt.Fprintf(p.Out, format, a...)
	}
}

// PlayFile is the play a tag names.
func PlayFile(tag string) string { return tag + ".yml" }

// check refuses what would stop the play before it runs.
func (p *Play) check() error {
	if p.Runner == nil {
		return refused("fleet play has no runner")
	}
	if !playTagRe.MatchString(p.Tag) {
		return refused("%q is not a play name: fleet play <tag> names <tag>.yml in the play directory, like tools", p.Tag)
	}
	if p.Registry == "" {
		return refused("the play's inventory reads the machines registry: --machines <file>, or %s", MachinesEnv)
	}
	if p.PlayDir == "" {
		return refused("no fleet play directory: --play-dir <dir>, or %s", PlayDirEnv)
	}
	for _, f := range []string{PlayInventory, PlayFile(p.Tag)} {
		if _, err := os.Stat(filepath.Join(p.PlayDir, f)); err != nil {
			return refused("the fleet play directory %s has no %s (--play-dir <dir>, or %s)", p.PlayDir, f, PlayDirEnv)
		}
	}
	known := map[string]bool{}
	for _, m := range p.Machines {
		known[m.Name] = true
	}
	for _, b := range p.Limit {
		if !known[b] {
			return refused("--bench %s is not a machine in the registry %s (--bench names registry machines, comma separated)", b, p.Registry)
		}
	}
	if p.Client == nil && !p.DryRun {
		return refused("fleet play writes each bench's receipt to the fleet store: --redis <addr>, or --dry-run")
	}
	return nil
}

// cloneSha refuses a rowan-tools clone that is dirty or behind its
// upstream, each with the git command that fixes it, and returns its HEAD.
func (p *Play) cloneSha(ctx context.Context) (string, error) {
	out, err := p.Runner.Run(ctx, p.PlayDir, nil, []string{"git", "rev-parse", "--show-toplevel", "HEAD"})
	f := strings.Fields(out)
	if err != nil || len(f) != 2 || !commitRe.MatchString(f[1]) {
		return "", refused("the play directory %s is not in a git clone of rowan-tools (git -C %s rev-parse HEAD: %s)", p.PlayDir, p.PlayDir, lastLine(out))
	}
	top, sha := f[0], f[1]
	out, err = p.Runner.Run(ctx, top, nil, []string{"git", "status", "--porcelain"})
	if err != nil {
		return "", refused("git -C %s status --porcelain failed: %s", top, lastLine(out))
	}
	if dirty := strings.Split(strings.TrimRight(out, "\n"), "\n"); strings.TrimSpace(out) != "" {
		return "", refused("the rowan-tools clone %s is dirty (%d paths, first %s): commit and push it, or git -C %s stash -u",
			top, len(dirty), strings.TrimSpace(dirty[0]), top)
	}
	if out, err = p.Runner.Run(ctx, top, nil, []string{"git", "fetch", "-q"}); err != nil {
		return "", refused("git -C %s fetch -q failed: %s (the behind check needs the upstream)", top, lastLine(out))
	}
	out, err = p.Runner.Run(ctx, top, nil, []string{"git", "rev-list", "--count", "HEAD..@{u}"})
	if err != nil {
		return "", refused("the rowan-tools clone %s has no upstream branch: git -C %s switch main", top, top)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return "", refused("git -C %s rev-list --count HEAD..@{u} answered %q", top, lastLine(out))
	}
	if n > 0 {
		return "", refused("the rowan-tools clone %s is %d commits behind its upstream: git -C %s pull --ff-only", top, n, top)
	}
	return sha, nil
}

// Run checks, refuses a dirty or behind clone, runs the play, prints one
// receipt per bench and role, and writes each bench's receipt. An error is
// a refusal (ErrRefused) or the store failing; a stopped bench is in the
// result.
func (p *Play) Run(ctx context.Context) (PlayResult, error) {
	res := PlayResult{Tag: p.Tag, DryRun: p.DryRun}
	if err := p.check(); err != nil {
		return res, err
	}
	sha, err := p.cloneSha(ctx)
	if err != nil {
		return res, err
	}
	res.Sha = sha

	argv := PlayArgv(PlayFile(p.Tag), "", p.Limit)
	if p.DryRun {
		argv = append(argv, "--check")
	}
	env := append(PlayEnv(p.Registry), "ANSIBLE_CALLBACKS_ENABLED="+ProfileCallback)
	pctx, cancel := context.WithTimeout(ctx, PlayTimeout)
	out, runErr := p.Runner.Run(pctx, p.PlayDir, env, argv)
	cancel()
	res.Hosts = ParsePlay(out)
	for _, h := range res.Hosts {
		for _, r := range h.Roles {
			p.printf("%s\n", r.Line())
		}
	}
	if len(res.Hosts) == 0 {
		why := "no PLAY RECAP"
		if runErr != nil {
			why = strings.Join(strings.Fields(runErr.Error()), "_")
		}
		res.Err = why
		p.printf("PLAY ERROR %s err=%s last=%s\n", PlayFile(p.Tag), why, lastLine(out))
	}
	if p.DryRun || len(res.Hosts) == 0 {
		return res, nil
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	at := strconv.FormatInt(now().UnixMilli(), 10)
	pipe := p.Client.Pipeline()
	for _, h := range res.Hosts {
		pipe.HSet(ctx, PlayKey(h.Host), "at", at, "tag", p.Tag, "sha", sha, "role", h.Last(), "result", h.Result())
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return res, fmt.Errorf("write the play receipts: %w", err)
	}
	return res, nil
}
