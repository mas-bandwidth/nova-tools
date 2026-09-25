package pr_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
)

// #3156's policy, passed as arguments (the predicates hold no thresholds).
const (
	staleAfter = 3600 * time.Second
	behindN    = 3
)

var gatedTypes = []string{"code"}

func at(sec int64) time.Time { return time.Unix(sec, 0) }

func wantMissing(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, pr.ErrMissing) || err.Error() != "MISSING "+field {
		t.Fatalf("err = %v, want MISSING %s wrapping pr.ErrMissing", err, field)
	}
}

// wantKept asserts every predicate answers "keep" for an invalid Input: the
// unit is never reap-eligible when a field is MISSING.
func wantKept(t *testing.T, in pr.Input, landed []pr.Input) {
	t.Helper()
	if in.Err() == nil {
		t.Fatalf("Input.Err() = nil for an invalid record")
	}
	if !pr.ApprovedAtHead(in) {
		t.Errorf("ApprovedAtHead = false; an invalid record must keep X1")
	}
	if d := pr.StaleFor(in, at(1<<40)); d != 0 {
		t.Errorf("StaleFor = %v, want 0", d)
	}
	if pr.SupersededBy(in, landed) {
		t.Errorf("SupersededBy = true for an invalid record")
	}
	if n := pr.Behind(in, landed); n != 0 {
		t.Errorf("Behind = %d, want 0", n)
	}
	if pr.Gated(in, gatedTypes) {
		t.Errorf("Gated = true for an invalid record")
	}
}

func TestPRRecordReapFields(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	const unit = "u-seven"
	h.pushCard("card-seven", "internal/x a/b", "code")
	h.mustHead(unit, h1, "card-seven")

	t.Run("seven_fields_present", func(t *testing.T) {
		h := h.at(t)
		rec := h.rec(unit)
		for _, f := range pr.ReapFields {
			if _, ok := rec[f]; !ok {
				t.Fatalf("field %s absent after one bound ns_unit_head: %v", f, rec)
			}
		}
		want := map[string]string{
			"head": h1, "paths": `["a/b","internal/x"]`, "card_type": "code",
			"last_read_at": "", "approve_head": "", "merged_at": "",
		}
		for f, v := range want {
			if rec[f] != v {
				t.Errorf("%s = %q, want %q", f, rec[f], v)
			}
		}
		cardCut, _ := h.c.HGet(h.ctx, h.cardKey("card-seven"), "cut_at").Result()
		if rec["cut_at"] == "" || rec["cut_at"] != cardCut {
			t.Errorf("cut_at = %q, card cut_at = %q", rec["cut_at"], cardCut)
		}
		atoi(t, rec["cut_at"])
	})

	t.Run("merged_at_empty_valid", func(t *testing.T) {
		h := h.at(t)
		in, err := pr.Inputs(h.rec(unit))
		if err != nil {
			t.Fatalf("Inputs: %v", err)
		}
		if in.Merged || in.MergedAt != 0 {
			t.Fatalf("Merged = %v MergedAt = %d, want not merged", in.Merged, in.MergedAt)
		}
	})

	t.Run("bound_unreviewed_valid", func(t *testing.T) {
		h := h.at(t)
		in, err := pr.Inputs(h.rec(unit))
		if err != nil {
			t.Fatalf("Inputs: %v", err)
		}
		if in.Read || in.Approved {
			t.Fatalf("Read = %v Approved = %v, want neither", in.Read, in.Approved)
		}
		if d := pr.StaleFor(in, at(in.CutAt+3600)); d < staleAfter {
			t.Errorf("StaleFor(cut_at+3600) = %v, want stale (>= %v)", d, staleAfter)
		}
		if d := pr.StaleFor(in, at(in.CutAt+3599)); d >= staleAfter {
			t.Errorf("StaleFor(cut_at+3599) = %v, want not stale", d)
		}
		if pr.ApprovedAtHead(in) {
			t.Errorf("ApprovedAtHead = true with no APPROVE")
		}
	})

	t.Run("sentinel_not_reset", func(t *testing.T) {
		h := h.at(t)
		h.mustRead(unit, "stella", h1, "APPROVE", "read")
		before, _ := h.field(unit, "last_read_at")
		if before == "" {
			t.Fatalf("a counted read left last_read_at empty")
		}
		h.mustHead(unit, h1, "card-seven")
		h.mustHead(unit, h2, "card-seven")
		after, _ := h.field(unit, "last_read_at")
		if after != before {
			t.Fatalf("last_read_at %q -> %q after ns_unit_head replays", before, after)
		}
	})

	t.Run("deleted_field_missing", func(t *testing.T) {
		h := h.at(t)
		if err := h.c.HDel(h.ctx, land.UnitKey(h.S, unit), "last_read_at").Err(); err != nil {
			t.Fatal(err)
		}
		h.mustHead(unit, h2, "card-seven")
		if _, ok := h.field(unit, "last_read_at"); ok {
			t.Fatalf("ns_unit_head re-created a deleted last_read_at")
		}
		in, err := pr.Inputs(h.rec(unit))
		wantMissing(t, err, "last_read_at")
		wantKept(t, in, nil)
	})

	t.Run("no_card_missing", func(t *testing.T) {
		h := h.at(t)
		h.mustHead("u-nocard", h1, "")
		if _, ok := h.field("u-nocard", "paths"); ok {
			t.Fatalf("paths written without a card")
		}
		_, err := pr.Inputs(h.rec("u-nocard"))
		wantMissing(t, err, "paths")
	})

	t.Run("read_before_unit_nounit", func(t *testing.T) {
		h := h.at(t)
		err := h.read("u-absent", "stella", h1, "APPROVE", "read")
		if !errors.Is(err, land.ErrNoUnit) {
			t.Fatalf("ns_read on an absent unit: %v, want NOUNIT", err)
		}
		if n, _ := h.c.Exists(h.ctx, land.UnitKey(h.S, "u-absent")).Result(); n != 0 {
			t.Fatalf("EXISTS s:<S>:u:u-absent = %d, want 0", n)
		}
		if n, _ := h.c.Exists(h.ctx, land.ReadKey(h.S, "u-absent", "stella")).Result(); n != 1 {
			t.Fatalf("the read record was not written")
		}
	})
}

