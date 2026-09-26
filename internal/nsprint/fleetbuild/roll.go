package fleetbuild

// Roll (#4332) is `nova-sprint fleet roll [--to <sha>]`: the rest of
// fleet-roll.sh as one verb. Three steps, one receipt line each:
//
//	RELEASE  Release.Run of the sha (--to, else dev's tip by git ls-remote):
//	         the Studio, fn deploy and the bench roll, its own receipts and
//	         its FLEET RELEASE line
//	PLAY     the fleet play through ansible, never ssh by hand
//	         (fleet-changes-only-through-ansible): ansible-playbook -i
//	         inventory.py <play> --forks 16 --diff -e nova_build=<v> in the
//	         play directory, FLEET_REGISTRY naming the machines registry the
//	         inventory reads; the recap per host, then PLAY OK|FAIL
//	VERIFY   each bench's beat version field (bench:<b>:beat build, one
//	         pipelined read, no ssh) against the released version, re-read
//	         until every bench is on it or the wait is spent; one VERIFY
//	         line per bench (bench, want, have, ok|behind)
//
// The last line is FLEET ROLL OK, or FLEET ROLL BEHIND|FAIL naming the benches
// behind. A failed play or a refused FN does not stop the verify: the beats
// are the evidence, and the verify reads them either way.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

const (
	// DefaultPlay is the play that installs the declared nova build on
	// every bench (the tools play of the rowan-tools fleet directory, its
	// tools role).
	DefaultPlay = "tools.yml"
	// PlayDirEnv names the fleet play directory; unset, it is
	// <home>/DefaultPlayDirRel.
	PlayDirEnv        = "NOVA_FLEET_PLAY_DIR"
	DefaultPlayDirRel = "rowan-working/rowan-tools/fleet"
	// PlayInventory is the dynamic inventory in the play directory: it
	// reads the machines registry FLEET_REGISTRY names.
	PlayInventory = "inventory.py"
	PlayRegistry  = "FLEET_REGISTRY"
	// PlayForks is the play's parallelism (the tools target's --forks 16).
	PlayForks = 16
	// PlayTimeout bounds one play.
	PlayTimeout = 15 * time.Minute
	// DefaultVerifyWait is how long the verify re-reads the beats after the
	// play (a restarted beat writes its build within a tick or two);
	// DefaultVerifyPoll is the pause between reads.
	DefaultVerifyWait = 60 * time.Second
	DefaultVerifyPoll = 5 * time.Second
)

// BeatCheck is one bench's beat against the release.
type BeatCheck struct {
	Bench, Want string
	Have        string // the version the beat names; "" when the bench has no beat
}

// OK is true when the beat names the wanted version.
func (b BeatCheck) OK() bool { return b.Have != "" && b.Have == b.Want }

// Line is the VERIFY receipt: bench, want, have, ok|behind.
func (b BeatCheck) Line() string {
	word := "behind"
	if b.OK() {
		word = "ok"
	}
	have := b.Have
	if have == "" {
		have = "none"
	}
	return fmt.Sprintf("VERIFY %s want=%s have=%s %s", b.Bench, b.Want, have, word)
}

// VerifyBeats reads bench:<b>:beat build for every bench in one pipelined
// round trip and checks each against want. A bench with no beat has Have "".
func VerifyBeats(ctx context.Context, c *redis.Client, benches []string, want string) ([]BeatCheck, error) {
	if len(benches) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(benches))
	for i, b := range benches {
		cmds[i] = pipe.HGet(ctx, "bench:"+b+":beat", "build")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]BeatCheck, len(benches))
	for i, b := range benches {
		out[i] = BeatCheck{Bench: b, Want: want, Have: beatVersion(cmds[i].Val())}
	}
	return out, nil
}

// beatVersion is the version a beat's build field names: field two of a
// nova-sprint version line, else the field itself with no whitespace.
func beatVersion(line string) string {
	if bf, ok := buildinfo.Parse(line); ok {
		return bf.Version
	}
	return strings.Join(strings.Fields(line), "_")
}

// DevTipArgv asks the remote for dev's tip without a clone.
func DevTipArgv(repo string) []string {
	return []string{"git", "ls-remote", repo, "refs/heads/" + ReleaseBase}
}

// ParseDevTip reads the sha out of git ls-remote's one line.
func ParseDevTip(out string) (string, bool) {
	f := strings.Fields(lastLine(out))
	if len(f) != 2 || f[1] != "refs/heads/"+ReleaseBase || !commitRe.MatchString(f[0]) {
		return "", false
	}
	return f[0], true
}

// PlayArgv is the fleet play for version, narrowed to limit when given.
func PlayArgv(play, version string, limit []string) []string {
	argv := []string{"ansible-playbook", "-i", PlayInventory, play, "--forks", fmt.Sprint(PlayForks), "--diff", "-e", "nova_build=" + version}
	if len(limit) > 0 {
		argv = append(argv, "--limit", strings.Join(limit, ","))
	}
	return argv
}

// PlayEnv is the play's environment: the Makefile's ansible settings and the
// registry the inventory reads.
func PlayEnv(registry string) []string {
	return []string{"ANSIBLE_NOCOWS=1", "ANSIBLE_HOST_KEY_CHECKING=True", PlayRegistry + "=" + registry}
}

// Recap is the PLAY RECAP section of ansible's output, one host per line
// with its runs of spaces folded.
func Recap(out string) []string {
	var rows []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "PLAY RECAP") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if t := strings.Join(strings.Fields(l), " "); t != "" {
			rows = append(rows, t)
		}
	}
	return rows
}

