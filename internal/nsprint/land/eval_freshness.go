package land

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// InboundConsumerKey is the land consumer's beat on ev:github (3.2): last_id,
// pending, at. ns_inbound_beat is its one writer.
const InboundConsumerKey = "ev:github:consumer:land"

// BranchUnitKey is the unit whose branch is <branch> in <repo>, written by
// ns_unit_head, so a ref resolves to its unit without a scan.
func BranchUnitKey(sprint, repo, branch string) string {
	return "s:" + sprint + ":branchunit:" + repo + ":" + branch
}

// RefSeenKey is the evaluator's record of one ref's head (ns_ref_seen):
// sha, source (delivery or git), at.
func RefSeenKey(repo, ref string) string {
	return "land:" + repo + ":ref:" + ref
}

// CheckInboundFreshness checks ev:github:consumer:land for pending entries and heartbeat age (§3.2).
// Refuses if the beat is absent, pending > 0, or the beat is older than 10s: a
// missing beat is no evidence the consumer is caught up, so it freezes too.
func CheckInboundFreshness(ctx context.Context, c *redis.Client, now time.Time) (fresh bool, reason string, err error) {
	vals, err := c.HMGet(ctx, InboundConsumerKey, "pending", "at").Result()
	if err != nil {
		return false, "", err
	}
	if len(vals) < 2 || vals[0] == nil || vals[1] == nil {
		return false, "inbound-stale: no beat on " + InboundConsumerKey, nil
	}

	pendingStr, _ := vals[0].(string)
	atStr, _ := vals[1].(string)

	pending, perr := strconv.Atoi(pendingStr)
	atUnix, aerr := strconv.ParseInt(atStr, 10, 64)
	if perr != nil || aerr != nil || atUnix <= 0 {
		return false, fmt.Sprintf("inbound-stale: unreadable beat pending=%q at=%q", pendingStr, atStr), nil
	}
	if pending > 0 {
		return false, fmt.Sprintf("inbound-stale: %d pending", pending), nil
	}

	if atUnix > 100000000000 { // milliseconds
		atUnix = atUnix / 1000
	}
	age := now.Sub(time.Unix(atUnix, 0))
	if age > 10*time.Second {
		return false, fmt.Sprintf("inbound-stale: age %v > 10s", age), nil
	}

	return true, "", nil
}

// CallRefSeen records the head of one ref through ns_ref_seen. source is
// "delivery" (a webhook named it and the mirror was fetched) or "git" (the
// reconcile read the mirror). It returns MISSED, MOVED, SEEN or NEW.
func CallRefSeen(ctx context.Context, c *redis.Client, repo, ref, sha, source string) (string, error) {
	res, err := c.FCall(ctx, "ns_ref_seen", nil, repo, ref, sha, source).StringSlice()
	if err != nil {
		return "", err
	}
	if len(res) < 1 {
		return "", fmt.Errorf("ns_ref_seen: empty reply")
	}
	return res[0], nil
}

// ReconcileMirror compares mirror refs against the heads recorded in Redis
// (§3.2, L32). gitRefs is what git says now (ref -> sha). A ref that moved with
// no delivery naming the new sha writes INBOUND MISSED (ns_ref_seen) and then
// the head from git: the unit on that branch (s:<S>:branchunit) gets the new
// head through ns_unit_head, with files from the mirror diff when mirrorDir
// is set. It returns one INBOUND MISSED line per missed ref, in ref order.
func ReconcileMirror(ctx context.Context, c *redis.Client, sprint, repo, mirrorDir string, gitRefs map[string]string) ([]string, error) {
	refs := make([]string, 0, len(gitRefs))
	for ref := range gitRefs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	var missed []string
	var errs []error
	for _, ref := range refs {
		sha := gitRefs[ref]
		verdict, err := CallRefSeen(ctx, c, repo, ref, sha, "git")
		if err != nil {
			errs = append(errs, fmt.Errorf("ref %s: %w", ref, err))
			continue
		}
		if verdict != "MISSED" {
			continue
		}
		missed = append(missed, fmt.Sprintf("INBOUND MISSED ref=%s sha=%s", ref, sha))
		if _, err := HeadFromGit(ctx, c, sprint, repo, mirrorDir, ref, sha); err != nil {
			errs = append(errs, fmt.Errorf("head from git %s: %w", ref, err))
		}
	}
	return missed, errors.Join(errs...)
}

