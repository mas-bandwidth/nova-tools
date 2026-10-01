package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// landRig is the sprint over the in-memory store beside a local bare git
// repository as origin: a worker's clone that pushes each card's head, and
// the coordinator's clone land is given (--repo-dir). No network.
type landRig struct {
	*testApp
	dir, remote, worker, clone string
	env                        []string
}

func newLandRig(t *testing.T) *landRig {
	t.Helper()
	r := &landRig{testApp: newTestApp(t), dir: t.TempDir()}
	r.env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=lander", "GIT_AUTHOR_EMAIL=lander@example.invalid", "GIT_COMMITTER_NAME=lander", "GIT_COMMITTER_EMAIL=lander@example.invalid")
	r.remote, r.worker, r.clone = filepath.Join(r.dir, "remote.git"), filepath.Join(r.dir, "worker"), filepath.Join(r.dir, "clone")
	r.git("", "init", "-q", "--bare", "-b", "main", r.remote)
	r.git("", "clone", "-q", r.remote, r.worker)
	r.commit("README", "base\n", "base")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git("", "clone", "-q", r.remote, r.clone)
	r.a.gitEnv = r.env
	r.a.landRoot = func() (string, error) { return filepath.Join(r.dir, "land"), nil }
	r.ok("init --readers reader-a,reader-b --members m1")
	return r
}

// git runs git in dir and returns its trimmed stdout.
func (r *landRig) git(dir string, args ...string) string {
	r.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: r.env, OwnRepo: dir != ""}, args...)
	require.NoError(r.t, err, "git %v: %s", args, res.Stderr)
	return strings.TrimSpace(string(res.Stdout))
}

// commit writes file in the worker's clone and commits it.
func (r *landRig) commit(file, text, msg string) string {
	r.t.Helper()
	require.NoError(r.t, os.WriteFile(filepath.Join(r.worker, file), []byte(text), 0o600))
	r.git(r.worker, "add", file)
	r.git(r.worker, "commit", "-q", "-m", msg)
	return r.git(r.worker, "rev-parse", "HEAD")
}

// head is a card's work as a member makes it: one commit writing file on
// the base, kept as the card's branch for queued to push; its sha.
func (r *landRig) head(id, base, file, text string) string {
	r.t.Helper()
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/"+id, "refs/remotes/origin/"+base)
	return r.commit(file, text, "work of "+id)
}

// moveBase puts one more commit on origin's base, as another lander would.
func (r *landRig) moveBase(base, file string) {
	r.t.Helper()
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--detach", "origin/"+base)
	r.commit(file, "moved\n", "moved "+file)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/"+base)
}

// queued pushes the cards' heads to origin, as each member does, and takes
// the cards through the sprint to the merge queue, each finished at its head,
// in the order given.
func (r *landRig) queued(heads map[string]string, order ...string) {
	r.t.Helper()
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	r.deal(len(order))
	r.ok("take --as m1 --limit 100")
	for _, id := range order {
		r.ok("finish --as m1 " + id + ".w1@1 --head " + heads[id])
	}
	r.ok("ask")
	r.ok("read --as reader-a --ok --limit 100")
	r.ok("read --as reader-b --ok --limit 100")
	r.ok("accept --read-ok")
}

// places is each card's place in the work table and the merge table.
func (r *landRig) places(ids ...string) map[string]string {
	r.t.Helper()
	out := map[string]string{}
	for _, id := range ids {
		var v cardView
		r.json("card "+id, &v)
		out[id] = v.Primary.Col + "/" + v.Merge.Col
	}
	return out
}

// mainLog is the first-parent subjects of origin's main, newest first.
func (r *landRig) mainLog() []string {
	return strings.Split(r.git(r.remote, "log", "--first-parent", "--format=%s", "main"), "\n")
}

// streamState is a stream's state and cause in the merge table.
func (r *landRig) streamState(stream string) string {
	r.t.Helper()
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(r.t, err)
	s, err := st.Load(context.Background(), []string{sprint.Merge}, nil)
	require.NoError(r.t, err)
	ctl := s.StreamCtl(stream)
	return strings.TrimSuffix(ctl.F("state")+" "+ctl.F("cause"), " ")
}

