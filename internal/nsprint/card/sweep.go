package card

// sweep.go is the bench sweep (#2632): the job dir a crashed wrapper left
// behind. The wrapper deletes its own job dir at card end (wrapper.go, step
// 7); a wrapper that dies never does, and nothing in Redis says the dir is
// gone. `nova-sprint bench sweep` runs on the bench, reads its ended cards
// from Redis (never a directory walk), and deletes a card's job dir only when
// every one of these holds, in this order:
//
//   - the card is ended and not already swept;
//   - containment: the stored jobdir is clean and absolute (else TRAVERSAL),
//     is byte for byte <root>/<S>/<label>/<attempt> under the configured root
//     (else OUTSIDE), and each of <S>, <label> and <attempt> under the
//     resolved root is a real directory, not a symlink (else SYMLINK; a
//     missing one is how=absent);
//   - results: the card's results dir holds an end.record naming the card's
//     identity and a RESULT.md (else NO-RESULTS);
//   - overlap: the results dir and the job dir are not the same directory and
//     neither contains the other, decided by device and inode (else OVERLAP);
//   - the delete is safepath.RemoveUnder(root, candidate).
//
// Then ns_card_sweep records swept_at, sweep_how and sweep_receipt on the
// card hash. A dry run runs every check in the same order and stops before
// the lock, the delete and the FCALL: its only Redis commands are SMEMBERS and
// HMGET. A dir with no card hash is never listed and never deleted.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/redis/go-redis/v9"
)

// SweepJobsEnv is the jobs root the sweep reads: the same variable nova-card
// reads for the wrapper's JobsRoot, so the wrapper and the sweeper share it.
const SweepJobsEnv = "NOVA_CARD_JOBS"

// SweepLockTTL bounds bench:<b>:sweep:lock, the sweep's only TTL.
const SweepLockTTL = 60 * time.Second

// sweepBatch is the HMGET pipeline width.
const sweepBatch = 256

// Sweep exit codes.
const (
	SweepExitOK    = 0 // the sweep (or the preview) completed
	SweepExitBusy  = 1 // another sweep holds the bench's lock
	SweepExitRoot  = 2 // the jobs root was refused
	SweepExitRedis = 6 // Redis unavailable
)

// SweepConfig is one sweep. Root is the raw NOVA_CARD_JOBS value.
type SweepConfig struct {
	Bench  string
	Sprint string // empty is every sprint in `sprints`
	Root   string
	DryRun bool
	Now    func() time.Time
}

// SweepLockKey is the bench's sweep lock.
func SweepLockKey(bench string) string { return "bench:" + bench + ":sweep:lock" }

// SweepRoot checks the configured jobs root and returns it resolved. why is
// empty when the root is usable. Nothing here reads Redis.
func SweepRoot(v string) (rootReal string, why string) {
	switch {
	case v == "":
		return "", "unset"
	case !filepath.IsAbs(v):
		return "", "relative"
	case filepath.Clean(v) != v:
		return "", "unclean"
	}
	info, err := os.Stat(v)
	if err != nil {
		return "", "missing"
	}
	if !info.IsDir() {
		return "", "not-a-directory"
	}
	if why := unsafeRoot(v); why != "" {
		return "", why
	}
	rootReal, err = filepath.EvalSymlinks(v)
	if err != nil {
		return "", "unresolved"
	}
	// The resolved root is the boundary actually used (safepath's rule).
	if why := unsafeRoot(rootReal); why != "" {
		return "", why
	}
	return rootReal, ""
}

// unsafeRoot mirrors safepath's unsafe-root rule, by identity: the whole disk
// and the user's home are not a boundary. It fails closed.
func unsafeRoot(root string) string {
	if same, err := sameDirIdentity(root, string(os.PathSeparator)); err != nil {
		return "unidentified"
	} else if same {
		return "whole-disk"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		same, err := sameDirIdentity(root, home)
		if err != nil {
			return "unidentified"
		}
		if same {
			return "home"
		}
	}
	return ""
}