func TestPRFieldSources(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	srv := repoServer(t)

	t.Run("head_is_unit_head", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-head", "a/b", "code")
		h.mustHead("u-head", h1, "c-head")
		h.mustHead("u-head", h2, "c-head")
		if in := h.inputs("u-head"); in.Head != h2 {
			t.Fatalf("Inputs.Head = %q, want the unit head %q", in.Head, h2)
		}
	})

	t.Run("paths_write_once", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-pw", "a/b internal/x", "code")
		h.pushCard("c-pw-same", "./internal/x/ a//b", "code")
		h.mustHead("u-pw", h1, "c-pw")
		before, _ := h.field("u-pw", "paths")
		h.mustHead("u-pw", h2, "c-pw")
		if _, err := h.head("u-pw", h2, "c-pw-same", ""); err != nil && strings.Contains(err.Error(), "paths-changed") {
			t.Fatalf("the same canonical paths reported a change: %v", err)
		}
		if after, _ := h.field("u-pw", "paths"); after != before || before != `["a/b","internal/x"]` {
			t.Fatalf("paths %q -> %q", before, after)
		}
	})

	t.Run("paths_changed_unresolved", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-pc", "a/b", "code")
		h.pushCard("c-pc-other", "c/d", "code")
		h.mustHead("u-pc", h1, "c-pc")
		before := h.rec("u-pc")
		seq, err := h.head("u-pc", h1, "c-pc-other", "")
		if !errors.Is(err, land.ErrUnresolved) || !strings.Contains(err.Error(), "UNRESOLVED paths-changed") {
			t.Fatalf("err = %v, want UNRESOLVED paths-changed", err)
		}
		if after, _ := h.field("u-pc", "paths"); after != before["paths"] {
			t.Fatalf("paths %q -> %q", before["paths"], after)
		}
		field := "u-pc:paths-changed:" + strconv.FormatInt(seq, 10)
		if ok, _ := h.c.HExists(h.ctx, "s:"+h.S+":unresolved", field).Result(); !ok {
			t.Fatalf("s:<S>:unresolved has no %s", field)
		}
	})

	t.Run("card_type_from_type_line", func(t *testing.T) {
		h := h.at(t)
		h.pushCardBody(srv, "c-type", map[string]string{"TYPE": "code"})
		h.mustHead("u-type", h1, "c-type")
		if v, _ := h.field("u-type", "card_type"); v != "code" {
			t.Fatalf("card_type = %q, want code", v)
		}
	})

	t.Run("card_type_absent_missing", func(t *testing.T) {
		h := h.at(t)
		h.pushCardBody(srv, "c-notype", nil)
		h.mustHead("u-notype", h1, "c-notype")
		if _, ok := h.field("u-notype", "card_type"); ok {
			t.Fatalf("card_type written for a card with no TYPE: line")
		}
		_, err := pr.Inputs(h.rec("u-notype"))
		wantMissing(t, err, "card_type")
	})

	t.Run("type_distinct_from_kind", func(t *testing.T) {
		h := h.at(t)
		h.pushCardBody(srv, "c-kind", map[string]string{"KIND": "script", "TYPE": "code"})
		got, _ := h.c.HMGet(h.ctx, h.cardKey("c-kind"), "kind", "card_type").Result()
		if got[0] != "script" || got[1] != "code" {
			t.Fatalf("KIND: script TYPE: code stored kind=%v card_type=%v", got[0], got[1])
		}
		kind, _ := h.c.HGet(h.ctx, h.cardKey("c-notype"), "kind").Result()
		if kind != "model" {
			t.Fatalf("no KIND: stored kind=%q, want model", kind)
		}
		if ok, _ := h.c.HExists(h.ctx, h.cardKey("c-notype"), "card_type").Result(); ok {
			t.Fatalf("no TYPE: stored a card_type")
		}
	})

	t.Run("cut_at_from_push_time", func(t *testing.T) {
		h := h.at(t)
		t0 := h.redisNow().Unix()
		h.pushCard("c-cut", "a/b", "code")
		t1 := h.redisNow().Unix()
		cardCut, _ := h.c.HGet(h.ctx, h.cardKey("c-cut"), "cut_at").Result()
		if n := atoi(t, cardCut); n < t0 || n > t1 {
			t.Fatalf("card cut_at %d not Redis TIME at push [%d, %d]", n, t0, t1)
		}
		h.mustHead("u-cut", h1, "c-cut")
		first, _ := h.field("u-cut", "cut_at")
		h.mustHead("u-cut", h2, "c-cut")
		second, _ := h.field("u-cut", "cut_at")
		if first != cardCut || second != first {
			t.Fatalf("unit cut_at %q then %q, card %q", first, second, cardCut)
		}
	})

	t.Run("last_read_first_eval_empty", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-lr", "a/b", "code")
		h.mustHead("u-lr", h1, "c-lr")
		if v, ok := h.field("u-lr", "last_read_at"); !ok || v != "" {
			t.Fatalf("last_read_at after the first ns_unit_head = %q present=%v, want present-empty", v, ok)
		}
		t0 := h.redisNow().Unix()
		h.mustRead("u-lr", "stella", h1, "HOLD", "read")
		v, _ := h.field("u-lr", "last_read_at")
		if n := atoi(t, v); n < t0 {
			t.Fatalf("last_read_at %d before the read at %d", n, t0)
		}
	})

	t.Run("last_read_monotonic", func(t *testing.T) {
		h := h.at(t)
		h.set("u-lr", "last_read_at", "9999999999")
		h.mustRead("u-lr", "emma", h1, "APPROVE", "read")
		if v, _ := h.field("u-lr", "last_read_at"); v != "9999999999" {
			t.Fatalf("a later read lowered last_read_at to %q", v)
		}
		h.set("u-lr", "last_read_at", "1")
		h.mustRead("u-lr", "emma", h1, "APPROVE", "read")
		if v, _ := h.field("u-lr", "last_read_at"); atoi(t, v) <= 1 {
			t.Fatalf("a newer read did not raise last_read_at: %q", v)
		}
	})

	t.Run("jev_read_excluded", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-jev", "a/b", "code")
		h.mustHead("u-jev", h1, "c-jev")
		h.mustRead("u-jev", "jev", h1, "APPROVE", "read")
		rec := h.rec("u-jev")
		if rec["last_read_at"] != "" || rec["approve_head"] != "" {
			t.Fatalf("a jev read wrote last_read_at=%q approve_head=%q", rec["last_read_at"], rec["approve_head"])
		}
	})

	t.Run("ci_note_excluded", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-ci", "a/b", "code")
		h.mustHead("u-ci", h1, "c-ci")
		h.mustRead("u-ci", "stella", h1, "APPROVE", "ci")
		h.mustRead("u-ci", "emma", h1, "NOTE", "read")
		rec := h.rec("u-ci")
		if rec["last_read_at"] != "" || rec["approve_head"] != "" {
			t.Fatalf("a ci read or a NOTE wrote last_read_at=%q approve_head=%q", rec["last_read_at"], rec["approve_head"])
		}
	})

	t.Run("approve_at_head_not_displaced", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-ap", "a/b", "code")
		h.mustHead("u-ap", h1, "c-ap")
		h.mustHead("u-ap", h2, "c-ap")
		h.mustRead("u-ap", "stella", h2, "APPROVE", "read")
		before := h.rec("u-ap")
		if before["approve_head"] != h2 {
			t.Fatalf("APPROVE at the current head stored approve_head=%q", before["approve_head"])
		}
		h.mustRead("u-ap", "emma", h1, "APPROVE", "read")
		after := h.rec("u-ap")
		if after["approve_head"] != before["approve_head"] || after["approve_seq"] != before["approve_seq"] {
			t.Fatalf("an old-head APPROVE displaced approve_head %q/%q -> %q/%q",
				before["approve_head"], before["approve_seq"], after["approve_head"], after["approve_seq"])
		}
	})

	t.Run("approve_head_follows_new_head", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-af", "a/b", "code")
		h.mustHead("u-af", h1, "c-af")
		h.mustRead("u-af", "stella", h1, "APPROVE", "read")
		if v, _ := h.field("u-af", "approve_head"); v != h1 {
			t.Fatalf("approve_head = %q, want %q", v, h1)
		}
		h.mustHead("u-af", h2, "c-af")
		h.mustRead("u-af", "stella", h2, "APPROVE", "read")
		if v, _ := h.field("u-af", "approve_head"); v != h2 {
			t.Fatalf("approve_head = %q, want %q", v, h2)
		}
	})

	t.Run("merged_at_set_once", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-m", "a/b", "code")
		h.mustHead("u-m", h1, "c-m")
		l := h.land("u-m", h1)
		landedAt, _ := h.c.HGet(h.ctx, land.BatchKey(testRepo, testBase, l.batch), "landed_at").Result()
		before := h.rec("u-m")
		if want := strconv.FormatInt(atoi(t, landedAt)/1000, 10); before["merged_at"] != want {
			t.Fatalf("merged_at = %q, want floor(landed_at/1000) = %s", before["merged_at"], want)
		}
		if before["merge_sha"] != l.mergeSHA {
			t.Fatalf("merge_sha = %q, want %q", before["merge_sha"], l.mergeSHA)
		}
		res, err := land.CallLand(h.ctx, h.c, h.S, testRepo, testBase, l.batch, h.lease, l.trainHead, l.mergeSHA, "1")
		if err != nil || res != "ALREADY" {
			t.Fatalf("replay: %q %v, want ALREADY", res, err)
		}
		after := h.rec("u-m")
		if after["merged_at"] != before["merged_at"] || after["merge_sha"] != before["merge_sha"] {
			t.Fatalf("replay changed merged_at/merge_sha %q/%q -> %q/%q",
				before["merged_at"], before["merge_sha"], after["merged_at"], after["merge_sha"])
		}
	})
}