// A three-card stream lands in queue order as one batch: one merge commit a
// card on origin's base, the check run once on the batch branch, and the
// store moved as merge --stream s1 --batch 3 moves it.
func TestLandMergesAStreamInQueueOrderAsOneBatch(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	out := r.ok("land --repo-dir " + r.clone + " --base main --check 'test -f s1-1.txt && test -f s1-3.txt'")
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=3 base=main tip="+tip+" ids=s1-1..s1-3")
	assert.Contains(t, out, "LAND DONE batches=1 cards=3 refused=0")
	assert.Equal(t, []string{"land s1-3 (sprint stream s1)", "land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged", "s1-3": "landed/merged"}, r.places("s1-1", "s1-2", "s1-3"))
	// nothing is left queued: the next land lands nothing and says so
	assert.Contains(t, r.ok("land --repo-dir "+r.clone), "LAND DONE batches=0 cards=0 refused=0")
	r.clean()
}

// A head that does not merge ends its batch: the cards before it land, it is
// reported with the merge step's conflict fact carrying git's words, and the
// card behind it stays queued.
func TestLandStopsAtAHeadThatDoesNotMerge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, why string
		second    func(r *landRig) string
	}{
		{"a conflicting head", "does not merge", func(r *landRig) string { return r.head("s1-2", "main", "s1-1.txt", "other\n") }},
		{"a missing head", "is missing", func(*landRig) string { return strings.Repeat("ab", 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 3")
			heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"), "s1-2": tc.second(r), "s1-3": r.head("s1-3", "main", "s1-3.txt", "three\n")}
			r.queued(heads, "s1-1", "s1-2", "s1-3")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Equal(t, 1, code)
			assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
			assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 "+tc.why)
			assert.Contains(t, errs, "LAND DONE batches=1 cards=1 refused=1")
			assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "base"}, r.mainLog())
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck", "s1-3": "merging/queued"}, r.places("s1-1", "s1-2", "s1-3"))
			assert.Equal(t, "stopped conflict", r.streamState("s1"))
			assert.Contains(t, r.ok("inbox"), "stream stopped: conflict on a card")
			r.clean()
		})
	}
}

// A base that moved under the push is fetched and the batch rebuilt on its
// new tip once; a second rejection is reported with the rejected fact and
// the cards stay queued.
func TestLandRebuildsOnceOnAMovedBase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		moves  int
		code   int
		want   string
		state  string
		places string
	}{
		{"moved once", 1, 0, "LAND OK stream=s1 cards=2", "landed", "landed/merged"},
		{"moved twice", 2, 1, "LAND REFUSED stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2 dir=", "stopped rejected", "merging/queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}
			r.queued(heads, "s1-1", "s1-2")
			var pushes []int
			r.a.beforePush = func(attempt int) {
				pushes = append(pushes, attempt)
				if attempt <= tc.moves {
					r.moveBase("main", "moved"+strconv.Itoa(attempt)+".txt")
				}
			}
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Equal(t, tc.code, code, out+errs)
			assert.Contains(t, out+errs, tc.want)
			assert.Equal(t, []int{1, 2}, pushes)
			assert.Equal(t, tc.state, r.streamState("s1"))
			assert.Equal(t, map[string]string{"s1-1": tc.places, "s1-2": tc.places}, r.places("s1-1", "s1-2"))
			if tc.moves == 1 {
				assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "moved moved1.txt", "base"}, r.mainLog())
			}
			r.clean()
		})
	}
}

// The landing and the report are one operation: a batch pushed whose report
// the store cannot take (the queue changed under the push) is LAND FAILED,
// exit 2, with the exact merge command to run.
func TestLandSaysLoudlyWhenAPushedBatchIsNotReported(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	r.a.beforePush = func(int) { r.ok("return s1-1 --reason 'taken back under the push'") }
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 2, code)
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Contains(t, errs, "LAND FAILED stream=s1 cards=2 base=main tip="+tip+" ids=s1-1..s1-2")
	assert.Contains(t, errs, "and NOT reported (s1: the merge queue of s1 holds 1 cards now, fewer than the batch of 2")
	assert.Contains(t, errs, "run land again, which rereads the queue and lets its checks decide")
	assert.Contains(t, errs, ": nova-sprint land --stream s1\n")
	assert.NotContains(t, errs, "merge --stream", "a bare merge step would pass the head guard")
	assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	// the guard landed nothing it did not push: s1-1 went back to review, s1-2 is still queued
	assert.Equal(t, map[string]string{"s1-1": "review/returned", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
	// running land again recovers the pushed and unreported card (tla/Land.tla, Recovers)
	r.a.beforePush = nil
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main tip="+tip+" ids=s1-2")
	assert.Equal(t, tip, r.git(r.remote, "rev-parse", "main"), "the recovery pushed nothing new")
	assert.Equal(t, "landed/merged", r.places("s1-2")["s1-2"])
	r.clean()
}

// A check that fails pushes nothing and reports the batch red.
func TestLandACheckThatFailsPushesNothing(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	before := r.git(r.remote, "rev-parse", "main")
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main --check 'echo the tests are red; exit 3'")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2")
	assert.Contains(t, errs, "fact=red reason=the check echo the tests are red; exit 3 failed: exit status 3: the tests are red")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, "stopped red", r.streamState("s1"))
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
	r.clean()
}

