package card_test

// sweep_test.go is the bench sweep's controls (#2632). Every test runs the
// real ns_card_sweep on a throwaway redis-server and skips without one (a skip
// is not a pass).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const sweepBench = "bench-b"

// sweepFix is one fixture: jobs root R, the sibling X outside it, and RES,
// where the cards' results dirs live.
type sweepFix struct {
	ctx         context.Context
	st          *store.Store
	client      *redis.Client
	R, X, RES   string
	sprintsSeen map[string]bool
}

func newSweepFix(t *testing.T) *sweepFix {
	t.Helper()
	st, client := newSprint(t)
	base := t.TempDir()
	f := &sweepFix{ctx: context.Background(), st: st, client: client,
		R: filepath.Join(base, "R"), X: filepath.Join(base, "X"), RES: filepath.Join(base, "RES"),
		sprintsSeen: map[string]bool{}}
	for _, d := range []string{f.R, f.X, f.RES} {
		mkdir(t, d)
	}
	return f
}

func mkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sweepID(sprint, label string, attempt int) card.Identity {
	return card.Identity{Sprint: sprint, Label: label, BaseSHA: "0123abcd", Bench: sweepBench, Attempt: attempt}
}

// job makes a job dir with a file in it and returns its path.
func job(t *testing.T, dir string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, "out", "work.txt"), "work "+dir+"\n")
	return dir
}

// proven writes a results dir holding id's end.record and a RESULT.md.
func proven(t *testing.T, dir string, id card.Identity) string {
	t.Helper()
	mkdir(t, dir)
	writeRecord(t, dir, card.EndRecord{Identity: id, Outcome: "DONE", Reason: "done",
		TokenSHA: card.TokenSHA("1.x"), PushedSHA: "-", At: "1970-01-01T00:00:00Z"})
	writeFile(t, filepath.Join(dir, "RESULT.md"), "RESULT "+id.String()+"\n")
	return dir
}

// resultsFor is id's canonical results dir under RES (not created).
func (f *sweepFix) resultsFor(id card.Identity) string {
	return filepath.Join(f.RES, filepath.FromSlash(id.String()))
}

// card seeds a card hash in state on this bench and lists it in the bench's
// ended set (so a running card is a candidate too, and must be refused).
func (f *sweepFix) card(t *testing.T, id card.Identity, state, jobdir, results string) {
	t.Helper()
	token := attemptToken(id.Attempt, "0123456789abcdef0123456789abcdef")
	seedCard(t, f.ctx, f.client, id, state, token)
	fields := map[string]string{"jobdir": jobdir, "results": results}
	if err := f.client.HSet(f.ctx, card.CardKey(id.Sprint, id.Label), fields).Err(); err != nil {
		t.Fatal(err)
	}
	pipe := f.client.Pipeline()
	pipe.SAdd(f.ctx, card.BenchEndedKey(id.Sprint, sweepBench), id.Label)
	pipe.SAdd(f.ctx, "sprints", id.Sprint)
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.sprintsSeen[id.Sprint] = true
}

func (f *sweepFix) sweep(t *testing.T, root string, dry bool) (int, []string) {
	t.Helper()
	var out bytes.Buffer
	code := card.RunSweep(f.ctx, f.st, card.SweepConfig{Bench: sweepBench, Root: root, DryRun: dry}, &out)
	return code, strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
}

func (f *sweepFix) sweptAt(t *testing.T, sprint, label string) string {
	t.Helper()
	return hashOf(t, f.ctx, f.client, sprint, label)["swept_at"]
}

func (f *sweepFix) logLens(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for s := range f.sprintsSeen {
		out[s] = xlen(t, f.ctx, f.client, s)
	}
	return out
}

// treeHash is the hash of a tree's paths, modes, file contents and link
// targets, never following a link; "absent" when the root is gone.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return "absent"
	}
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s %v\n", rel, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			dst, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "-> %s\n", dst)
		case info.Mode().IsRegular():
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// commandCalls reads INFO commandstats: calls per command name.
func commandCalls(t *testing.T, ctx context.Context, client *redis.Client) map[string]int64 {
	t.Helper()
	raw, err := client.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "cmdstat_") {
			continue
		}
		for _, kv := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(kv, "calls="); ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				out[strings.TrimPrefix(name, "cmdstat_")] = n
			}
		}
	}
	return out
}

