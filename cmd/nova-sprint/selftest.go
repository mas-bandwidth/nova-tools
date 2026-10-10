package main

// selftest.go is the install gate a hand step of an install of 2026-10-04
// becomes: the build installed there refused every landing for nine minutes
// ("the base dev fails the tree gate": go build said "package os is not in
// std") while its unit tests were green, and the install was gated by hand
// with a shell script that ran the help's real walkthrough (twin.go
// realSteps) with the built binary. That hand step is a verb, so an install
// gate runs the binary it is about to install: `nova-sprint selftest
// [--dir <d>] [--keep]` lands one card on a twin through the tree gate and
// prints one line (docs/SPEC-SPRINT.md section 11, selftest). It opens no
// Redis and no network: the store is a twin file in a fresh directory of the
// selftest's own, the forge a bare repository in that directory, and the
// programs it runs are git and, through the lander's tree gate, go itself.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
)

func init() {
	// Every verb has one class (docs/SPEC-SPRINT.md section 11; the class test
	// holds every verb of the table has one): selftest is the machine's, like
	// tick — it opens no store of the caller's, so it needs no actor.
	verbClasses["selftest"] = classMachine
}

// selftestGoMod is the go.mod of the module the selftest lands through, and
// selftestMain its one program: a module importing fmt and os, so the lander's
// tree gate (landgo.go treeGate: go build ./..., go vet ./...) really builds
// and vets — a clone with no go.mod has no gate, which is how the install of
// 2026-10-04 hid its broken go from a walkthrough that landed an empty base.
// The go directive is the one of this module's go.mod, read from the
// toolchain that built this binary (the module pins its toolchain).
func selftestGoMod() string {
	return "module selftest\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\n"
}

const selftestMain = `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stdout, len(os.Args))
}
`

// cmdSelftest is the verb: the whole landing of one card through the tree
// gate, in a fresh directory the selftest owns, and one line that says
// whether it worked (docs/SPEC-SPRINT.md section 11, selftest).
func (a *app) cmdSelftest(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("selftest")
	dir := fs.String("dir", "", "the directory to make the selftest's fresh directory in (default: the system's temporary directory)")
	keep := fs.Bool("keep", false, "keep the fresh directory after the selftest (it is removed on success unless this is given; a failure always keeps it, named in the line)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "selftest", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "selftest", "takes no words, found "+pos[0])
	}
	root := *dir
	if root == "" {
		root = os.TempDir()
	}
	d, err := os.MkdirTemp(root, "nova-sprint-selftest-")
	if err != nil {
		return refuse(stderr, "selftest", "the fresh directory could not be made under "+oneline.Field(root)+": "+oneline.Err(err))
	}
	start := time.Now()
	land, landed, step, why := a.selftestFlow(d, a.selftestEnv(d))
	if step != "" {
		// a failure keeps the directory and names it, so the finding can be
		// read where it happened
		fmt.Fprintf(stderr, "SELFTEST FAILED step=%s why=%s dir=%s\n", oneline.Field(step), oneline.Escape(oneline.Cap(why, 1500)), oneline.Field(d))
		return 1
	}
	fmt.Fprintf(stdout, "SELFTEST OK landed=%d land=%s gate=%s go=%s dir=%s\n",
		landed, land.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), runtime.Version(), oneline.Field(d))
	if !*keep {
		// the removal is safepath's, never a raw one (docs/STANDARD.md section 9,
		// removeall); a removal that cannot run is said, never silent
		if err := safepath.RemoveUnder(root, d); err != nil {
			fmt.Fprintf(stdout, "NOTE the fresh directory %s was not removed: %s; remove it by hand\n", oneline.Field(d), oneline.Err(err))
		}
	}
	return 0
}

// selftestEnv is the environment the selftest's git and the lander's gate run
// in: the caller's (a test gives its own through gitEnv) with the fresh
// directory as HOME, no global or system git config, and a fixed identity, so
// nothing of the caller's is read, nothing of the caller's is written, and the
// clone the selftest makes is the selftest's alone. PATH is the caller's, so
// the gate runs the go an install would run.
func (a *app) selftestEnv(d string) []string {
	env := a.gitEnv
	if env == nil {
		env = os.Environ()
	}
	return withEnv(env,
		"HOME="+d,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=selftest",
		"GIT_AUTHOR_EMAIL=selftest@example.invalid",
		"GIT_COMMITTER_NAME=selftest",
		"GIT_COMMITTER_EMAIL=selftest@example.invalid",
	)
}