// --dry-run prints the batches and changes nothing: no clone, no push, no
// write to the store.
func TestLandDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	before, applies := r.git(r.remote, "rev-parse", "main"), r.applies()
	out := r.ok("land --repo-dir " + r.clone + " --base main --dry-run")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2 dir="+r.clone+" dry_run=yes")
	assert.Contains(t, out, "LAND DONE batches=1 cards=2 refused=0 dry_run=yes")
	var v struct {
		Status string      `json:"status"`
		Cards  int         `json:"cards"`
		Items  []landBatch `json:"items"`
	}
	r.json("land --repo-dir "+r.clone+" --base main --dry-run", &v)
	require.Len(t, v.Items, 1)
	assert.Equal(t, "ok", v.Status)
	assert.Equal(t, 2, v.Cards)
	assert.Equal(t, []string{"s1-1", "s1-2"}, v.Items[0].IDs)
	assert.True(t, v.Items[0].DryRun)
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, applies, r.applies())
	_, err := os.Stat(filepath.Join(r.dir, "land"))
	assert.True(t, os.IsNotExist(err), "a dry run made the land directory")
	assert.NotContains(t, r.git(r.clone, "branch", "--list"), "land/s1")
}

// Two streams land as two batches, in a clone land keeps under its root, each
// card's repository and base read from its brief's REPO: and BASE: lines; a
// card naming another base is a batch of its own.
func TestLandTwoStreamsAsTheirOwnBatches(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "push", "-q", "origin", "main:refs/heads/alt")
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --brief-file " + brief("a", "main") + " --brief-file " + brief("b", "main"))
	r.ok("add --stream s2 --brief-file " + brief("c", "main") + " --brief-file " + brief("d", "alt"))
	heads := map[string]string{}
	for id, base := range map[string]string{"a": "main", "b": "main", "c": "main", "d": "alt"} {
		heads[id] = r.head(id, base, id+".txt", id+"\n")
	}
	r.queued(heads, "a", "b", "c", "d")
	out := r.ok("land")
	kept := filepath.Join(r.dir, "land", repoDirName(r.remote))
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.Contains(t, out, "ids=a..b repo="+r.remote+" dir="+kept)
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=alt")
	assert.Contains(t, out, "LAND DONE batches=3 cards=4 refused=0")
	assert.Less(t, strings.Index(out, "stream=s1"), strings.Index(out, "stream=s2"))
	assert.Equal(t, []string{"land c (sprint stream s2)", "land b (sprint stream s1)", "land a (sprint stream s1)", "base"}, r.mainLog())
	assert.Equal(t, "land d (sprint stream s2)", r.git(r.remote, "log", "-1", "--format=%s", "alt"))
	assert.Equal(t, map[string]string{"a": "landed/merged", "b": "landed/merged", "c": "landed/merged", "d": "landed/merged"}, r.places("a", "b", "c", "d"))
	r.clean()
}

// The refusals land makes before any git: a card naming no repository with no
// clone given, and a card naming no base with no --base.
func TestLandRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	for _, tc := range []struct{ line, want string }{
		{"land --base main", "the card names no REPO: line and no --repo-dir was given; run: nova-sprint land --repo-dir <clone>"},
		{"land --repo-dir " + r.clone, "card s1-1 names no BASE: line and no --base was given; run: nova-sprint land --stream s1 --base <branch>"},
		{"land --stream s9 --repo-dir " + r.clone, "no such stream"},
	} {
		code, _, errs := r.do(tc.line)
		assert.Equal(t, 1, code, tc.line)
		assert.Contains(t, errs, tc.want, tc.line)
	}
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
}

