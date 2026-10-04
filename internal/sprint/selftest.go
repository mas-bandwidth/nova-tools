package sprint

// selftest land: the canned card's flow, landed for real on a scratch clone, by the
// binary that is about to serve (docs/SPEC-SPRINT.md section 14, switching the
// server's binary). The flow is the one nova-sprint help walks through (a twin file,
// a bare repository standing for the forge, the worker finishing at its pushed commit
// and land merging, pushing and reporting it): a build whose lander is broken fails it
// before it is installed, not after it has served for minutes.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// SelftestLines is the canned card's flow, as nova-sprint help prints it and as
// selftest land runs it: a line is the shell's git (each && part one git) or a verb of
// nova-sprint, run from one directory. The one substitution is the shell's own, a
// "$(git ...)" word, which is the card's head.
var SelftestLines = []string{
	"git init -q --bare origin.git && git clone -q origin.git work",
	"git -C work commit -q --allow-empty -m base && git -C work push -q origin HEAD:main",
	"nova-sprint init --readers reader-a,reader-b --members m1",
	"nova-sprint add --stream s1 --count 1",
	"nova-sprint start",
	"nova-sprint tick",
	"nova-sprint tick",
	"nova-sprint take --as m1 --epoch 0",
	"git -C work commit -q --allow-empty -m s1-1 && git -C work push -q origin HEAD:sprint/s1-1.w1.g1.e0",
	`nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --head "$(git -C work rev-parse HEAD)" --report done`,
	"nova-sprint tick",
	"nova-sprint read --as reader-a --begin --epoch 0",
	"nova-sprint read --as reader-a --ok --epoch 0",
	"nova-sprint tick",
	"nova-sprint land --stream s1 --repo-dir work --base main",
	"nova-sprint tick",
	"nova-sprint where",
}

// The canned card, as SelftestLines name it: the bare repository standing for the
// forge, the base it lands on, the card and its stream.
const (
	SelftestOrigin = "origin.git"
	SelftestBase   = "main"
	SelftestCard   = "s1-1"
	SelftestStream = "s1"
)

// SelftestLanding is the subject of the merge land makes for the canned card on the base.
func SelftestLanding() string {
	return "land " + SelftestCard + " (sprint stream " + SelftestStream + ")"
}

// selftestTwin is the twin file the flow's verbs keep the sprint in, under the run's
// directory.
const selftestTwin = "sprint.twin"

// SelftestTwin is the twin address the flow's verbs run on, for a run in dir.
func SelftestTwin(dir string) string { return "mem:" + filepath.Join(dir, selftestTwin) }

// SelftestEnv is the environment the flow's git runs in, the lander's included: no
// global or system configuration, an identity of its own and dir as HOME, so the
// caller's configuration (a hook, a signing key, a credential helper) can neither
// break the selftest nor reach the scratch clone.
func SelftestEnv(dir string) []string {
	return append(gitrun.WithoutRepoVars(os.Environ()), "HOME="+dir, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=nova-sprint selftest", "GIT_AUTHOR_EMAIL=selftest@example.invalid",
		"GIT_COMMITTER_NAME=nova-sprint selftest", "GIT_COMMITTER_EMAIL=selftest@example.invalid")
}

// SelftestGit is the flow's git for a run in dir: git in the directory it is given, in
// SelftestEnv(dir), its trimmed stdout.
func SelftestGit(dir string) func(ctx context.Context, in string, args ...string) (string, error) {
	env := SelftestEnv(dir)
	return func(ctx context.Context, in string, args ...string) (string, error) {
		return gitrun.Output(ctx, gitrun.Options{Dir: in, Env: env}, args...)
	}
}

// Selftest is one run of selftest land.
type Selftest struct {
	// Dir is the run's own directory, empty: every path the lines name is under it.
	Dir string
	// Git runs git in a directory (SelftestGit).
	Git func(ctx context.Context, dir string, args ...string) (string, error)
	// Verb runs one verb of this binary, its arguments after the tool's name, on the
	// twin SelftestTwin(Dir) names: what it printed and its exit code.
	Verb func(ctx context.Context, args []string) (out string, code int)
}

// SelftestResult is what a run found. Line is the line that failed ("" when every line
// ran and the check after them decided); Head is the card's head, Tip origin's base.
type SelftestResult struct {
	OK             bool
	Step           int
	Line, Why      string
	Head, Tip, Dir string
}

// substitution is the shell's "$(git ...)" word in a line of the flow.
var substitution = regexp.MustCompile(`"\$\(git ([^)]*)\)"`)

// Land runs the canned card's flow and checks the base on origin holds the card's
// landing: a merge whose subject is SelftestLanding on the base's first-parent line,
// with the card's head below it. Every line must answer 0; the first that does not
// ends the run red with what it printed. A lander that reports a landing and pushes
// nothing is red at the check (docs/SPEC-SPRINT.md section 14).
func (s Selftest) Land(ctx context.Context) SelftestResult {
	r := SelftestResult{Dir: s.Dir}
	fail := func(step int, line, why string) SelftestResult {
		r.Step, r.Line, r.Why = step, line, strings.TrimSpace(why)
		return r
	}
	for i, line := range SelftestLines {
		if strings.HasPrefix(line, "git ") {
			for _, part := range strings.Split(line, " && ") {
				args, err := onboarding.SplitShell(part)
				if err != nil {
					return fail(i+1, line, err.Error())
				}
				if _, err := s.Git(ctx, s.Dir, args[1:]...); err != nil {
					return fail(i+1, line, err.Error())
				}
			}
			continue
		}
		var why string
		line := substitution.ReplaceAllStringFunc(line, func(word string) string {
			args, err := onboarding.SplitShell(substitution.FindStringSubmatch(word)[1])
			if err == nil {
				r.Head, err = s.Git(ctx, s.Dir, args...)
			}
			if err != nil {
				why = err.Error()
			}
			return r.Head
		})
		if why != "" {
			return fail(i+1, SelftestLines[i], why)
		}
		args, err := onboarding.SplitShell(strings.TrimPrefix(line, "nova-sprint "))
		if err != nil {
			return fail(i+1, SelftestLines[i], err.Error())
		}
		for j := range args {
			if j > 0 && args[j-1] == "--repo-dir" {
				args[j] = filepath.Join(s.Dir, args[j])
			}
		}
		if out, code := s.Verb(ctx, args); code != 0 {
			return fail(i+1, SelftestLines[i], fmt.Sprintf("exit %d: %s", code, out))
		}
	}
	origin := filepath.Join(s.Dir, SelftestOrigin)
	subjects, err := s.Git(ctx, s.Dir, "-C", origin, "log", "--first-parent", "--format=%s", SelftestBase)
	if err != nil {
		return fail(0, "", err.Error())
	}
	if !strings.Contains("\n"+subjects+"\n", "\n"+SelftestLanding()+"\n") {
		return fail(0, "", "origin's "+SelftestBase+" does not hold "+SelftestLanding()+": the lander reported and pushed nothing")
	}
	if _, err := s.Git(ctx, s.Dir, "-C", origin, "merge-base", "--is-ancestor", r.Head, SelftestBase); err != nil {
		return fail(0, "", "origin's "+SelftestBase+" does not hold the card's head "+r.Head+": "+err.Error())
	}
	if r.Tip, err = s.Git(ctx, s.Dir, "-C", origin, "rev-parse", SelftestBase); err != nil {
		return fail(0, "", err.Error())
	}
	r.OK = true
	return r
}