func sameDirIdentity(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// sweepCard is one candidate as Redis holds it.
type sweepCard struct {
	Sprint, Label                                string
	State, JobDir, Results, Identity, SweptAt, B string
}

// sweepVerdict is one card's outcome: How is deleted|absent when the card is
// taken, else Kept names why not.
type sweepVerdict struct {
	Card      sweepCard
	ID        Identity
	Candidate string
	How       string
	Kept      string
}

// RunSweep runs one sweep (or one dry run) and prints its lines to out. It
// returns the exit code. The root is checked before any Redis command.
func RunSweep(ctx context.Context, st *store.Store, cfg SweepConfig, out io.Writer) int {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	rreal, why := SweepRoot(cfg.Root)
	if why != "" {
		fmt.Fprintf(out, "SWEEP-REFUSED root=%q why=%s\n", cfg.Root, why)
		return SweepExitRoot
	}
	c := st.Client()
	token := ""
	if !cfg.DryRun {
		token = fmt.Sprintf("%d", now().UnixNano())
		ok, err := c.SetNX(ctx, SweepLockKey(cfg.Bench), token, SweepLockTTL).Result()
		if err != nil {
			return sweepRedisDown(out, cfg.Bench, err)
		}
		if !ok {
			fmt.Fprintf(out, "SWEEP-BUSY bench=%s lock=%s; another sweep is running, wait for it\n", cfg.Bench, SweepLockKey(cfg.Bench))
			return SweepExitBusy
		}
		defer func() {
			_ = c.FCall(context.WithoutCancel(ctx), "ns_card_sweep_unlock", []string{SweepLockKey(cfg.Bench)}, token).Err()
		}()
	}
	cards, err := sweepCandidates(ctx, c, cfg)
	if err != nil {
		return sweepRedisDown(out, cfg.Bench, err)
	}
	taken, kept := 0, 0
	for _, sc := range cards {
		v := judgeSweep(cfg.Root, rreal, sc)
		if v.Kept == "" && !cfg.DryRun {
			if err := applySweep(ctx, st, rreal, cfg.Bench, &v); err != nil {
				fmt.Fprintf(out, "SWEEP-REFUSED bench=%s why=redis %s\n", cfg.Bench, oneLine(err))
				return SweepExitRedis
			}
		}
		card := sc.Sprint + "/" + sc.Label
		switch {
		case v.Kept != "":
			kept++
			fmt.Fprintf(out, "KEPT %s %s\n", card, v.Kept)
		case cfg.DryRun:
			taken++
			fmt.Fprintf(out, "WOULD-SWEEP %s attempt=%d how=%s\n", card, v.ID.Attempt, v.How)
		default:
			taken++
			fmt.Fprintf(out, "SWEPT %s attempt=%d how=%s\n", card, v.ID.Attempt, v.How)
		}
	}
	at := now().UTC().Format(time.RFC3339)
	if cfg.DryRun {
		fmt.Fprintf(out, "SWEEP bench=%s dry-run=1 would=%d kept=%d at=%s\n", cfg.Bench, taken, kept, at)
	} else {
		fmt.Fprintf(out, "SWEEP bench=%s swept=%d kept=%d at=%s\n", cfg.Bench, taken, kept, at)
	}
	return SweepExitOK
}

func sweepRedisDown(out io.Writer, bench string, err error) int {
	fmt.Fprintf(out, "SWEEP-REFUSED bench=%s why=redis %s\n", bench, oneLine(err))
	return SweepExitRedis
}

func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// sweepCandidates reads the bench's ended cards: SMEMBERS sprints (or the one
// sprint), one pipeline of SMEMBERS of each ended set, then one pipelined
// HMGET per sweepBatch labels. Sorted by sprint then label.
func sweepCandidates(ctx context.Context, c *redis.Client, cfg SweepConfig) ([]sweepCard, error) {
	sprints := []string{cfg.Sprint}
	if cfg.Sprint == "" {
		got, err := c.SMembers(ctx, "sprints").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		sprints = got
	}
	sort.Strings(sprints)
	if len(sprints) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	ended := make([]*redis.StringSliceCmd, len(sprints))
	for i, s := range sprints {
		ended[i] = pipe.SMembers(ctx, BenchEndedKey(s, cfg.Bench))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var cards []sweepCard
	for i, s := range sprints {
		labels := ended[i].Val()
		sort.Strings(labels)
		for _, l := range labels {
			cards = append(cards, sweepCard{Sprint: s, Label: l})
		}
	}
	for lo := 0; lo < len(cards); lo += sweepBatch {
		hi := min(lo+sweepBatch, len(cards))
		pipe := c.Pipeline()
		got := make([]*redis.SliceCmd, hi-lo)
		for i := lo; i < hi; i++ {
			got[i-lo] = pipe.HMGet(ctx, CardKey(cards[i].Sprint, cards[i].Label), "state", "jobdir", "results", "identity", "swept_at", "bench")
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for i := lo; i < hi; i++ {
			v := got[i-lo].Val()
			str := func(j int) string {
				if j < len(v) {
					if s, ok := v[j].(string); ok {
						return s
					}
				}
				return ""
			}
			cards[i].State, cards[i].JobDir, cards[i].Results = str(0), str(1), str(2)
			cards[i].Identity, cards[i].SweptAt, cards[i].B = str(3), str(4), str(5)
		}
	}
	return cards, nil
}

// judgeSweep runs every read-only check, in order, for one card. It reads the
// file system (EvalSymlinks, Lstat, Stat, end.record, RESULT.md) and changes
// nothing; the dry run and the sweep both use it.
func judgeSweep(root, rreal string, sc sweepCard) sweepVerdict {
	v := sweepVerdict{Card: sc}
	if sc.State != "ended" {
		v.Kept = "NOT-ENDED"
		return v
	}
	if sc.SweptAt != "" {
		v.Kept = "ALREADY"
		return v
	}
	id, err := ParseIdentity(sc.Identity)
	if err != nil || id.Sprint != sc.Sprint || id.Label != sc.Label {
		v.Kept = "NO-RESULTS"
		return v
	}
	v.ID = id
	if sc.JobDir == "" {
		v.Kept = "NO-JOBDIR"
		return v
	}
	// Containment 1: a clean absolute path with no "." or ".." element.
	if !cleanAbs(sc.JobDir) {
		v.Kept = "TRAVERSAL"
		return v
	}
	// Containment 2: the wrapper's own path under the configured root.
	if sc.JobDir != WrapperJobDir(root, id.Sprint, id.Label, id.Attempt) && sc.JobDir != WrapperJobDir(rreal, id.Sprint, id.Label, id.Attempt) {
		v.Kept = "OUTSIDE"
		return v
	}
	// Containment 3: every element under the resolved root a real directory.
	cand := WrapperJobDir(rreal, id.Sprint, id.Label, id.Attempt)
	absent := false
	for _, p := range []string{filepath.Join(rreal, id.Sprint), filepath.Join(rreal, id.Sprint, id.Label), cand} {
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			absent = true
			break
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			v.Kept = "SYMLINK"
			return v
		}
	}
	// Results: the card's own end.record and a RESULT.md, in its results dir.
	if !resultsProven(sc.Results, id) {
		v.Kept = "NO-RESULTS"
		return v
	}
	if absent {
		v.How = "absent"
		return v
	}
	// Overlap: fails closed on any error.
	if !cleanAbs(sc.Results) {
		v.Kept = "OVERLAP"
		return v
	}
	resReal, err := filepath.EvalSymlinks(sc.Results)
	if err != nil {
		v.Kept = "OVERLAP"
		return v
	}
	if over, err := overlaps(resReal, cand); err != nil || over {
		v.Kept = "OVERLAP"
		return v
	}
	v.Candidate, v.How = cand, "deleted"
	return v
}

// cleanAbs: absolute, filepath.Clean-equal, and no "." or ".." element.
func cleanAbs(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, el := range strings.Split(filepath.ToSlash(p), "/") {
		if el == "." || el == ".." {
			return false
		}
	}
	return true
}

// resultsProven: the results dir's end.record parses and names id, and
// RESULT.md is a file there.
func resultsProven(results string, id Identity) bool {
	if results == "" {
		return false
	}
	rec, err := ReadEndRecord(results)
	if err != nil || rec.Identity != id {
		return false
	}
	info, err := os.Stat(filepath.Join(results, "RESULT.md"))
	return err == nil && info.Mode().IsRegular()
}

// overlaps reports whether res and cand are the same directory or either is
// an ancestor of the other, deciding every step by device and inode (it
// mirrors safepath's strictlyUnder and sameDir). So .../1 and .../10 do not
// overlap, and a results path spelled through a symlink alias is still
// caught once resolved. An error is returned, never read as "no overlap".
func overlaps(res, cand string) (bool, error) {
	ri, err := os.Stat(res)
	if err != nil {
		return false, err
	}
	ci, err := os.Stat(cand)
	if err != nil {
		return false, err
	}
	if os.SameFile(ri, ci) {
		return true, nil
	}
	if under, err := ancestorIs(cand, ri); err != nil || under {
		return true, err
	}
	return ancestorIs(res, ci)
}

// ancestorIs walks up from path's parent to the volume root and reports
// whether some ancestor is target.
func ancestorIs(path string, target os.FileInfo) (bool, error) {
	at := filepath.Clean(path)
	for {
		parent := filepath.Dir(at)
		if parent == at {
			return false, nil
		}
		info, err := os.Stat(parent)
		if err != nil {
			return false, err
		}
		if os.SameFile(info, target) {
			return true, nil
		}
		at = parent
	}
}

// applySweep is containment 4 and the record: the delete through safepath,
// the empty <S>/<label> and <S> parents after it (as the wrapper's own
// cleanup does), then ns_card_sweep. A refusal sets v.Kept; only an
// unavailable Redis is an error.
func applySweep(ctx context.Context, st *store.Store, rreal, bench string, v *sweepVerdict) error {
	if v.How == "deleted" {
		if err := safepath.RemoveUnder(rreal, v.Candidate); err != nil {
			if errors.Is(err, safepath.ErrUnsafe) {
				v.Kept = "OUTSIDE"
			} else {
				v.Kept = "IO"
			}
			return nil
		}
		for _, dir := range []string{filepath.Dir(v.Candidate), filepath.Dir(filepath.Dir(v.Candidate))} {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	sc := v.Card
	reply, err := fcall(ctx, st, "ns_card_sweep", cardKeys(sc.Sprint, sc.Label),
		sc.Sprint, sc.Label, sc.Identity, v.How, bench, sc.JobDir)
	if err != nil {
		if redisUnavailable(err) {
			return err
		}
		v.Kept = "REFUSED"
		return nil
	}
	if reply.Code != 0 {
		v.Kept = reply.Status
	}
	return nil
}
