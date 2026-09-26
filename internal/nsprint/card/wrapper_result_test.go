package card_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// newGoOrigin is a bare origin whose dev holds base.txt and a Go module with
// one passing and one failing test: the card's TEST line runs against it.
func newGoOrigin(t *testing.T) (origin, base string) {
	t.Helper()
	dir := t.TempDir()
	origin = filepath.Join(dir, "origin.git")
	gitIn(t, dir, "init", "-q", "--bare", origin)
	seed := filepath.Join(dir, "seed")
	gitIn(t, dir, "init", "-q", seed)
	for name, body := range map[string]string{
		"base.txt":      "base\n",
		"go.mod":        "module example.com/cardcheck\n\ngo 1.20\n",
		"check_test.go": "package cardcheck\n\nimport \"testing\"\n\nfunc TestPass(t *testing.T) {}\n\nfunc TestFail(t *testing.T) { t.Fatal(\"the card's check fails\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(seed, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-q", "-m", "base")
	gitIn(t, seed, "push", "-q", origin, "HEAD:refs/heads/dev")
	gitIn(t, dir, "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/dev")
	return origin, gitIn(t, seed, "rev-parse", "HEAD")
}

// TestWrapperFillsTheResult is the DONE-WHEN control of nova-tools#3689. A
// fake native whose model writes only the two lines (and one that writes the
// BRANCH the card told it, `rowan/<label>`, tonight's quack cards) both end
// DONE with a valid record in Redis: the wrapper wrote SCHEMA, KIND, ATTEMPT,
// REPO, BRANCH = nova/<S>/<label>-a<n>, PATHS from the diff (base.txt), CHECK
// from its own run of the card's TEST line (pass for TestPass, fail for
// TestFail), GREEN, Gates and Left owed; the result hash also holds the model's
// two lines and note, outcome, reason, wall, commit and the provider facts from
// the START line; wrapper.line carries the RESULT line; and card show prints
// the record. RED AT cfe7adbf: the wrong-BRANCH card is `BRANCH contradictory`
// and the two-line card `SCHEMA missing`, both valid=0.
func TestWrapperFillsTheResult(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, test, check string
	}{
		{"two-lines", "native-two", ". TestPass", "pass"},
		{"wrong-branch", "native-wrongbranch", ". TestPass", "pass"},
		{"check-fails", "native-two", ". TestFail", "fail"},
		{"no-test-line", "native-two", "", "not-run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			origin, base := newGoOrigin(t)
			id := card.Identity{Sprint: "flash-0925", Label: "quack-" + tc.name, BaseSHA: base[:8], Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, strings.Repeat("e", 32))
			seedCard(t, ctx, client, id, "dealt", token)
			fields := map[string]string{"kind": typedrec.KindFix, "repo": "mas-bandwidth/nova-tools", "base_sha": base}
			if tc.test != "" {
				fields["test"] = tc.test
			}
			if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), fields).Err(); err != nil {
				t.Fatal(err)
			}
			gate := filepath.Join(t.TempDir(), "gate")
			if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeGateEnv, gate)
			t.Setenv(fakeOriginEnv, origin)
			t.Setenv(fakeSlotEnv, filepath.Join(t.TempDir(), "slot"))
			h := newHarnessRun(t, id, self)
			rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
			if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
				t.Fatalf("report %s why=%q; want DONE ended", rep.Line(), rep.Why)
			}
			if strings.Contains(rep.Why, "wrapper bug") {
				t.Fatalf("report why=%q: a wrapper-written field contradicted the card", rep.Why)
			}
			branch := card.WrapperBranch(id.Sprint, id.Label, 1)
			hs := hashOf(t, ctx, client, id.Sprint, id.Label)
			if hs["state"] != "ended" || hs["outcome"] != "DONE" || len(hs["pushed_sha"]) != 40 {
				t.Fatalf("card hash %v, want ended DONE with a pushed_sha", hs)
			}
			res, err := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)+":result:a1").Result()
			if err != nil {
				t.Fatal(err)
			}
			line1 := "RESULT: " + id.Sprint + "/" + id.Label + "/1 sha=000000000000"
			want := map[string]string{
				"valid": "1", "schema": "v2", "kind": "fix", "line1": line1,
				"c_schema": "v2", "c_kind": "fix", "c_attempt": "1", "c_repo": "mas-bandwidth/nova-tools",
				"c_branch": branch, "c_paths": "base.txt", "c_check": tc.check, "c_red": "not-run",
				"w_synth": "1", "w_line2": "DONE", "w_result": "present", "w_check": tc.check,
				"w_outcome": "DONE", "w_reason": "done", "w_exit": "0", "w_commit": "COMMITTED",
				"w_pushed_sha": hs["pushed_sha"], "w_branch": branch, "w_paths": "base.txt",
				"w_tier": "flash", "w_route": "opencode-flash", "w_model": "opencode/kimi-k3", "w_key": "OPENCODE_API_KEY",
			}
			if tc.mode == "native-two" {
				want["w_note"] = "the retry path is still owed"
			}
			for k, v := range want {
				if res[k] != v {
					t.Errorf("result.%s = %q, want %q", k, res[k], v)
				}
			}
			for _, k := range []string{"w_wall_ms", "w_wall_max_s", "raw_sha256"} {
				if res[k] == "" {
					t.Errorf("result.%s is empty", k)
				}
			}
			if res["wrapper_bug"] != "" {
				t.Errorf("wrapper_bug %q on a clean record", res["wrapper_bug"])
			}
			switch tc.check {
			case "pass":
				if !strings.Contains(res["c_green"], "go test . -run ^TestPass$ -count=1: pass") {
					t.Errorf("c_green %q, want the wrapper's passing run", res["c_green"])
				}
			case "fail":
				if res["c_green"] != "" || !strings.Contains(res["raw_bytes"], "go test . -run ^TestFail$ -count=1: fail") {
					t.Errorf("a failing check: c_green %q, record:\n%s", res["c_green"], res["raw_bytes"])
				}
			case "not-run":
				if !strings.Contains(res["raw_bytes"], "- TEST: not-run (no TEST line on the card)") {
					t.Errorf("no TEST line: record\n%s", res["raw_bytes"])
				}
			}
			if strings.Contains(res["raw_bytes"], "rowan/") {
				t.Errorf("the model's BRANCH survived into the record:\n%s", res["raw_bytes"])
			}
			if tc.mode == "native-two" && !strings.Contains(res["raw_bytes"], "## Left owed\n- the retry path is still owed\n") {
				t.Errorf("the note is not the Left owed row:\n%s", res["raw_bytes"])
			}

			line, _ := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			if !strings.Contains(string(line), "outcome=DONE") || !strings.Contains(string(line), `result="`+line1+`"`) {
				t.Errorf("wrapper.line %q; want outcome=DONE and result %q", line, line1)
			}
			if model, err := os.ReadFile(filepath.Join(h.results, card.ModelResultName)); err != nil || !strings.HasPrefix(string(model), line1+"\nDONE\n") {
				t.Errorf("the model's own RESULT.md is not kept as %s: %q %v", card.ModelResultName, model, err)
			}

			cardFields, resultFields, err := card.ShowRecord(ctx, client, id.Sprint, id.Label)
			if err != nil {
				t.Fatal(err)
			}
			shown := strings.Join(card.ShowLines(cardFields, resultFields), "\n")
			for _, w := range []string{"card.state ended", "card.outcome DONE", "result.valid 1", "result.c_branch " + branch,
				"result.c_check " + tc.check, "result.w_line2 DONE", "result.w_model opencode/kimi-k3", "result.w_commit COMMITTED"} {
				if !strings.Contains(shown, w+"\n") && !strings.HasSuffix(shown, w) {
					t.Errorf("card show lacks %q:\n%s", w, shown)
				}
			}
			if strings.Contains(shown, token) {
				t.Fatalf("card show printed the token")
			}
			h.assertNoJobDir()
		})
	}
}