func TestPRPathsCanon(t *testing.T) {
	t.Parallel()

	t.Run("space_in_json_form", func(t *testing.T) {
		got, err := pr.CanonPaths(`["a b/c", "x"]`)
		if err != nil || !reflect.DeepEqual(got, []string{"a b/c", "x"}) {
			t.Fatalf("CanonPaths = %q, %v", got, err)
		}
		if enc := pr.EncodePaths(got); enc != `["a b/c","x"]` {
			t.Fatalf("EncodePaths = %s", enc)
		}
		if tok, _ := pr.CanonPaths("a b/c"); !reflect.DeepEqual(tok, []string{"a", "b/c"}) {
			t.Fatalf("token form = %q, want two entries", tok)
		}
		h := newHarness(t)
		h.pushCard("c-sp", `["a b/c"]`, "code")
		h.mustHead("u-sp", h1, "c-sp")
		if v, _ := h.field("u-sp", "paths"); v != `["a b/c"]` {
			t.Fatalf("stored paths = %s", v)
		}
	})

	t.Run("component_boundary", func(t *testing.T) {
		cases := []struct {
			a, b string
			want bool
		}{
			{"a/b", "a/bc", false},
			{"a/b", "a/b/c", true},
			{"a/b/c", "a/b", true},
			{"a/b", "a/b", true},
			{"a/bc", "a/b/c", false},
		}
		for _, c := range cases {
			if got := pr.Overlap([]string{c.a}, []string{c.b}); got != c.want {
				t.Errorf("Overlap(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
			}
		}
	})

	t.Run("normalize", func(t *testing.T) {
		for _, line := range []string{"./a//b/", `["./a//b/"]`, "a/b ./a/b", "a/b, a/b/"} {
			got, err := pr.CanonPaths(line)
			if err != nil || !reflect.DeepEqual(got, []string{"a/b"}) {
				t.Errorf("CanonPaths(%q) = %q, %v, want [a/b]", line, got, err)
			}
		}
		if got, _ := pr.CanonPaths("z a"); !reflect.DeepEqual(got, []string{"a", "z"}) {
			t.Errorf("not sorted: %q", got)
		}
	})

	t.Run("refuse_dotdot_absolute", func(t *testing.T) {
		h := newHarness(t)
		for _, c := range []struct{ label, paths string }{{"c-dd", "a/../b"}, {"c-abs", "/etc/x"}} {
			h.pushCard(c.label, c.paths, "code")
			unit := "u-" + c.label
			seq, err := h.head(unit, h1, c.label, "")
			if !errors.Is(err, pr.ErrRefused) || seq == 0 {
				t.Fatalf("%s: seq %d err %v, want the unit written and REFUSED paths", c.paths, seq, err)
			}
			if _, ok := h.field(unit, "paths"); ok {
				t.Fatalf("%s: a refused PATHS was stored", c.paths)
			}
			_, ierr := pr.Inputs(h.rec(unit))
			wantMissing(t, ierr, "paths")
		}
	})

	t.Run("refuse_error_text", func(t *testing.T) {
		cases := map[string]string{
			"a/../b":    "REFUSED paths dotdot a/../b",
			"/abs":      "REFUSED paths absolute /abs",
			`["a", ""]`: `REFUSED paths empty ""`,
			`["a"`:      `REFUSED paths json ["a"`,
			"./":        "REFUSED paths empty ./",
		}
		for line, want := range cases {
			_, err := pr.CanonPaths(line)
			if err == nil || err.Error() != want || !errors.Is(err, pr.ErrRefused) {
				t.Errorf("CanonPaths(%q) err = %v, want %q", line, err, want)
			}
		}
	})
}

func TestPRReapBehaviour(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	t.Run("x1_head_equals_approve_head", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-x1", "a/b", "code")
		h.mustHead("u-x1", h1, "c-x1")
		h.mustRead("u-x1", "stella", h1, "APPROVE", "read")
		if !pr.ApprovedAtHead(h.inputs("u-x1")) {
			t.Fatalf("APPROVE at head does not protect (X1)")
		}
		h.set("u-x1", "approve_head", h2)
		if pr.ApprovedAtHead(h.inputs("u-x1")) {
			t.Fatalf("approve_head at another sha still protects")
		}
	})

	t.Run("e1_straddle", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-e1", "a/b", "code")
		h.mustHead("u-e1", h1, "c-e1")
		h.mustRead("u-e1", "stella", h1, "HOLD", "read")
		in := h.inputs("u-e1")
		lr := in.LastReadAt
		if !in.Read || pr.StaleFor(in, at(lr+3599)) >= staleAfter || pr.StaleFor(in, at(lr+3600)) < staleAfter {
			t.Fatalf("E1 does not straddle at last_read_at %d", lr)
		}
		h.set("u-e1", "last_read_at", strconv.FormatInt(lr+1, 10))
		if pr.StaleFor(h.inputs("u-e1"), at(lr+3600)) >= staleAfter {
			t.Fatalf("raising last_read_at did not flip E1")
		}
	})

	t.Run("cut_at_fallback", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-cf", "a/b", "code")
		h.mustHead("u-cf", h1, "c-cf")
		in := h.inputs("u-cf")
		cut := in.CutAt
		if in.Read || pr.StaleFor(in, at(cut+3599)) >= staleAfter || pr.StaleFor(in, at(cut+3600)) < staleAfter {
			t.Fatalf("E1 with no read does not straddle at cut_at %d", cut)
		}
		h.set("u-cf", "last_read_at", strconv.FormatInt(cut+3600, 10))
		if pr.StaleFor(h.inputs("u-cf"), at(cut+3600)) >= staleAfter {
			t.Fatalf("a read at now did not flip E1")
		}
	})

	// P is cut an hour before L merges: cut_at and merged_at are both Redis
	// TIME seconds, so the fixture moves P's cut_at back instead of sleeping.
	loadLanded := func(t *testing.T, units ...string) []pr.Input {
		t.Helper()
		out := make([]pr.Input, 0, len(units))
		for _, u := range units {
			out = append(out, h.at(t).inputs(u))
		}
		return out
	}

	t.Run("e2_overlap_after_cut", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-p2", "a/b", "code")
		h.mustHead("u-p2", h1, "c-p2")
		h.pushCard("c-l2", "a/b/c", "code")
		h.mustHead("u-l2", h2, "c-l2")
		h.land("u-l2", h2)
		merged := h.inputs("u-l2").MergedAt
		h.set("u-p2", "cut_at", strconv.FormatInt(merged-3600, 10))
		p := h.inputs("u-p2")
		if !pr.SupersededBy(p, loadLanded(t, "u-l2")) {
			t.Fatalf("overlapping landed L merged after P's cut does not supersede")
		}
		lpaths, _ := h.field("u-l2", "paths")
		h.set("u-l2", "paths", `["a/bc"]`)
		if pr.SupersededBy(p, loadLanded(t, "u-l2")) {
			t.Fatalf("a/bc supersedes a/b")
		}
		h.set("u-l2", "paths", lpaths)
		h.set("u-l2", "merged_at", strconv.FormatInt(p.CutAt, 10))
		if pr.SupersededBy(p, loadLanded(t, "u-l2")) {
			t.Fatalf("L merged at P's cut_at supersedes")
		}
		h.set("u-l2", "merged_at", strconv.FormatInt(merged, 10))
	})

	t.Run("e3_behind_gated", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-p3", "e3/p", "code")
		h.mustHead("u-p3", h1, "c-p3")
		var landed []string
		for i := 0; i < 4; i++ {
			u := "u-e3-" + strconv.Itoa(i)
			h.pushCard("c-e3-"+strconv.Itoa(i), "e3/l"+strconv.Itoa(i), "code")
			h.mustHead(u, h2, "c-e3-"+strconv.Itoa(i))
			h.land(u, h2)
			landed = append(landed, u)
		}
		h.set("u-p3", "cut_at", strconv.FormatInt(h.inputs(landed[0]).MergedAt-3600, 10))
		p := h.inputs("u-p3")
		all := loadLanded(t, landed...)
		if n := pr.Behind(p, all); n != 4 || n <= behindN || !pr.Gated(p, gatedTypes) {
			t.Fatalf("4 landed after cut_at, code: Behind = %d Gated = %v", n, pr.Gated(p, gatedTypes))
		}
		if n := pr.Behind(p, all[:3]); n > behindN {
			t.Fatalf("3 landed is behind: %d", n)
		}
		h.set("u-p3", "card_type", "docs")
		if pr.Gated(h.inputs("u-p3"), gatedTypes) {
			t.Fatalf("card_type docs is gated")
		}
	})

	t.Run("missing_refuses", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-mr", "a/b/c/d", "code")
		h.mustHead("u-mr", h1, "c-mr")
		h.mustRead("u-mr", "stella", h1, "HOLD", "read")
		h.set("u-mr", "cut_at", "1")
		landed := loadLanded(t, "u-l2")
		in := h.inputs("u-mr")
		if pr.ApprovedAtHead(in) || pr.StaleFor(in, at(1<<40)) == 0 || !pr.SupersededBy(in, landed) || pr.Behind(in, landed) == 0 || !pr.Gated(in, gatedTypes) {
			t.Fatalf("the base record must be eligible on every rule before a field is deleted")
		}
		for _, f := range pr.ReapFields {
			v, _ := h.field("u-mr", f)
			if err := h.c.HDel(h.ctx, land.UnitKey(h.S, "u-mr"), f).Err(); err != nil {
				t.Fatal(err)
			}
			in, err := pr.Inputs(h.rec("u-mr"))
			wantMissing(t, err, f)
			wantMissing(t, in.Err(), f)
			wantKept(t, in, landed)
			h.set("u-mr", f, v)
		}
		if _, err := pr.Inputs(h.rec("u-mr")); err != nil {
			t.Fatalf("restored record: %v", err)
		}
	})
}