var sweepWrites = []string{"fcall", "xadd", "hset", "hdel", "set", "del", "unlink"}

func linesWith(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// cardsOf is the set of <S>/<label> in lines, the second field of each.
func cardsOf(lines []string) []string {
	var out []string
	for _, l := range lines {
		if f := strings.Fields(l); len(f) > 1 {
			out = append(out, f[1])
		}
	}
	sort.Strings(out)
	return out
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestBenchSweepDeletesOnlyEndedJobsWithResults(t *testing.T) {
	f := newSweepFix(t)
	R, X := f.R, f.X
	const S, S2, S3 = "sw", "sw2", "sw3"

	// (a) ended, results proven, job dir present.
	idA := sweepID(S, "case-a", 2)
	jobA := job(t, card.WrapperJobDir(R, S, "case-a", 2))
	resA := proven(t, f.resultsFor(idA), idA)
	f.card(t, idA, "ended", jobA, resA)
	// (b) ended, empty results dir.
	idB := sweepID(S, "case-b", 1)
	jobB := job(t, card.WrapperJobDir(R, S, "case-b", 1))
	resB := f.resultsFor(idB)
	mkdir(t, resB)
	f.card(t, idB, "ended", jobB, resB)
	// (c) running, with a live lease.
	idC := sweepID(S, "case-c", 1)
	jobC := job(t, card.WrapperJobDir(R, S, "case-c", 1))
	resC := proven(t, f.resultsFor(idC), idC)
	f.card(t, idC, "running", jobC, resC)
	if err := f.client.ZAdd(f.ctx, card.BenchLivingKey(sweepBench), redis.Z{Score: 1, Member: S + "/case-c/1"}).Err(); err != nil {
		t.Fatal(err)
	}
	// (d) a job dir with no card hash.
	jobD := job(t, card.WrapperJobDir(R, S, "orphan-d", 1))
	// (e) ended, results proven, job dir already gone.
	idE := sweepID(S, "case-e", 1)
	resE := proven(t, f.resultsFor(idE), idE)
	f.card(t, idE, "ended", card.WrapperJobDir(R, S, "case-e", 1), resE)
	// (f) outside root: the suffix matches and the dir exists, under X.
	idF := sweepID(S, "case-f", 1)
	jobF := job(t, card.WrapperJobDir(X, S, "case-f", 1))
	f.card(t, idF, "ended", jobF, proven(t, f.resultsFor(idF), idF))
	// (g) traversal: exists once resolved.
	idG := sweepID(S, "case-g", 1)
	job(t, card.WrapperJobDir(R, S, "case-g", 1))
	jobG := R + "/" + S + "/case-g/../case-g/1"
	f.card(t, idG, "ended", jobG, proven(t, f.resultsFor(idG), idG))
	// (h) symlink parent: R/<S2> -> X/<S2>.
	idH := sweepID(S2, "case-h", 1)
	job(t, card.WrapperJobDir(X, S2, "case-h", 1))
	if err := os.Symlink(filepath.Join(X, S2), filepath.Join(R, S2)); err != nil {
		t.Fatal(err)
	}
	jobH := card.WrapperJobDir(R, S2, "case-h", 1)
	f.card(t, idH, "ended", jobH, proven(t, f.resultsFor(idH), idH))
	// (i) results contains jobdir: results = R/<S3>/<label>.
	idI := sweepID(S3, "case-i", 1)
	jobI := job(t, card.WrapperJobDir(R, S3, "case-i", 1))
	resI := proven(t, filepath.Join(R, S3, "case-i"), idI)
	f.card(t, idI, "ended", jobI, resI)

	labels := []card.Identity{idA, idB, idC, idE, idF, idG, idH, idI}

	// Phase 1: the dry run changes nothing and previews exactly (a) and (e).
	treeR, treeX, treeRES := treeHash(t, R), treeHash(t, X), treeHash(t, f.RES)
	logs := f.logLens(t)
	calls := commandCalls(t, f.ctx, f.client)
	code, dry := f.sweep(t, R, true)
	if code != 0 {
		t.Fatalf("dry run exit %d, want 0: %q", code, dry)
	}
	would := linesWith(dry, "WOULD-SWEEP ")
	wantWould := []string{"WOULD-SWEEP sw/case-a attempt=2 how=deleted", "WOULD-SWEEP sw/case-e attempt=1 how=absent"}
	if strings.Join(would, "|") != strings.Join(wantWould, "|") {
		t.Fatalf("dry run WOULD-SWEEP = %q, want %q", would, wantWould)
	}
	last := dry[len(dry)-1]
	if !strings.HasPrefix(last, fmt.Sprintf("SWEEP bench=%s dry-run=1 would=2 kept=6 at=", sweepBench)) {
		t.Fatalf("dry run last line %q", last)
	}
	if treeHash(t, R) != treeR || treeHash(t, X) != treeX || treeHash(t, f.RES) != treeRES {
		t.Fatal("dry run changed a tree")
	}
	if _, err := os.Stat(jobA); err != nil {
		t.Fatalf("dry run removed (a)'s job dir: %v", err)
	}
	for _, id := range labels {
		if at := f.sweptAt(t, id.Sprint, id.Label); at != "" {
			t.Fatalf("dry run set swept_at on %s", id.Label)
		}
	}
	for s, n := range f.logLens(t) {
		if n != logs[s] {
			t.Fatalf("dry run moved s:%s:log from %d to %d", s, logs[s], n)
		}
	}
	if n, err := f.client.Exists(f.ctx, card.SweepLockKey(sweepBench)).Result(); err != nil || n != 0 {
		t.Fatalf("dry run left the lock (%d, %v)", n, err)
	}
	after := commandCalls(t, f.ctx, f.client)
	for _, c := range sweepWrites {
		if after[c] != calls[c] {
			t.Fatalf("dry run sent %s: %d -> %d", c, calls[c], after[c])
		}
	}

	// Phase 2: the sweep deletes only (a)'s job dir and records (a) and (e).
	keep := map[string]string{}
	for name, dir := range map[string]string{"b": jobB, "c": jobC, "d": jobD, "f": jobF,
		"g": card.WrapperJobDir(R, S, "case-g", 1), "h": card.WrapperJobDir(X, S2, "case-h", 1),
		"i-results": resI, "X": X, "RES": f.RES} {
		keep[name] = treeHash(t, dir)
	}
	code, got := f.sweep(t, R, false)
	if code != 0 {
		t.Fatalf("sweep exit %d, want 0: %q", code, got)
	}
	swept := linesWith(got, "SWEPT ")
	wantSwept := []string{"SWEPT sw/case-a attempt=2 how=deleted", "SWEPT sw/case-e attempt=1 how=absent"}
	if strings.Join(swept, "|") != strings.Join(wantSwept, "|") {
		t.Fatalf("SWEPT = %q, want %q", swept, wantSwept)
	}
	for _, want := range []string{"KEPT sw/case-b NO-RESULTS", "KEPT sw/case-c NOT-ENDED", "KEPT sw/case-f OUTSIDE",
		"KEPT sw/case-g TRAVERSAL", "KEPT sw2/case-h SYMLINK", "KEPT sw3/case-i OVERLAP"} {
		if !hasLine(got, want) {
			t.Fatalf("sweep output lacks %q: %q", want, got)
		}
	}
	if strings.Join(linesWith(dry, "KEPT "), "|") != strings.Join(linesWith(got, "KEPT "), "|") {
		t.Fatalf("dry-run KEPT %q != sweep KEPT %q", linesWith(dry, "KEPT "), linesWith(got, "KEPT "))
	}
	if strings.Join(cardsOf(would), "|") != strings.Join(cardsOf(swept), "|") {
		t.Fatalf("WOULD-SWEEP cards %q != SWEPT cards %q", cardsOf(would), cardsOf(swept))
	}
	if !strings.HasPrefix(got[len(got)-1], fmt.Sprintf("SWEEP bench=%s swept=2 kept=6 at=", sweepBench)) {
		t.Fatalf("sweep last line %q", got[len(got)-1])
	}
	if _, err := os.Lstat(jobA); !os.IsNotExist(err) {
		t.Fatalf("(a)'s job dir is still there: %v", err)
	}
	for name, dir := range map[string]string{"b": jobB, "c": jobC, "d": jobD, "f": jobF,
		"g": card.WrapperJobDir(R, S, "case-g", 1), "h": card.WrapperJobDir(X, S2, "case-h", 1),
		"i-results": resI, "X": X, "RES": f.RES} {
		if treeHash(t, dir) != keep[name] {
			t.Fatalf("sweep changed %s (%s)", name, dir)
		}
	}
	for _, id := range labels {
		at := f.sweptAt(t, id.Sprint, id.Label)
		want := id == idA || id == idE
		if (at != "") != want {
			t.Fatalf("%s swept_at=%q, want set=%v", id.Label, at, want)
		}
	}
	if h := hashOf(t, f.ctx, f.client, S, "case-a"); h["sweep_how"] != "deleted" || h["sweep_receipt"] == "" || h["state"] != "ended" {
		t.Fatalf("(a) record: how=%q receipt=%q state=%q", h["sweep_how"], h["sweep_receipt"], h["state"])
	}
	if h := hashOf(t, f.ctx, f.client, S, "case-e"); h["sweep_how"] != "absent" {
		t.Fatalf("(e) sweep_how=%q, want absent", h["sweep_how"])
	}
	msgs, err := f.client.XRange(f.ctx, card.LogKey(S3), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Values["to"] == "swept" {
			t.Fatalf("s:%s:log has a to=swept entry for (i): %v", S3, m.Values)
		}
	}
	if n, err := f.client.Exists(f.ctx, card.SweepLockKey(sweepBench)).Result(); err != nil || n != 0 {
		t.Fatalf("sweep left the lock (%d, %v)", n, err)
	}
}

func TestBenchSweepIsIdempotent(t *testing.T) {
	f := newSweepFix(t)
	id := sweepID("sw", "twice", 1)
	jobdir := job(t, card.WrapperJobDir(f.R, "sw", "twice", 1))
	f.card(t, id, "ended", jobdir, proven(t, f.resultsFor(id), id))
	if code, got := f.sweep(t, f.R, false); code != 0 || !hasLine(got, "SWEPT sw/twice attempt=1 how=deleted") {
		t.Fatalf("first sweep %d %q", code, got)
	}
	n := xlen(t, f.ctx, f.client, "sw")
	receipt := hashOf(t, f.ctx, f.client, "sw", "twice")["sweep_receipt"]
	code, got := f.sweep(t, f.R, false)
	if code != 0 || !hasLine(got, "KEPT sw/twice ALREADY") {
		t.Fatalf("second sweep %d %q", code, got)
	}
	if xlen(t, f.ctx, f.client, "sw") != n {
		t.Fatal("second sweep wrote to the log")
	}
	if hashOf(t, f.ctx, f.client, "sw", "twice")["sweep_receipt"] != receipt {
		t.Fatal("second sweep rewrote the receipt")
	}
}

func TestBenchSweepRedisDownDeletesNothing(t *testing.T) {
	f := newSweepFix(t)
	id := sweepID("sw", "down", 1)
	jobdir := job(t, card.WrapperJobDir(f.R, "sw", "down", 1))
	f.card(t, id, "ended", jobdir, proven(t, f.resultsFor(id), id))
	before := treeHash(t, f.R)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()
	client := redis.NewClient(&redis.Options{Addr: dead, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	down := store.New(client)
	for _, dry := range []bool{false, true} {
		var out bytes.Buffer
		code := card.RunSweep(f.ctx, down, card.SweepConfig{Bench: sweepBench, Root: f.R, DryRun: dry}, &out)
		if code != card.SweepExitRedis {
			t.Fatalf("dry=%v exit %d, want %d: %q", dry, code, card.SweepExitRedis, out.String())
		}
		if treeHash(t, f.R) != before {
			t.Fatalf("dry=%v changed the jobs root with Redis down", dry)
		}
	}
}

func TestBenchSweepRefusesSymlinkAndOverlap(t *testing.T) {
	f := newSweepFix(t)
	R := f.R
	Y := filepath.Join(filepath.Dir(R), "Y")
	mkdir(t, Y)

	// A symlinked leaf, pointing inside R.
	idLeaf := sweepID("sa", "leaf", 1)
	mkdir(t, filepath.Join(R, "sa", "leaf"))
	job(t, filepath.Join(R, "elsewhere", "leaf-target"))
	if err := os.Symlink(filepath.Join(R, "elsewhere", "leaf-target"), card.WrapperJobDir(R, "sa", "leaf", 1)); err != nil {
		t.Fatal(err)
	}
	f.card(t, idLeaf, "ended", card.WrapperJobDir(R, "sa", "leaf", 1), proven(t, f.resultsFor(idLeaf), idLeaf))
	// A symlinked <S>/<label> ancestor, pointing inside R.
	idLab := sweepID("sa", "lab", 1)
	job(t, filepath.Join(R, "elsewhere", "lab", "1"))
	if err := os.Symlink(filepath.Join(R, "elsewhere", "lab"), filepath.Join(R, "sa", "lab")); err != nil {
		t.Fatal(err)
	}
	f.card(t, idLab, "ended", card.WrapperJobDir(R, "sa", "lab", 1), proven(t, f.resultsFor(idLab), idLab))

	// Overlap: equal, jobdir contains results, results contains jobdir, and
	// results spelled through an alias Y/alias -> R/so/alias.
	idEq := sweepID("so", "eq", 1)
	jobEq := job(t, card.WrapperJobDir(R, "so", "eq", 1))
	f.card(t, idEq, "ended", jobEq, proven(t, jobEq, idEq))
	idAnc := sweepID("so", "anc", 1)
	jobAnc := job(t, card.WrapperJobDir(R, "so", "anc", 1))
	f.card(t, idAnc, "ended", jobAnc, proven(t, filepath.Join(jobAnc, "res"), idAnc))
	idDesc := sweepID("so", "desc", 1)
	jobDesc := job(t, card.WrapperJobDir(R, "so", "desc", 1))
	f.card(t, idDesc, "ended", jobDesc, proven(t, filepath.Join(R, "so", "desc"), idDesc))
	idAlias := sweepID("so", "alias", 1)
	jobAlias := job(t, card.WrapperJobDir(R, "so", "alias", 1))
	proven(t, filepath.Join(R, "so", "alias"), idAlias)
	if err := os.Symlink(filepath.Join(R, "so", "alias"), filepath.Join(Y, "alias")); err != nil {
		t.Fatal(err)
	}
	f.card(t, idAlias, "ended", jobAlias, filepath.Join(Y, "alias"))

	// Control: a sibling sharing a prefix (.../1 and .../10) is no overlap.
	idPre := sweepID("so", "pre", 1)
	jobPre := job(t, card.WrapperJobDir(R, "so", "pre", 1))
	resPre := proven(t, filepath.Join(R, "so", "pre", "10"), idPre)
	f.card(t, idPre, "ended", jobPre, resPre)

	overlapDirs := map[string]string{"eq": jobEq, "anc": jobAnc, "desc": filepath.Join(R, "so", "desc"), "alias": filepath.Join(R, "so", "alias"), "Y": Y}
	before := map[string]string{}
	for k, d := range overlapDirs {
		before[k] = treeHash(t, d)
	}
	elsewhere := treeHash(t, filepath.Join(R, "elsewhere"))
	preRes := treeHash(t, resPre)

	code, got := f.sweep(t, R, false)
	if code != 0 {
		t.Fatalf("sweep exit %d: %q", code, got)
	}
	for _, want := range []string{"KEPT sa/leaf SYMLINK", "KEPT sa/lab SYMLINK",
		"KEPT so/eq OVERLAP", "KEPT so/anc OVERLAP", "KEPT so/desc OVERLAP", "KEPT so/alias OVERLAP",
		"SWEPT so/pre attempt=1 how=deleted"} {
		if !hasLine(got, want) {
			t.Fatalf("sweep output lacks %q: %q", want, got)
		}
	}
	for k, d := range overlapDirs {
		if treeHash(t, d) != before[k] {
			t.Fatalf("sweep changed %s (%s)", k, d)
		}
	}
	if treeHash(t, filepath.Join(R, "elsewhere")) != elsewhere {
		t.Fatal("sweep changed a symlink target")
	}
	for _, id := range []card.Identity{idLeaf, idLab, idEq, idAnc, idDesc, idAlias} {
		if at := f.sweptAt(t, id.Sprint, id.Label); at != "" {
			t.Fatalf("%s/%s swept_at=%q, want empty", id.Sprint, id.Label, at)
		}
	}
	if _, err := os.Lstat(jobPre); !os.IsNotExist(err) {
		t.Fatalf("control job dir still there: %v", err)
	}
	if treeHash(t, resPre) != preRes {
		t.Fatal("control's sibling results dir changed")
	}
}

func TestBenchSweepRootFromConfigOnly(t *testing.T) {
	f := newSweepFix(t)
	id := sweepID("sw", "root", 1)
	jobdir := job(t, card.WrapperJobDir(f.R, "sw", "root", 1))
	f.card(t, id, "ended", jobdir, proven(t, f.resultsFor(id), id))
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", "relative/R", "/", home, f.R + "/../R"} {
		before := commandCalls(t, f.ctx, f.client)
		var out bytes.Buffer
		code := card.RunSweep(f.ctx, f.st, card.SweepConfig{Bench: sweepBench, Root: root}, &out)
		if code != card.SweepExitRoot || !strings.HasPrefix(out.String(), "SWEEP-REFUSED root=") {
			t.Fatalf("root %q: exit %d %q, want %d SWEEP-REFUSED", root, code, out.String(), card.SweepExitRoot)
		}
		after := commandCalls(t, f.ctx, f.client)
		for c, n := range after {
			if c != "info" && n != before[c] {
				t.Fatalf("root %q sent %s before refusing", root, c)
			}
		}
	}
	// R given as a symlink to the real dir: Rreal is used and the card is swept.
	link := filepath.Join(t.TempDir(), "Rlink")
	if err := os.Symlink(f.R, link); err != nil {
		t.Fatal(err)
	}
	id2 := sweepID("sw", "via-link", 1)
	jobLink := job(t, card.WrapperJobDir(link, "sw", "via-link", 1))
	f.card(t, id2, "ended", jobLink, proven(t, f.resultsFor(id2), id2))
	code, got := f.sweep(t, link, false)
	if code != 0 || !hasLine(got, "SWEPT sw/via-link attempt=1 how=deleted") {
		t.Fatalf("link root sweep: %d %q", code, got)
	}
	if _, err := os.Lstat(card.WrapperJobDir(f.R, "sw", "via-link", 1)); !os.IsNotExist(err) {
		t.Fatalf("job dir under Rreal still there: %v", err)
	}
}

func TestSweepFnRefusesStaleIdentity(t *testing.T) {
	f := newSweepFix(t)
	id := sweepID("sw", "stale", 2)
	f.card(t, id, "ended", card.WrapperJobDir(f.R, "sw", "stale", 2), f.resultsFor(id))
	before := hashOf(t, f.ctx, f.client, "sw", "stale")
	n := xlen(t, f.ctx, f.client, "sw")
	old := sweepID("sw", "stale", 1)
	keys := []string{card.CardKey("sw", "stale"), card.LogKey("sw"), card.IdemKey("sw")}
	raw, err := f.client.FCall(f.ctx, "ns_card_sweep", keys, "sw", "stale", old.String(), "absent", sweepBench, "/j").Text()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "2|IDENTITY|") {
		t.Fatalf("stale identity reply %q, want 2|IDENTITY", raw)
	}
	after := hashOf(t, f.ctx, f.client, "sw", "stale")
	if fmt.Sprint(after) != fmt.Sprint(before) || xlen(t, f.ctx, f.client, "sw") != n {
		t.Fatal("a stale identity wrote to the card or the log")
	}
	// The current identity is recorded, once.
	raw, err = f.client.FCall(f.ctx, "ns_card_sweep", keys, "sw", "stale", id.String(), "absent", sweepBench, "/j").Text()
	if err != nil || !strings.HasPrefix(raw, "0|OK|2|") {
		t.Fatalf("current identity reply %q %v", raw, err)
	}
	raw, _ = f.client.FCall(f.ctx, "ns_card_sweep", keys, "sw", "stale", id.String(), "absent", sweepBench, "/j").Text()
	if !strings.HasPrefix(raw, "2|ALREADY|") {
		t.Fatalf("second record reply %q, want 2|ALREADY", raw)
	}
}