// TestTestCommandIsTheDeclaredGrammar: only `<package> <TestName>` runs; the
// wrapper never assumes a package or runs a free command.
func TestTestCommandIsTheDeclaredGrammar(t *testing.T) {
	t.Parallel()

	for test, want := range map[string]string{
		"./internal/docs TestEveryTopLevelDocLinkResolves": "go test ./internal/docs -run ^TestEveryTopLevelDocLinkResolves$ -count=1",
		"./internal/pulse/ TestStale":                      "go test ./internal/pulse/ -run ^TestStale$ -count=1",
		". TestPass":                                       "go test . -run ^TestPass$ -count=1",
		"":                                                 "",
		"none":                                             "",
		"rm -rf /":                                         "",
		"../x TestY":                                       "",
		"./a/../../b TestY":                                "",
		"./x TestY; echo":                                  "",
		"./x NotATest":                                     "",
		"./.. TestY":                                       "",
	} {
		argv, why := card.TestCommand(test)
		if got := strings.Join(argv, " "); got != want || (want == "" && why == "") {
			t.Errorf("TestCommand(%q) = %q (why %q), want %q", test, got, why, want)
		}
	}
}

// TestWrapperFieldContradictionIsALoggedBug is #3689 item 3: a record the
// wrapper wrote (w_synth=1) whose BRANCH disagrees with the card's can only be
// a wrapper bug, so ns_card_result logs it (wrapper_bug on the result hash and
// a card-result log entry) and keeps the record valid; the same disagreement
// in a record the wrapper did not write is still contradictory.
func TestWrapperFieldContradictionIsALoggedBug(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, client := newSprint(t)
	const line1 = "RESULT: bug-card sha=0123456789ab nova-tools fix: a card"
	for _, tc := range []struct {
		label string
		synth bool
		valid string
	}{
		{"bug-synth", true, "1"},
		{"bug-model", false, "0"},
	} {
		id := card.Identity{Sprint: "flash-0925", Label: tc.label, BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
		token := attemptToken(1, strings.Repeat("f", 32))
		seedCard(t, ctx, client, id, "running", token)
		if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), "kind", "fix", "repo", "mas-bandwidth/nova-tools", "branch", card.WrapperBranch(id.Sprint, id.Label, 1)).Err(); err != nil {
			t.Fatal(err)
		}
		raw := typedrec.Synthesize([]byte(line1+"\nDONE\n"), typedrec.WrapperFacts{
			Kind: "fix", Attempt: 1, Repo: "mas-bandwidth/nova-tools", Branch: "nova/elsewhere/x-a1", Paths: []string{"a.go"}, Red: "not-run",
		})
		res := typedrec.ParseResult(raw, typedrec.ParseOptions{ExpectedKind: "fix", ExpectedAttempt: 1})
		if !res.Valid {
			t.Fatalf("%s: the parse is invalid before Redis: %s %s", tc.label, res.Field, res.Defect)
		}
		var facts []card.Fact
		if tc.synth {
			facts = append(facts, card.Fact{Name: "w_synth", Value: "1"})
		}
		ledger := &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}
		if code, err := ledger.Result(ctx, res, t.TempDir(), facts...); err != nil || code != 0 {
			t.Fatalf("%s: Result code=%d err=%v", tc.label, code, err)
		}
		h := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)+":result:a1").Val()
		if h["valid"] != tc.valid {
			t.Fatalf("%s: valid=%q field=%s defect=%s, want %s", tc.label, h["valid"], h["field"], h["defect"], tc.valid)
		}
		if tc.synth && h["wrapper_bug"] != "BRANCH contradictory" {
			t.Fatalf("%s: wrapper_bug %q, want the logged BRANCH bug", tc.label, h["wrapper_bug"])
		}
		if !tc.synth && (h["field"] != "BRANCH" || h["defect"] != "contradictory") {
			t.Fatalf("%s: field=%s defect=%s, want BRANCH contradictory", tc.label, h["field"], h["defect"])
		}
	}
	logged := 0
	for _, m := range client.XRange(ctx, card.LogKey("flash-0925"), "-", "+").Val() {
		if m.Values["reason"] == "wrapper-bug" && m.Values["id"] == "bug-synth" && m.Values["evidence"] == "BRANCH contradictory" {
			logged++
		}
	}
	if logged != 1 {
		t.Fatalf("%d wrapper-bug log entries for bug-synth, want 1", logged)
	}
}

// TestCardPushStoresTest (#3689): lint reads TEST: into the card hash's test
// field, which the wrapper runs at card end; an absent line stores nothing.
func TestCardPushStoresTest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	for _, tc := range []struct {
		label string
		lines []string
		want  string
	}{
		{"test-named", []string{"TEST: ./internal/docs TestEveryTopLevelDocLinkResolves"}, "./internal/docs TestEveryTopLevelDocLinkResolves"},
		{"test-absent", nil, ""},
	} {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = tc.label
		if res := card.Push(ctx, client, sprint, withHeader(f, tc.lines...)); res.Code != 0 {
			t.Fatalf("%s: exit %d stderr %q, want exit 0", tc.label, res.Code, res.Stderr)
		}
		if got := client.HGet(ctx, keyCard(tc.label), "test").Val(); got != tc.want {
			t.Fatalf("%s: test %q, want %q", tc.label, got, tc.want)
		}
	}
}