// normRepo makes one repository's spellings equal and keeps two apart.
func TestLandNamesARepositoryOneWay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"https://forge.test/owner/name.git", "git@forge.test:owner/name", true},
		{"https://forge.test/owner/name", "ssh://git@forge.test/owner/name.git/", true},
		{"https://forge.test/owner/name.git", "https://forge.test/owner/other.git", false},
		{"/srv/git/name.git", "/srv/git/name", true},
		{"/srv/git/Repo.git", "/srv/git/repo.git", false},
		{"https://Forge.TEST/owner/name.git", "https://forge.test/owner/name", true},
		{"https://forge.test/Owner/name.git", "https://forge.test/owner/name.git", false},
	} {
		assert.Equal(t, tc.same, sameRepo(tc.a, tc.b), "%s %s", tc.a, tc.b)
	}
	assert.Regexp(t, `^forge\.test-owner-name-[0-9a-f]{16}$`, repoDirName("https://forge.test/owner/name.git"))
	assert.Equal(t, repoDirName("https://forge.test/owner/name.git"), repoDirName("git@forge.test:owner/name"))
}

// The review's findings (#5020, held at e95b7961), each a property.

// Two repositories never share a kept clone: names that read alike (a-b/c and
// a/b-c) differ by their hash, and a kept clone whose origin is another
// repository is refused before any fetch or push, both remotes untouched.
func TestLandReviewClonesOfTwoRepositoriesNeverShareADirectory(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, repoDirName("https://example.invalid/a-b/c.git"), repoDirName("https://example.invalid/a/b-c.git"))
	r := newLandRig(t)
	other := filepath.Join(r.dir, "other.git")
	r.git("", "init", "-q", "--bare", "-b", "main", other)
	r.git(r.worker, "push", "-q", other, "refs/remotes/origin/main:refs/heads/main")
	kept := filepath.Join(r.dir, "land", repoDirName(r.remote))
	require.NoError(t, os.MkdirAll(filepath.Dir(kept), 0o755))
	r.git("", "clone", "-q", other, kept) // a kept clone at the card repository's place, of another repository
	briefs := t.TempDir()
	path := filepath.Join(briefs, "a.md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite a.txt.")), 0o600))
	r.ok("add --stream s1 a --brief-file " + path)
	r.queued(map[string]string{"a": r.head("a", "main", "a.txt", "a\n")}, "a")
	before, otherBefore := r.git(r.remote, "rev-parse", "main"), r.git(other, "rev-parse", "main")
	code, _, errs := r.do("land")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the card names the repository "+r.remote+" and the clone "+kept+" fetches from "+other+"; nothing was fetched or pushed")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, otherBefore, r.git(other, "rev-parse", "main"))
	assert.Equal(t, map[string]string{"a": "merging/queued"}, r.places("a"))
}

// A caller's --epoch the sprint has left is refused before any git: nothing is
// fetched, merged, pushed or reported.
func TestLandReviewAWrongEpochPushesNothing(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	before, applies := r.git(r.remote, "rev-parse", "main"), r.applies()
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main --epoch 999")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the sprint is at epoch 0, not 999 (cleared since): nothing was fetched, pushed or reported; run: nova-sprint where")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, applies, r.applies())
	assert.NotContains(t, r.git(r.clone, "branch", "--list"), "land/s1")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
}

// A git that fails for a reason that is not the card's (here an empty
// identity) blames no card: nothing is reported, the card stays queued, the
// stream keeps merging, and land exits non-zero with git's reason.
func TestLandReviewAGitEnvironmentFailureBlamesNoCard(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n"), "s1-2": r.head("s1-2", "main", "b.txt", "b\n")}, "s1-1", "s1-2")
	r.a.gitEnv = append(append([]string(nil), r.env...), "GIT_AUTHOR_NAME=", "GIT_COMMITTER_NAME=")
	before := r.git(r.remote, "rev-parse", "main")
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2")
	assert.Contains(t, errs, "the merge of s1-1 failed in git, not on its changes")
	assert.Contains(t, errs, "empty ident name")
	assert.Contains(t, errs, "no card is blamed and nothing was pushed or reported")
	assert.NotContains(t, errs, "fact=")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
	assert.Equal(t, "merging", r.streamState("s1"))
	r.clean()
}