// selftestFlow runs the card's flow of the help's walkthrough (twin.go
// realSteps), in process, on a twin file in d: a bare origin.git standing for
// the forge and a clone work whose base commit holds the go module, then init
// with two readers and one member, one card, start, ticks, take, a commit
// pushed to the card's branch, finish at its head, a read ok, land and a tick,
// and the landing read back from origin's main. It returns the land step's
// duration and the cards it landed, or the step that failed and why.
func (a *app) selftestFlow(d string, env []string) (land time.Duration, landed int, step, why string) {
	ctx := context.Background()
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{Dir: d, Env: env}, args...)
		if err != nil {
			return "", errors.New(firstLine(string(res.Stderr), err))
		}
		return strings.TrimSpace(string(res.Stdout)), nil
	}
	// the forge and the worker's checkout, as the walkthrough makes them
	if _, err := git("init", "-q", "--bare", "origin.git"); err != nil {
		step, why = "origin", err.Error()
		return
	}
	if _, err := git("clone", "-q", "origin.git", "work"); err != nil {
		step, why = "clone", err.Error()
		return
	}
	work := filepath.Join(d, "work")
	// the base commit holds the go module, so the gate that meets it below
	// really builds: this is the half of the walkthrough that was missing
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(selftestGoMod()), 0o644); err != nil {
		step, why = "base", err.Error()
		return
	}
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(selftestMain), 0o644); err != nil {
		step, why = "base", err.Error()
		return
	}
	for _, args := range [][]string{
		{"-C", "work", "add", "--", "go.mod", "main.go"},
		{"-C", "work", "commit", "-q", "-m", "base"},
		{"-C", "work", "push", "-q", "origin", "HEAD:main"},
	} {
		if _, err := git(args...); err != nil {
			step, why = "base", err.Error()
			return
		}
	}
	// the flow's verbs, one fresh app a verb, as the walkthrough runs them: the
	// twin is loaded from the file before each verb and saved to it after
	twin := "mem:" + filepath.Join(d, "sprint.twin")
	verb := func(args ...string) (code int, out, errs string) {
		e := map[string]string{"NOVA_SPRINT_REDIS": twin, "NOVA_SPRINT_ACTOR": "boss"}
		ia := newApp(func(k string) string { return e[k] })
		ia.gitEnv = env
		var o, errb bytes.Buffer
		code = ia.run(args, &o, &errb)
		ia.close()
		return code, o.String(), errb.String()
	}
	stepVerb := func(name string, args ...string) (string, string) {
		code, out, errs := verb(args...)
		if code != 0 {
			return name, selftestStepWhy(out, errs)
		}
		return "", ""
	}
	for _, s := range []struct {
		name string
		args []string
	}{
		{"init", []string{"init", "--readers", "reader-a,reader-b", "--members", "m1"}},
		{"add", []string{"add", "--stream", "s1", "--count", "1", "--one"}}, // one card on purpose: add refuses a single card without --one
		// the throwaway origin stands for the promotion stream: its main is the protected
		// branch the flow lands on (docs/SPEC-SPRINT.md section 7, the protected branches)
		{"mark", []string{"stream", "set", "s1", "--land-protected", "any"}},
		{"start", []string{"start"}},
		{"tick", []string{"tick"}},
		{"tick", []string{"tick"}},
		{"take", []string{"take", "--as", "m1", "--epoch", "0"}},
	} {
		if step, why = stepVerb(s.name, s.args...); step != "" {
			return
		}
	}
	// the worker's empty commit, pushed to the card's branch, as the packet
	// names it (sprint/<card>.g<gen>.e<epoch>)
	for _, args := range [][]string{
		{"-C", "work", "commit", "-q", "--allow-empty", "-m", "s1-1"},
		{"-C", "work", "push", "-q", "origin", "HEAD:sprint/s1-1.w1.g1.e0"},
	} {
		if _, err := git(args...); err != nil {
			step, why = "commit", err.Error()
			return
		}
	}
	head, err := git("-C", "work", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		step, why = "commit", err.Error()
		return
	}
	for _, s := range []struct {
		name string
		args []string
	}{
		{"finish", []string{"finish", "--as", "m1", "s1-1.w1@1", "--epoch", "0", "--head", head, "--report", "done"}},
		{"tick", []string{"tick"}},
		{"read", []string{"read", "--as", "reader-a", "--begin", "--epoch", "0"}},
		{"read", []string{"read", "--as", "reader-a", "--ok", "--epoch", "0"}},
		{"tick", []string{"tick"}},
	} {
		if step, why = stepVerb(s.name, s.args...); step != "" {
			return
		}
	}
	// the landing, timed: the tree gate runs here, on the base the flow built
	began := time.Now()
	code, out, errs := verb("land", "--repo-dir", work, "--base", "main")
	land = time.Since(began)
	if code != 0 {
		step, why = "land", selftestLandWhy(out, errs)
		return
	}
	landed = selftestLanded(out)
	if step, why = stepVerb("tick", "tick"); step != "" {
		return
	}
	// the landing is on origin's main: the card, landed for real
	subjects, err := git("-C", "origin.git", "log", "--format=%s", "main")
	if err != nil {
		step, why = "check", err.Error()
		return
	}
	if !strings.Contains(subjects, "land s1-1 (sprint stream s1)") {
		step, why = "check", "origin's main holds no landing of the card, whose subjects are "+firstLine(subjects, nil)
		return
	}
	return
}

// selftestStepWhy is the first thing the step said when it failed, its own
// words, stderr first: the verb prints what did not move and why there.
func selftestStepWhy(out, errs string) string {
	for _, text := range [2]string{errs, out} {
		for _, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l
			}
		}
	}
	return "it failed and said nothing"
}

// selftestLandWhy is the lander's own reason for a landing that did not land,
// never a paraphrase: the reason of its LAND REFUSED or LAND FAILED line, and
// the first thing it said when it printed no line of that shape.
func selftestLandWhy(out, errs string) string {
	for _, text := range [2]string{errs, out} {
		for _, l := range strings.Split(text, "\n") {
			_, rest, ok := strings.Cut(l, " reason=")
			if ok && (strings.HasPrefix(l, "LAND REFUSED ") || strings.HasPrefix(l, "LAND FAILED ")) {
				return rest
			}
		}
	}
	return selftestStepWhy(out, errs)
}

// selftestLanded is the cards the land verb reports it landed, the cards= of
// its LAND OK line; 0 when it printed none.
func selftestLanded(out string) int {
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "LAND OK ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(f, "cards="); ok {
				if n, err := strconv.Atoi(v); err == nil {
					return n
				}
			}
		}
	}
	return 0
}
