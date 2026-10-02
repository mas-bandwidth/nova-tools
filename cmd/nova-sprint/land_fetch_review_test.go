package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Cold review of 94bd7706 (land: fetch the base and the batch's heads by id).
// The full fetch of origin brought every card branch, so a head was found
// however origin let it be asked for; fetched by id, a head is found only when
// it is a whole commit id and origin serves an object it did not advertise.
// Both tests pass on 94bd7706^ and fail on 94bd7706.

// landsBoth is a two-card stream whose heads are pushed on their branches and
// finished at headOf(full sha), landed with land; its exit, stdout, stderr.
func landsBoth(t *testing.T, r *landRig, headOf func(string) string) (int, string, string) {
	t.Helper()
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2"} {
		heads[id] = headOf(r.head(id, "main", id+".txt", id+"\n"))
	}
	r.queued(heads, "s1-1", "s1-2")
	return r.do("land --repo-dir " + r.clone + " --base main")
}

// shaRE admits an abbreviated head ("hex, abbreviated or whole"), and a card
// finished at one landed while the fetch brought every branch. git fetches by
// id only a whole id: the one fetch fails, the fallback's per-head fetch fails
// the same way ("couldn't find remote ref"), and the card is blamed as missing
// from origin, its stream stopped on a conflict, though origin holds the commit
// on the card's branch.
func TestLandLandsACardFinishedAtAnAbbreviatedHead(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	code, out, errs := landsBoth(t, r, func(full string) string { return full[:12] })
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.NotContains(t, errs, "is missing: origin holds no such commit")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
}

// A remote that serves only what it advertises (git protocol v0 without
// uploadpack.allowReachableSHA1InWant, git's default there) refuses a want by an
// id that is no ref's tip with "not our ref", which notOnOrigin reads as "origin
// holds no such commit". A card's branch moved past its finished head (a later
// push to the branch) is such a head: the old full fetch brought the branch and
// the head with it; fetched by id, the card is blamed as missing.
func TestLandLandsAHeadBehindItsBranchTipFromARemoteThatServesOnlyAdvertisedRefs(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.env = append(r.env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=0")
	r.a.gitEnv = r.env
	r.ok("add --stream s1 --count 1")
	heads := map[string]string{"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n")}
	r.queued(heads, "s1-1")
	// the branch moves on past the head the card finished at
	r.git(r.worker, "switch", "-q", "sprint/s1-1")
	r.commit("later.txt", "later\n", "later on the card's branch")
	r.git(r.worker, "push", "-q", "origin", "sprint/s1-1")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.NotContains(t, errs, "is missing: origin holds no such commit")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
}
