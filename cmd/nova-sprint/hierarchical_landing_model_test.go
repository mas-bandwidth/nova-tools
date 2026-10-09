package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/stretchr/testify/require"
)

// HierarchicalLandingSource replays these observations without a Record guard.
// This bridge checks the actual bare destination and actual marker/store writes;
// forge metadata is deliberately insufficient in the first observation.
func TestHierarchicalLandingPromotionSourceBridge(t *testing.T) {
	t.Parallel()
	observed := make([]string, 3)
	t.Cleanup(func() { hierarchySequence(t, "HierarchicalLandingSource.tla", "SourceObserved", observed) })
	for mode := 0; mode < 3; mode++ {
		t.Run(fmt.Sprintf("source_checkpoint_%d", mode), func(t *testing.T) {
			t.Parallel()
			w := newPromoteTwin(t)
			f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
			var frozen string
			f.merged = func(head string) string {
				frozen = w.remote("refs/heads/" + head)
				if mode == 0 {
					return frozen
				}
				return w.mergeOnDev(head, true)
			}
			var forge promoteForge = f
			if mode == 2 {
				forge = &lostCreatePromotionForge{fakeForge: f}
			}
			useForge(t, w, forge)
			ta := promoteApp(t, w)
			code, out, errs := ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
			if mode == 2 {
				require.Equal(t, 1, code, errs)
				saved := w.git(w.dir, "config", "--local", "--get", "promote.cut")
				code, out, errs = ta.do("promote --once --branch sprint/live --poll 1m --repo-dir " + w.dir)
				require.Contains(t, out, "PROMOTE RECOVERED")
				require.Equal(t, saved, frozen)
			}
			if mode == 0 {
				require.Equal(t, 1, code, "%s\n%s", out, errs)
			} else {
				require.Zero(t, code, "%s\n%s", out, errs)
			}
			require.NotEmpty(t, frozen)
			dev := w.remote("refs/heads/dev")
			_, cutErr := gitrun.Run(context.Background(), gitrun.Options{C: w.bare, Env: w.env, OwnRepo: true}, "merge-base", "--is-ancestor", frozen, f.mergedSHA)
			_, mergeErr := gitrun.Run(context.Background(), gitrun.Options{C: w.bare, Env: w.env, OwnRepo: true}, "merge-base", "--is-ancestor", f.mergedSHA, dev)
			marker := w.git(w.dir, "for-each-ref", "--format=%(objectname)", "refs/promoted/last")
			stored := ta.promotedSha()
			observed[mode] = fmt.Sprintf("[dev |-> %s, marker |-> %s, store |-> %s, opens |-> %d, recovered |-> %s]", hierarchyBool(cutErr == nil && mergeErr == nil), hierarchyBool(marker != ""), hierarchyBool(stored != ""), hierarchyOpenCount(f.calls), hierarchyBool(mode == 2))
			t.Logf("HIERARCHY SOURCE RECEIPT %s", observed[mode])
			if mode != 0 {
				require.Equal(t, dev, marker)
				require.Equal(t, dev, stored)
			}
			ta.clean()
		})
	}
}

func hierarchyBool(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

func hierarchyOpenCount(calls []string) int {
	n := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "open ") {
			n++
		}
	}
	return n
}

// HierarchicalLandingChainSource checkpoints come from two actual land passes
// and actual promotion, with ancestor proofs against the bare remote after each.
func TestHierarchicalLandingChainSourceBridge(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	var observed []string
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/parent", "HEAD:refs/heads/sprint/live", "HEAD:refs/heads/dev")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream a --count 1 --one")
	r.ok("add --stream b --count 1 --one")
	r.ok("priority b-1 --critical --reason 'urgent hierarchy child'")
	heads := map[string]string{"a-1": r.head("a-1", "parent", "a.txt", "a\n"), "b-1": r.head("b-1", "parent", "b.txt", "b\n")}
	r.queued(heads, "a-1", "b-1")
	out := r.ok("land --repo-dir " + r.clone + " --base parent --check 'test -f README'")
	require.Contains(t, out, "LAND OK stream=a")
	require.Contains(t, out, "LAND OK stream=b")
	require.Less(t, strings.Index(out, "LAND OK stream=b"), strings.Index(out, "LAND OK stream=a"), "later critical sibling gets the next serial push before earlier normal sibling")
	require.Equal(t, map[string]string{"a-1": "landed/merged", "b-1": "landed/merged"}, r.places("a-1", "b-1"))
	parent := r.git(r.remote, "rev-parse", "parent")
	hierarchyAncestors(t, r, parent, heads["a-1"], heads["b-1"])
	observed = append(observed, hierarchyChainReceipt("parent", `{"a","b"}`, `{"a","b"}`))
	r.ok("add --stream parent --count 1 --one")
	r.git(r.worker, "fetch", "-q", "origin")
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/parent-1", "origin/parent")
	r.queued(map[string]string{"parent-1": parent}, "parent-1")
	out = r.ok("land --repo-dir " + r.clone + " --base sprint/live --check 'test -f a.txt && test -f b.txt'")
	require.Contains(t, out, "LAND OK stream=parent")
	require.Equal(t, map[string]string{"parent-1": "landed/merged"}, r.places("parent-1"))
	sprintTip := r.git(r.remote, "rev-parse", "sprint/live")
	hierarchyAncestors(t, r, sprintTip, heads["a-1"], heads["b-1"], parent)
	observed = append(observed, hierarchyChainReceipt("sprint", `{"a","b","parent"}`, `{"parent"}`))
	w := &promoteTwin{t: t, env: r.env, bare: r.remote, dir: r.clone, peer: r.worker}
	f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
	var cut string
	f.merged = func(head string) string { cut = w.remote("refs/heads/" + head); return w.mergeOnDev(head, true) }
	useForge(t, w, f)
	r.a.after = func(time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- t0; return ch }
	r.ok("promote --once --branch sprint/live --poll 1m --repo-dir " + r.clone)
	dev := w.remote("refs/heads/dev")
	require.NotEmpty(t, cut)
	hierarchyAncestors(t, r, dev, heads["a-1"], heads["b-1"], parent, cut)
	require.Equal(t, dev, r.promotedSha())
	observed = append(observed, hierarchyChainReceipt("dev", `{"a","b","parent","cut"}`, `{"cut"}`))
	hierarchySequence(t, "HierarchicalLandingChainSource.tla", "ChainObserved", observed)
	r.clean()
}

func hierarchyAncestors(t *testing.T, r *landRig, tip string, heads ...string) {
	t.Helper()
	for _, head := range heads {
		r.git(r.remote, "merge-base", "--is-ancestor", head, tip)
	}
}

func hierarchyChainReceipt(destination, contains, claims string) string {
	return fmt.Sprintf("[destination |-> %q, contains |-> %s, claims |-> %s]", destination, contains, claims)
}

func hierarchySequence(t *testing.T, file, name string, observed []string) {
	t.Helper()
	data, err := os.ReadFile("../../tla/" + file)
	require.NoError(t, err)
	parts := strings.SplitN(string(data), name+" == ", 2)
	require.Len(t, parts, 2)
	end := strings.Index(parts[1], ">>")
	require.NotEqual(t, -1, end)
	normalize := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace
	require.Equal(t, normalize(parts[1][:end+2]), normalize("<<"+strings.Join(observed, ",")+">>"), "the complete actual source sequence must equal the model replay inputs")
	t.Logf("HIERARCHY SOURCE SEQUENCE %s <<%s>>", name, strings.Join(observed, ","))
}
