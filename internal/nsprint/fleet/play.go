package fleet

import (
	"context"
	"errors"
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
	// PlayInventory is the default inventory script.
	PlayInventory = "inventory.py"
	// PlayForks is the default ansible forks count.
	PlayForks = 16
	// ProfileCallback is the ansible.posix callback that prints each role's time.
	ProfileCallback = "ansible.posix.profile_roles"
	// FactsRole names the Gathering Facts task.
	FactsRole = "facts"
	// TasksRole names play tasks outside any role.
	TasksRole = "tasks"
	// PlayFailedPrefix starts a receipt result naming the role a bench stopped in.
	PlayFailedPrefix = "failed:"
	// PlayTimeout is the maximum duration for a play run.
	PlayTimeout = 15 * time.Minute
	// PlayDirEnv names the environment variable holding the play directory.
	PlayDirEnv = "NOVA_FLEET_PLAY_DIR"
	// MachinesEnv names the environment variable holding the machines registry path.
	MachinesEnv = "NOVA_FLEET_MACHINES"
	// PlayRegistry is the environment variable passed to ansible inventory.
	PlayRegistry = "FLEET_REGISTRY"
	// DefaultPlayDirRel is the relative path from home to the default play directory.
	DefaultPlayDirRel = "fleet"
)

// ErrRefused marks a fleet play refusal before execution.
var ErrRefused = errors.New("fleet play refused")

func refused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// ExecRunner runs one child in dir with extra environment and returns its output.
type ExecRunner interface {
	Run(ctx context.Context, dir string, env []string, argv []string) (string, error)
}

// PlayKey returns the bench's play receipt hash key in Redis.
func PlayKey(bench string) string { return "bench:" + bench + ":play" }

// BehindRole returns the role a play receipt's result says the bench stopped in,
// or "" if the result was ok or not a failure.
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
	commitRe      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// RoleRun is one bench's run of one role.
type RoleRun struct {
	Host  string
	Role  string
	State string // ok, changed, or failed
	MS    int64  // role timing from ROLES RECAP; -1 when missing
}

// Line returns the standard receipt line for one role run.
func (r RoleRun) Line() string {
	ms := "-"
	if r.MS >= 0 {
		ms = strconv.FormatInt(r.MS, 10)
	}
	return fmt.Sprintf("FLEET PLAY %s %s %s ms=%s", r.Host, r.Role, r.State, ms)
}

// StructuredLine returns a key=value receipt line.
func (r RoleRun) StructuredLine(tag string) string {
	status := strings.ToUpper(r.State)
	if status != "FAILED" {
		status = "OK"
	}
	ms := "-"
	if r.MS >= 0 {
		ms = strconv.FormatInt(r.MS, 10)
	}
	return fmt.Sprintf("FLEET PLAY bench=%s tag=%s role=%s status=%s ms=%s", r.Host, tag, r.Role, status, ms)
}

// HostPlay represents one bench's whole play run.
type HostPlay struct {
	Host   string
	Roles  []RoleRun
	Failed string // the role the bench stopped in; "" when it succeeded
}

// Last returns the last role the bench ran ("" when none ran).
func (h HostPlay) Last() string {
	if len(h.Roles) == 0 {
		return ""
	}
	return h.Roles[len(h.Roles)-1].Role
}

// Result returns the receipt's result field: ok or failed:<role>.
func (h HostPlay) Result() string {
	if h.Failed != "" {
		return PlayFailedPrefix + h.Failed
	}
	return "ok"
}

func roleOf(task string) string {
	if i := strings.Index(task, " : "); i > 0 {
		return task[:i]
	}
	if task == "Gathering Facts" {
		return FactsRole
	}
	return TasksRole
}

