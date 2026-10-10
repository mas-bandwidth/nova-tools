package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// A report's form is the machine's check at the finish, never a reader's
// (docs/SPEC-SPRINT.md, the report's form). The finish refuses a form miss and stamps the
// work card's form_refusals, but only on a card the finish itself would finish: a finish
// from another member, or one naming a stale generation, is refused by the finish and must
// not spend the owner's attempt. formRig drives that through the finish command, beside a
// local git repository the card's REPO: names and the lander's clone the finish reads the
// report blob from.
type formRig struct {
	*testApp
	dir, remote, sha string
}

func newFormRig(t *testing.T, report string) *formRig {
	t.Helper()
	r := &formRig{testApp: newTestApp(t), dir: t.TempDir()}
	git := func(in string, args ...string) string {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: in, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: in != ""}, args...)
		require.NoError(t, err, "git %v: %s", args, res.Stderr)
		return strings.TrimSpace(string(res.Stdout))
	}
	r.remote = filepath.Join(r.dir, "remote.git")
	work := filepath.Join(r.dir, "work")
	git("", "init", "-q", "--bare", "-b", "main", r.remote)
	git("", "init", "-q", "-b", "main", work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "REPORT.md"), []byte(report), 0o600))
	git(work, "add", "REPORT.md")
	git(work, "commit", "-q", "-m", "the report")
	git(work, "push", "-q", r.remote, "HEAD:refs/heads/main")
	r.sha = git(work, "rev-parse", "HEAD")
	land := filepath.Join(r.dir, "land", repoDirName(r.remote))
	require.NoError(t, os.MkdirAll(filepath.Dir(land), 0o755))
	git("", "clone", "-q", r.remote, land)
	r.a.gitEnv = testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	r.a.landRoot = func() (string, error) { return filepath.Join(r.dir, "land"), nil }
	r.ok("init --readers reader-a,reader-b --members m1,m2")
	return r
}

// brief is a card whose FORM: block holds REPORT.md to one rule, and whose REPO: names the
// test repository, so the finish reads the report blob from the lander's clone.
func (r *formRig) brief() string {
	return "s1-1: the report card (s1) tier: flash\nREPO: " + r.remote + "\nFORM: REPORT.md\nline 1: Verdict: LAND\n"
}

// seed admits one card with brief, deals it and takes it for m1, so the work card s1-1.w1
// is m1's to finish.
func (r *formRig) seed(brief string) {
	r.t.Helper()
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(r.t, err)
	res, err := st.Run(context.Background(), store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: brief}))
	require.NoError(r.t, err)
	require.Empty(r.t, res.Refused)
	r.deal(1)
	r.ok("take --as m1 s1-1.w1@1")
}

// TestAForeignFinishDoesNotStampAFormRefusal: a finish from another member is refused by
// the finish itself and stamps no form_refusals, so two such calls cannot turn the owner's
// next form miss into the third that fails the attempt.
func TestAForeignFinishDoesNotStampAFormRefusal(t *testing.T) {
	t.Parallel()
	r := newFormRig(t, "Verdict: HOLD\n")
	r.seed(r.brief())
	for i := 0; i < 2; i++ {
		code, out, errs := r.do("finish --as m2 s1-1.w1@1 --head " + r.sha)
		require.Equal(t, 1, code, "foreign finish %d: %d %s%s", i, code, out, errs)
		require.Contains(t, errs, "dealt to m1, not m2", "foreign finish %d: %s%s", i, out, errs)
		require.Equal(t, 0, r.fleetCard("s1-1.w1").Int(sprint.FormRefusalsField), "foreign finish %d stamped a form refusal", i)
	}
	// the owner's first miss is still the first refusal, never the third that fails
	code, _, errs := r.do("finish --as m1 s1-1.w1@1 --head " + r.sha)
	require.Equal(t, 1, code, "the owner's first miss: %s", errs)
	require.Contains(t, errs, "FORM: ", "the owner's first miss names the form: %s", errs)
	require.Equal(t, 1, r.fleetCard("s1-1.w1").Int(sprint.FormRefusalsField), "the owner's first miss is the first refusal")
}

// TestAStaleFinishDoesNotStampAFormRefusal: the card dealt again to another member has a
// new generation, and a finish naming the old one is refused by the finish itself, stamping
// no form_refusals, so two such calls cannot spend the holder's next miss.
func TestAStaleFinishDoesNotStampAFormRefusal(t *testing.T) {
	t.Parallel()
	r := newFormRig(t, "Verdict: HOLD\n")
	r.seed(r.brief())
	r.ok("fleet down m1") // the card is returned and dealt again at generation 2
	r.ok("take --as m2 s1-1.w1@2")
	for i := 0; i < 2; i++ {
		code, out, errs := r.do("finish --as m1 s1-1.w1@1 --head " + r.sha)
		require.Equal(t, 1, code, "stale finish %d: %d %s%s", i, code, out, errs)
		require.Contains(t, errs, "stale", "stale finish %d: %s%s", i, out, errs)
		require.Equal(t, 0, r.fleetCard("s1-1.w1").Int(sprint.FormRefusalsField), "stale finish %d stamped a form refusal", i)
	}
	code, _, errs := r.do("finish --as m2 s1-1.w1@2 --head " + r.sha)
	require.Equal(t, 1, code, "the holder's first miss: %s", errs)
	require.Contains(t, errs, "FORM: ", "the holder's first miss names the form: %s", errs)
	require.Equal(t, 1, r.fleetCard("s1-1.w1").Int(sprint.FormRefusalsField), "the holder's first miss is the first refusal")
}

// A first form finish can precede every landing, so the lander's persistent
// repository clone does not yet exist. The report still has to be read from the
// pushed head instead of turning a valid report into three form refusals.
func TestAFirstFormFinishReadsTheHeadWithoutALandClone(t *testing.T) {
	t.Parallel()
	r := newFormRig(t, "Verdict: LAND\n")
	require.NoError(t, os.RemoveAll(filepath.Join(r.dir, "land")))
	body, err := r.a.formReportBlob(context.Background(), r.brief(), r.sha, "main", "REPORT.md")
	require.NoError(t, err)
	require.Equal(t, "Verdict: LAND\n", string(body))
}
