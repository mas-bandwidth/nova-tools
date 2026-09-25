package card

// wrapper_noresult.go is the wrapper's fallback for a model that made the
// change and wrote no RESULT.md (nova-tools#3956). On swarm-0925a kimi-k3
// ended 0/14 NATIVE INCOMPLETE with no RESULT.md: the harness exits non-zero
// (RunExitNoResult), the wrapper read that as a crash, and a commit that held
// the work was never harvested. A missing file is not a crash when the work
// exists: a code card whose harness exited non-zero on its own, that left no
// RESULT.md and whose out/repo holds a commit ahead of base_sha ends DONE.
// The wrapper writes RESULT.md from facts (line 1 the card's, line 2 DONE and
// a note naming the commit), records line 2 as `DONE (no RESULT.md, commit
// <sha8>)` and the model's commit as w_commit_sha, and the commit step then
// commits the attempt's branch as for any DONE. Anything it cannot read
// leaves the end as it was: the fallback never turns a crash into a crash of
// its own.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NoResultLine2 is the line 2 the result record carries for a card the
// wrapper ended DONE from the model's commit alone.
func NoResultLine2(sha string) string {
	return "DONE (no RESULT.md, commit " + short8(sha) + ")"
}

func short8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// NoResultCommitEnd applies the fallback: kind is the card's, job the
// attempt's job dir (RESULT.md and repo under <job>/out, card.md at <job>),
// facts the card hash's (base_sha, contract) and label the card's
// <S>/<label>/<attempt>, line 1's last resort. On ok the end is DONE done
// (the harness's exit kept as evidence), SynthLine2 and ModelCommit are set,
// and <job>/out/RESULT.md is written.
func NoResultCommitEnd(kind, job, label string, facts CardFacts, end WrapperEnd) (WrapperEnd, bool) {
	if end.Outcome != "FAILED" || end.Reason != "crash" || end.Exit <= 0 || !CodeKind(kind) {
		return end, false
	}
	out := filepath.Join(job, "out")
	result := filepath.Join(out, "RESULT.md")
	if _, err := os.Lstat(result); !errors.Is(err, fs.ErrNotExist) {
		return end, false
	}
	sha, ok := modelCommit(filepath.Join(out, "repo"), facts.BaseSHA)
	if !ok {
		return end, false
	}
	line1 := strings.TrimSpace(facts.Contract)
	if line1 == "" {
		line1 = cardLine1(filepath.Join(job, "card.md"))
	}
	if line1 == "" {
		line1 = "RESULT: " + label
	}
	body := fmt.Sprintf("%s\nDONE\nno RESULT.md from the model (harness exit %d); the wrapper ended the card from its commit %s\n", line1, end.Exit, short8(sha))
	if err := os.WriteFile(result, []byte(body), 0o644); err != nil {
		return end, false
	}
	end.Outcome, end.Reason = "DONE", "done"
	end.SynthLine2, end.ModelCommit = NoResultLine2(sha), sha
	end.Why = fmt.Sprintf("no RESULT.md; the model's commit %s is the work (harness exit %d)", short8(sha), end.Exit)
	return end, true
}

// modelCommit is HEAD of repo when it is a commit ahead of base: `git
// rev-list --count <base>..HEAD` above zero, or, with no base the clone
// knows, commits no remote has.
func modelCommit(repo, base string) (string, bool) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return "", false
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(checkEnv(os.Environ()), "GIT_TERMINAL_PROMPT=0")
		var o bytes.Buffer
		cmd.Stdout = &o
		err := cmd.Run()
		return strings.TrimSpace(o.String()), err
	}
	head, err := git("rev-parse", "--verify", "-q", "HEAD")
	if err != nil || head == "" {
		return "", false
	}
	var n string
	if base != "" {
		if _, err := git("cat-file", "-e", base+"^{commit}"); err == nil {
			n, err = git("rev-list", "--count", base+"..HEAD")
			if err != nil {
				return "", false
			}
		}
	}
	if n == "" {
		if n, err = git("rev-list", "--count", "HEAD", "--not", "--remotes"); err != nil {
			return "", false
		}
	}
	if c, err := strconv.Atoi(n); err != nil || c < 1 {
		return "", false
	}
	return head, true
}

// cardLine1 is the card body's first line when it is a RESULT line.
func cardLine1(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() && strings.HasPrefix(sc.Text(), "RESULT") {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}