// A card reworked between the build and the report keeps its id and its
// epoch but not its head: the report, guarded on the head and attempt land
// pinned, refuses it, and the new attempt stays queued while the base holds
// the old one (tla/Land.tla, idguard).
func TestLandRefusesAReworkedHeadAndLandsItOnTheNextRun(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	old := r.head("s1-1", "main", "a.txt", "attempt 1\n")
	r.queued(map[string]string{"s1-1": old}, "s1-1")
	var replacement string
	r.a.beforePush = func(int) {
		r.ok("return s1-1 --reason 'replaced under the push'")
		r.ok("rework s1-1 --fix 'again'")
		replacement = r.head("s1-1.2", "main", "b.txt", "attempt 2\n")
		r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
		var q struct{ Cards []queueCard }
		r.json("queue --as m1", &q)
		if len(q.Cards) == 0 {
			r.deal(1)
			r.json("queue --as m1", &q)
		}
		require.Len(t, q.Cards, 1)
		w := q.Cards[0].ID + "@" + strconv.Itoa(q.Cards[0].Gen)
		if q.Cards[0].Col == "ready" {
			r.ok("take --as m1 " + w)
		}
		r.ok("finish --as m1 " + w + " --head " + replacement)
		r.ok("ask")
		r.ok("read --as reader-a --ok --limit 100")
		r.ok("read --as reader-b --ok --limit 100")
		r.ok("accept --read-ok")
	}
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "LAND FAILED stream=s1 cards=1")
	assert.Contains(t, errs, "s1-1 is at attempt 2 head "+replacement+" now, not attempt 1 head "+old)
	// the remedy is land again, never the merge step: here the queue starts
	// with s1-1 and merge --batch 1 would record the head the base lacks
	assert.Contains(t, errs, "a card reworked since is merged at its new head, or meets a real conflict): nova-sprint land --stream s1")
	assert.NotContains(t, errs, "merge --stream")
	assert.Equal(t, "attempt 1\n", r.git(r.remote, "show", "main:a.txt")+"\n")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	r.a.beforePush = nil
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.Equal(t, "landed/merged", r.places("s1-1")["s1-1"])
	assert.Equal(t, "attempt 2", r.git(r.remote, "show", "main:b.txt"), "the landed attempt is the one the base holds")
	r.clean()
}

// An --op given again names another operation for another batch: its op id
// is the op and the batch's pinned heads, so the receipt of the first batch
// never stands for the second, which lands for real.
func TestLandAReusedOpLandsTheNewBatchForReal(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first")
	r.queued(map[string]string{"first": r.head("first", "main", "a.txt", "a\n")}, "first")
	assert.Contains(t, r.ok("land --repo-dir "+r.clone+" --base main --op repeated-land"), "LAND OK stream=s1 cards=1")
	r.ok("add --stream s1 second")
	r.queued(map[string]string{"second": r.head("second", "main", "b.txt", "b\n")}, "second")
	out := r.ok("land --repo-dir " + r.clone + " --base main --op repeated-land")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1")
	assert.Contains(t, out, "ids=second")
	assert.Equal(t, map[string]string{"first": "landed/merged", "second": "landed/merged"}, r.places("first", "second"))
	assert.Equal(t, []string{"land second (sprint stream s1)", "land first (sprint stream s1)", "base"}, r.mainLog())
	r.clean()
}

// Every URL git push would write to is held to the repository: a clone whose
// origin fetches from the card's repository and pushes to another is refused
// before either repository changes; so is one with two push URLs.
func TestLandHoldsEveryPushURLToTheRepository(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		urls func(r *landRig, other string) []string
		want string
	}{
		{"another push URL", func(_ *landRig, other string) []string { return []string{other} }, " and pushes to "},
		{"two push URLs", func(r *landRig, other string) []string { return []string{r.remote, other} }, "pushes origin to 2 URLs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			other := filepath.Join(r.dir, "other.git")
			r.git("", "init", "-q", "--bare", "-b", "main", other)
			r.git(r.worker, "push", "-q", other, "refs/remotes/origin/main:refs/heads/main")
			for _, u := range tc.urls(r, other) {
				r.git(r.clone, "remote", "set-url", "--add", "--push", "origin", u)
			}
			briefs := t.TempDir()
			path := filepath.Join(briefs, "a.md")
			require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite a.txt.")), 0o600))
			r.ok("add --stream s1 a --brief-file " + path)
			r.queued(map[string]string{"a": r.head("a", "main", "a.txt", "a\n")}, "a")
			before, otherBefore := r.git(r.remote, "rev-parse", "main"), r.git(other, "rev-parse", "main")
			code, _, errs := r.do("land --repo-dir " + r.clone)
			assert.Equal(t, 1, code)
			assert.Contains(t, errs, tc.want)
			assert.Contains(t, errs, "nothing was fetched or pushed")
			assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
			assert.Equal(t, otherBefore, r.git(other, "rev-parse", "main"))
			assert.Equal(t, map[string]string{"a": "merging/queued"}, r.places("a"))
		})
	}
}
