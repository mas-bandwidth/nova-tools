package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// readStartRE is the line of a read's JOB.md that shows the work's change.
var readStartRE = regexp.MustCompile(`(?m)^    git diff --stat ([0-9a-f]{40})\.\.HEAD$`)

// A read staged after its base branch moved (cards landed on it while the work was read) is
// told the commit the work started from and the command that shows exactly the work's
// change, in every profile; a diff against the moved base shows the landed files too, which
// a reader on the 1000-card load test (2026-10-01) took for deletions and sent a correct
// work card back for (docs/SPEC-CARD-CONTRACT.md, JOB.md).
func TestAReadIsToldTheWorksChangeWhenTheBaseMoved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	start := gitAs(t, origin, "rev-parse", "main")

	// the work: one file, on its sprint branch
	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	require.NoError(t, os.MkdirAll(filepath.Join(w, "quacks"), 0o755))
	write(t, filepath.Join(w, "quacks", "w.txt"), "quack\n")
	gitAs(t, w, "add", "quacks/w.txt")
	gitAs(t, w, "commit", "-q", "-m", "quack: w")
	head := gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")

	// the base moves: two other cards land on main after the work began
	l := filepath.Join(root, "l")
	runGit(t, "", "clone", "-q", "--", origin, l)
	for _, name := range []string{"landed-a.txt", "landed-b.txt"} {
		write(t, filepath.Join(l, name), name+"\n")
		gitAs(t, l, "add", name)
		gitAs(t, l, "commit", "-q", "-m", name)
	}
	runGit(t, l, "push", "-q", "origin", "main")

	for _, model := range []string{"anthropic/claude-x", "openai/gpt-x", "fake/model-x"} {
		t.Run(cardcontract.FamilyOf(model), func(t *testing.T) {
			t.Parallel()
			slot := filepath.Join(root, "slot-"+cardcontract.FamilyOf(model))
			job := filepath.Join(slot, "jobs", "w.r1")
			require.NoError(t, os.MkdirAll(job, 0o755))
			fr := &cardcontract.Frame{Kind: "read", Card: "w.r1", Attempt: 1, Model: model, Repo: origin,
				BaseRef: "main", ReviewBase: "main", StageSha: head, Branch: "sprint/w"}
			st, err := swarm.StageCard(swarm.StageOptions{TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
				BenchHome: filepath.Join(root, "no-bench"), Base: &swarm.CardBase{Repo: origin, Sha: head, Ref: "main", Named: origin}, Branch: fr.Branch})
			require.NoError(t, err)
			require.Equal(t, head, st.BaseSha)
			checkout := filepath.Join(job, swarm.JobRepo)
			require.ElementsMatch(t, []string{"landed-a.txt", "landed-b.txt", "quacks/w.txt"},
				strings.Fields(runGit(t, checkout, "diff", "--name-only", "origin/main")), "the base moved: a diff against it is more than the work")

			require.NoError(t, installFrame(nativeRunConfig{slotDir: slot, model: model, frame: fr}, job, st.BaseSha))
			jobText, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
			require.NoError(t, err)
			m := readStartRE.FindStringSubmatch(string(jobText))
			require.NotNil(t, m, "JOB.md names the command that shows the work's change:\n%s", jobText)
			assert.Equal(t, start, m[1], "the start is the commit the work began from")
			assert.Contains(t, string(jobText), "    git diff "+start+"..HEAD\n")
			assert.Contains(t, string(jobText), "main may have moved since the work began")
			assert.Equal(t, []string{"quacks/w.txt"}, strings.Fields(runGit(t, checkout, "diff", "--name-only", m[1]+"..HEAD")),
				"the command JOB.md names shows exactly the work's change")
		})
	}
}