// HeadFromGit writes sha as the head of the unit whose branch is ref
// (refs/heads/<branch>) through ns_unit_head, keeping the unit's other
// fields. files is the mirror diff base_sha..sha when mirrorDir is set,
// otherwise the recorded files are kept. It returns the unit, or "" when no
// unit of this sprint is on that branch (a base or an unknown branch).
func HeadFromGit(ctx context.Context, c *redis.Client, sprint, repo, mirrorDir, ref, sha string) (string, error) {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	unit, err := c.Get(ctx, BranchUnitKey(sprint, repo, branch)).Result()
	if errors.Is(err, redis.Nil) || unit == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	u, err := c.HGetAll(ctx, UnitKey(sprint, unit)).Result()
	if err != nil {
		return "", err
	}
	if len(u) == 0 || u["head"] == sha {
		return unit, nil
	}
	files := u["files"]
	if mirrorDir != "" && u["base_sha"] != "" {
		diff, err := MirrorDiff(ctx, mirrorDir, u["base_sha"], sha)
		if err != nil {
			return unit, err
		}
		files = strings.Join(diff, ",")
	}
	_, err = CallUnitHead(ctx, c, UnitHeadParams{
		Sprint: sprint, Unit: unit, Repo: u["repo"], Base: u["base"], Branch: u["branch"],
		Head: sha, BaseSHA: u["base_sha"], StackParent: u["stack_parent"], Files: files,
		PathsHash: u["paths_hash"], Security: u["security"], Class: u["class"],
		PR: u["pr"], Author: u["author"],
	})
	return unit, err
}

// MirrorRefs reads the current sha of each ref from the mirror (git only).
// A ref the mirror does not have is left out.
func MirrorRefs(ctx context.Context, mirrorDir string, refs []string) (map[string]string, error) {
	out := map[string]string{}
	if mirrorDir == "" || len(refs) == 0 {
		return out, nil
	}
	args := append([]string{"-C", mirrorDir, "for-each-ref", "--format=%(refname) %(objectname)"}, refs...)
	b, err := gitOut(ctx, args...)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(b, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			out[f[0]] = f[1]
		}
	}
	return out, nil
}

// FetchMirror fetches refs/heads/* only (5.3: never refs/pull/*) into the
// mirror. With branches, only those heads; without, every head, and only when
// the mirror's FETCH_HEAD is older than every (the 60 s reconcile fetch, 3.2),
// so a 1 s evaluator loop does not fetch every second. It reports whether a
// fetch ran.
func FetchMirror(ctx context.Context, mirrorDir string, every time.Duration, branches ...string) (bool, error) {
	if mirrorDir == "" {
		return false, nil
	}
	var specs []string
	if len(branches) > 0 {
		for _, b := range branches {
			specs = append(specs, "+refs/heads/"+b+":refs/heads/"+b)
		}
	} else {
		if st, err := os.Stat(filepath.Join(mirrorDir, "FETCH_HEAD")); err == nil && time.Since(st.ModTime()) < every {
			return false, nil
		}
		specs = []string{"+refs/heads/*:refs/heads/*"}
	}
	args := append([]string{"-C", mirrorDir, "fetch", "--quiet", "origin"}, specs...)
	if _, err := gitOut(ctx, args...); err != nil {
		return false, err
	}
	return true, nil
}

func gitOut(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// MirrorDiff runs git diff --name-only baseSHA..head inside mirrorDir (§3.1).
func MirrorDiff(ctx context.Context, mirrorDir, baseSHA, head string) ([]string, error) {
	if mirrorDir == "" {
		return nil, nil
	}
	out, err := gitOut(ctx, "-C", mirrorDir, "diff", "--name-only", fmt.Sprintf("%s..%s", baseSHA, head))
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}