func TestPRResolve(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.pushCard("c-r", "a/b", "code")
	if _, err := h.head("u-r", h1, "c-r", "42"); err != nil {
		t.Fatal(err)
	}

	t.Run("prunit_resolves", func(t *testing.T) {
		h := h.at(t)
		unit, rec, err := pr.Resolve(h.ctx, h.c, h.S, testRepo, 42)
		if err != nil || unit != "u-r" {
			t.Fatalf("Resolve(42) = %q, %v", unit, err)
		}
		if _, err := pr.Inputs(rec); err != nil {
			t.Fatalf("Inputs(resolved record): %v", err)
		}
	})

	t.Run("no_prunit_missing", func(t *testing.T) {
		h := h.at(t)
		_, _, err := pr.Resolve(h.ctx, h.c, h.S, testRepo, 43)
		wantMissing(t, err, "s:"+h.S+":prunit:nova-tools:43")
	})

	t.Run("load_units_index", func(t *testing.T) {
		h := h.at(t)
		h.pushCard("c-r2", "c/d", "code")
		h.mustHead("u-r2", h2, "c-r2")
		lines := h.monitor()
		if err := h.c.Echo(h.ctx, "reap-3091-begin").Err(); err != nil {
			t.Fatal(err)
		}
		until(t, lines, "reap-3091-begin")
		units, err := pr.LoadUnits(h.ctx, h.c, h.S)
		if err != nil {
			t.Fatalf("LoadUnits: %v", err)
		}
		if err := h.c.Echo(h.ctx, "reap-3091-end").Err(); err != nil {
			t.Fatal(err)
		}
		seen := until(t, lines, "reap-3091-end")
		if len(units) != 2 {
			t.Fatalf("LoadUnits = %d units, want 2", len(units))
		}
		for u, rec := range units {
			if _, err := pr.Inputs(rec); err != nil {
				t.Fatalf("Inputs(%s): %v", u, err)
			}
		}
		count := map[string]int{}
		for _, line := range seen {
			name := commandName(line)
			switch name {
			case "SMEMBERS", "HGETALL":
				count[name]++
			case "HELLO", "CLIENT":
				// go-redis connection setup on a fresh pool connection
			default:
				t.Errorf("LoadUnits sent %s: %s", name, line)
			}
		}
		if count["SMEMBERS"] != 1 || count["HGETALL"] != 2 {
			t.Fatalf("LoadUnits sent SMEMBERS x%d HGETALL x%d, want 1 and 2", count["SMEMBERS"], count["HGETALL"])
		}
	})
}