// ParsePlay reads ansible-playbook output with the profile_roles callback into
// one HostPlay per bench in the PLAY RECAP's order.
func ParsePlay(out string) []HostPlay {
	type key struct{ host, role string }
	type cell struct{ changed, failed bool }
	var (
		section  string
		role     = TasksRole
		cells    = map[key]*cell{}
		order    = map[string][]string{}
		seen     = map[string]bool{}
		pending  *cell
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

// PlayResult represents the outcome of a fleet play execution.
type PlayResult struct {
	Tag    string
	Sha    string
	Hosts  []HostPlay
	Err    string
	DryRun bool
}

// Failed returns all benches that stopped in format <bench>:<role>.
func (r PlayResult) Failed() []string {
	var out []string
	for _, h := range r.Hosts {
		if h.Failed != "" {
			out = append(out, h.Host+":"+h.Failed)
		}
	}
	return out
}

// OK reports whether the play ran on at least one bench and none stopped.
func (r PlayResult) OK() bool {
	return r.Err == "" && len(r.Hosts) > 0 && len(r.Failed()) == 0
}

// Line returns the summary line for the play result.
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

// PlayFile returns the playbook file name for a tag.
func PlayFile(tag string) string { return tag + ".yml" }

// PlayArgv constructs the argv for running ansible-playbook.
func PlayArgv(play string, limit []string) []string {
	argv := []string{"ansible-playbook", "-i", PlayInventory, play, "--forks", fmt.Sprint(PlayForks), "--diff"}
	if len(limit) > 0 {
		argv = append(argv, "--limit", strings.Join(limit, ","))
	}
	return argv
}

// PlayEnv returns the required environment variables for ansible execution.
func PlayEnv(registry string) []string {
	return []string{
		"ANSIBLE_NOCOWS=1",
		"ANSIBLE_HOST_KEY_CHECKING=True",
		PlayRegistry + "=" + registry,
	}
}

// Play coordinates execution of one fleet play.
type Play struct {
	Runner   ExecRunner
	Client   *redis.Client
	PlayDir  string
	Tag      string
	Registry string
	Benches  []string // known registry benches
	Limit    []string // --limit filter
	DryRun   bool
	Now      func() time.Time
	Out      io.Writer
}

func (p *Play) printf(format string, a ...any) {
	if p.Out != nil {
		fmt.Fprintf(p.Out, format, a...)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

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
	if len(p.Benches) > 0 {
		known := map[string]bool{}
		for _, b := range p.Benches {
			known[b] = true
		}
		for _, b := range p.Limit {
			if !known[b] {
				return refused("--limit %s is not a machine in the registry %s (--limit names registry machines, comma separated)", b, p.Registry)
			}
		}
	}
	if p.Client == nil && !p.DryRun {
		return refused("fleet play writes each bench's receipt to the fleet store: --redis <addr>, or --dry-run")
	}
	return nil
}

func (p *Play) cloneSha(ctx context.Context) (string, error) {
	out, err := p.Runner.Run(ctx, p.PlayDir, nil, []string{"git", "rev-parse", "--show-toplevel", "HEAD"})
	f := strings.Fields(out)
	if err != nil || len(f) != 2 || !commitRe.MatchString(f[1]) {
		return "", refused("the play directory %s is not in a git clone (git -C %s rev-parse HEAD: %s)", p.PlayDir, p.PlayDir, lastLine(out))
	}
	top, sha := f[0], f[1]
	out, err = p.Runner.Run(ctx, top, nil, []string{"git", "status", "--porcelain"})
	if err != nil {
		return "", refused("git -C %s status --porcelain failed: %s", top, lastLine(out))
	}
	if dirty := strings.Split(strings.TrimRight(out, "\n"), "\n"); strings.TrimSpace(out) != "" {
		return "", refused("the clone %s is dirty (%d paths, first %s): commit and push it, or git -C %s stash -u",
			top, len(dirty), strings.TrimSpace(dirty[0]), top)
	}
	if out, err = p.Runner.Run(ctx, top, nil, []string{"git", "fetch", "-q"}); err != nil {
		return "", refused("git -C %s fetch -q failed: %s (the behind check needs the upstream)", top, lastLine(out))
	}
	out, err = p.Runner.Run(ctx, top, nil, []string{"git", "rev-list", "--count", "HEAD..@{u}"})
	if err != nil {
		return "", refused("the clone %s has no upstream branch: git -C %s switch main", top, top)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return "", refused("git -C %s rev-list --count HEAD..@{u} answered %q", top, lastLine(out))
	}
	if n > 0 {
		return "", refused("the clone %s is %d commits behind its upstream: git -C %s pull --ff-only", top, n, top)
	}
	return sha, nil
}

// Run checks pre-conditions, refuses a dirty or behind clone, runs the play,
// prints receipts, and writes them to Redis.
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

	argv := PlayArgv(PlayFile(p.Tag), p.Limit)
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
		p.printf("PLAY ABORTED %s err=%s last=%s\n", PlayFile(p.Tag), why, lastLine(out))
	}
	if p.DryRun || len(res.Hosts) == 0 || p.Client == nil {
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
		if h.Failed != "" {
			pipe.HSet(ctx, "bench:"+h.Host, "behind", h.Failed)
			pipe.HSet(ctx, "bench:"+h.Host+":state", "behind", h.Failed)
		} else {
			pipe.HDel(ctx, "bench:"+h.Host, "behind")
			pipe.HDel(ctx, "bench:"+h.Host+":state", "behind")
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return res, fmt.Errorf("write the play receipts: %w", err)
	}
	return res, nil
}