// Roll is one fleet roll.
type Roll struct {
	Release  *Release // the deploy; its Runner, Client, Machines, Benches and Out serve the roll too
	PlayDir  string
	Play     string // "" is DefaultPlay
	Registry string // the machines registry path the play's inventory reads
	RepoURL  string // where dev's tip is asked; "" is ReleaseRepoURL
	Wait     time.Duration
	Poll     time.Duration
	// Sleep pauses between verify reads; nil sleeps on a timer. Tests hand
	// in a fake, so no test waits on the clock.
	Sleep func(ctx context.Context, d time.Duration) error
}

// RollResult is what a roll did.
type RollResult struct {
	Release ReleaseResult
	Play    string // ok or failed
	Checks  []BeatCheck
}

// Behind names the benches whose beat is not on the release.
func (r RollResult) Behind() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.OK() {
			out = append(out, c.Bench)
		}
	}
	return out
}

// OK is true when the release answered, the play passed and every bench beat
// names the version.
func (r RollResult) OK() bool {
	return r.Release.OK() && r.Play == "ok" && len(r.Checks) > 0 && len(r.Behind()) == 0
}

// Line is the roll's last receipt.
func (r RollResult) Line() string {
	word := "OK"
	behind := r.Behind()
	switch {
	case len(behind) > 0:
		word = "BEHIND"
	case !r.OK():
		word = "FAIL"
	}
	list := "-"
	if len(behind) > 0 {
		list = strings.Join(behind, ",")
	}
	commit := r.Release.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("FLEET ROLL %s version=%s commit=%s fn=%s play=%s benches=%d behind=%s",
		word, r.Release.Version, commit, r.Release.Fn, r.Play, len(r.Checks), list)
}

func (r *Roll) printf(format string, a ...any) { r.Release.printf(format, a...) }

func (r *Roll) play() string {
	if r.Play != "" {
		return r.Play
	}
	return DefaultPlay
}

func (r *Roll) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// check refuses what would stop the roll halfway, before anything runs.
func (r *Roll) check() error {
	if r.Release == nil || r.Release.Runner == nil {
		return refused("fleet roll has no release to run")
	}
	if r.Release.Client == nil {
		return refused("fleet roll needs the fleet store (--redis <addr>) for the roll and the verify")
	}
	if r.Release.StudioOnly || r.Release.BenchesOnly {
		return refused("fleet roll is the whole deploy; --studio-only and --benches-only belong to fleet release")
	}
	if r.Registry == "" {
		return refused("the play's inventory reads the machines registry: --machines <file>, or %s", MachinesEnv)
	}
	if r.PlayDir == "" {
		return refused("no fleet play directory: --play-dir <dir>, or %s", PlayDirEnv)
	}
	for _, f := range []string{PlayInventory, r.play()} {
		if _, err := os.Stat(filepath.Join(r.PlayDir, f)); err != nil {
			return refused("the fleet play directory %s has no %s (--play-dir <dir>, or %s)", r.PlayDir, f, PlayDirEnv)
		}
	}
	if len(r.Release.rollList()) == 0 {
		return refused("no benches to roll: --benches <a,b,...>, or a machines registry with bench roles")
	}
	return nil
}

// Run resolves the sha (dev's tip when empty), releases it, runs the play
// and verifies the beats. An error is a refusal (ErrRefused) or the store
// failing; a failed play or benches behind are in the result.
func (r *Roll) Run(ctx context.Context, sha string) (RollResult, error) {
	var res RollResult
	if err := r.check(); err != nil {
		return res, err
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		repo := r.RepoURL
		if repo == "" {
			repo = ReleaseRepoURL
		}
		out, err := r.Release.run(ctx, "", nil, DevTipArgv(repo)...)
		if err != nil {
			return res, refused("git ls-remote %s %s: %v: %s (--to <sha> names the commit)", repo, ReleaseBase, err, lastLine(out))
		}
		tip, ok := ParseDevTip(out)
		if !ok {
			return res, refused("git ls-remote %s answered %q, not a %s tip (--to <sha> names the commit)", repo, lastLine(out), ReleaseBase)
		}
		r.printf("DEV TIP %s\n", tip)
		sha = tip
	}
	rel, err := r.Release.Run(ctx, sha)
	res.Release = rel
	if err != nil {
		return res, err
	}
	r.printf("%s\n", rel.Line())

	res.Play = "ok"
	pctx, cancel := context.WithTimeout(ctx, PlayTimeout)
	out, err := r.Release.run(pctx, r.PlayDir, PlayEnv(r.Registry), PlayArgv(r.play(), rel.Version, r.Release.Benches)...)
	cancel()
	for _, row := range Recap(out) {
		r.printf("RECAP %s\n", row)
	}
	if err != nil {
		res.Play = "failed"
		r.printf("PLAY FAIL %s version=%s err=%s last=%s\n", r.play(), rel.Version, strings.Join(strings.Fields(err.Error()), "_"), lastLine(out))
	} else {
		r.printf("PLAY OK %s version=%s\n", r.play(), rel.Version)
	}

	wait, poll := r.Wait, r.Poll
	if poll <= 0 {
		poll = DefaultVerifyPoll
	}
	if wait < 0 {
		wait = 0
	}
	benches := r.Release.rollList()
	for reads := int(wait / poll); ; reads-- {
		checks, err := VerifyBeats(ctx, r.Release.Client, benches, rel.Version)
		if err != nil {
			return res, err
		}
		res.Checks = checks
		if reads <= 0 || len(res.Behind()) == 0 {
			break
		}
		if err := r.sleep(ctx, poll); err != nil {
			return res, err
		}
	}
	for _, c := range res.Checks {
		r.printf("%s\n", c.Line())
	}
	return res, nil
}
