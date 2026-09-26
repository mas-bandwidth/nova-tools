package typedrec_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

func TestEveryConsumerRefusesMissingFieldByName(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	t.Run("cut", func(t *testing.T) {
		kinds := []string{"fix", "recut", "port", "docs-guard", "report", "read"}
		for _, kind := range kinds {
			tmpl := typedrec.Template(kind)
			wantFields := typedrec.Fields(kind)
			// verify template field lines equal typedrec.Fields(kind)
			for _, f := range wantFields {
				key, _, _ := strings.Cut(f, ":")
				if !strings.Contains(tmpl, key+":") {
					t.Fatalf("template for %s missing field %s", kind, key)
				}
			}

			// exemplar is valid
			ex := typedrec.Exemplar(kind)
			res := typedrec.ParseResult([]byte(ex))
			if !res.Valid {
				t.Fatalf("exemplar for %s invalid: field=%s defect=%s", kind, res.Field, res.Defect)
			}

			// The nova-pulse card-template check that followed left with internal/pulse (deleted 2026-09-25, #3969).
		}
	})

	t.Run("report", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatalf("load fn: %v", err)
		}
		st := store.New(client)

		sprint := "s1"
		validLabel := "c-report-valid"
		label := "c-report"

		// A valid report exemplar and the same record without PROBES:.
		reportEx := typedrec.Exemplar("report")
		var lines []string
		for _, l := range strings.Split(reportEx, "\n") {
			if strings.HasPrefix(l, "PROBES:") {
				continue
			}
			lines = append(lines, l)
		}
		docs := map[string]string{validLabel: reportEx, label: strings.Join(lines, "\n")}

		// The consumer under test is the card wrapper's end step: it reads
		// RESULT.md from the results dir, parses it with typedrec and records
		// it through ns_card_result. The test writes only the card hash and the
		// raw file; the result hash is the consumer's own write.
		resDirs := map[string]string{}
		for _, l := range []string{validLabel, label} {
			dir := filepath.Join(t.TempDir(), l)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte(docs[l]), 0o644); err != nil {
				t.Fatal(err)
			}
			resDirs[l] = dir
			if err := client.HSet(ctx, "s:"+sprint+":card:"+l,
				"state", "running", "kind", "report", "bench", "b1", "attempt", "1",
				"repo", "mas-bandwidth/nova-tools", "token", "tok-"+l,
				"identity", sprint+"/"+l+"/09fbedc9/b1/1").Err(); err != nil {
				t.Fatal(err)
			}
		}

		record := func(l string) typedrec.Result {
			t.Helper()
			ledger := &card.RedisLedger{Store: st, Sprint: sprint, Label: l, Token: "tok-" + l}
			res, found, code, err := card.RecordResult(ctx, ledger, resDirs[l], 1)
			if err != nil || !found || code != 0 {
				t.Fatalf("RecordResult %s: found=%v code=%d err=%v", l, found, code, err)
			}
			return res
		}
		if res := record(validLabel); !res.Valid {
			t.Fatalf("valid report exemplar parsed invalid: field=%s defect=%s", res.Field, res.Defect)
		}
		res := record(label)
		if res.Valid || res.Field != "PROBES" || res.Defect != "missing" {
			t.Fatalf("got valid=%v field=%s defect=%s, want PROBES missing", res.Valid, res.Field, res.Defect)
		}
		for l, want := range map[string]string{validLabel: "1", label: "0"} {
			h, err := client.HGetAll(ctx, "s:"+sprint+":card:"+l+":result:a1").Result()
			if err != nil || h["valid"] != want || h["by"] != "card-wrapper" {
				t.Fatalf("%s result hash = %v (err %v), want valid=%s by=card-wrapper", l, h, err, want)
			}
		}

		// The raw file goes; the typed record in Redis is what show reads.
		if err := os.Remove(filepath.Join(resDirs[label], "RESULT.md")); err != nil {
			t.Fatal(err)
		}

		root := findRoot(t)
		bin := filepath.Join(t.TempDir(), "nova-sprint")
		cmdBuild := exec.Command("go", "build", "-o", bin, "./cmd/nova-sprint")
		cmdBuild.Dir = root
		cmdBuild.Env = goenv.Clean(os.Environ())
		if out, err := cmdBuild.CombinedOutput(); err != nil {
			t.Fatalf("build nova-sprint: %v\n%s", err, out)
		}

		cmdShow := exec.Command(bin, "result", "show", "--sprint", sprint, "--redis", addr, label)
		out, err := cmdShow.CombinedOutput()
		if err != nil {
			t.Fatalf("result show failed: %v\n%s", err, out)
		}
		want := "valid=0 field=PROBES defect=missing"
		if !strings.Contains(string(out), want) {
			t.Fatalf("result show output %q does not contain %q", out, want)
		}
	})

	t.Run("post-reads", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatalf("load fn: %v", err)
		}
		st := store.New(client)

		sprint := "s1"
		validLabel := "read-valid"
		invalidLabel := "read-invalid"

		readEx := typedrec.Exemplar("read")
		resValid := typedrec.ParseResult([]byte(readEx))
		if !resValid.Valid {
			t.Fatalf("valid read exemplar failed: %v", resValid.Defect)
		}

		// Valid post-reads writes readev with counted=0
		if err := consume.PostReads(ctx, st, sprint, validLabel, resValid); err != nil {
			t.Fatalf("PostReads valid failed: %v", err)
		}

		repo := resValid.Claims["REPO"]
		pr := resValid.Claims["PR"]
		head := resValid.Claims["HEAD"]
		key := "s:" + sprint + ":readev:" + repo + ":" + pr
		field := validLabel + "@" + head
		val, err := client.HGet(ctx, key, field).Result()
		if err != nil {
			t.Fatalf("hget readev: %v", err)
		}
		if !strings.Contains(val, "counted=0") {
			t.Fatalf("readev val %q does not contain counted=0", val)
		}

		// Invalid without HEAD:
		var noHeadLines []string
		for _, l := range strings.Split(readEx, "\n") {
			if strings.HasPrefix(l, "HEAD:") {
				continue
			}
			noHeadLines = append(noHeadLines, l)
		}
		resInvalid := typedrec.ParseResult([]byte(strings.Join(noHeadLines, "\n")))
		err = consume.PostReads(ctx, st, sprint, invalidLabel, resInvalid)
		if err == nil {
			t.Fatal("expected PostReads to refuse missing HEAD")
		}
		if !strings.Contains(err.Error(), "field=HEAD") {
			t.Fatalf("error %q does not name field=HEAD", err.Error())
		}

		// Check no entry written for invalidLabel
		allFields, _ := client.HGetAll(ctx, key).Result()
		for k := range allFields {
			if strings.HasPrefix(k, invalidLabel+"@") {
				t.Fatalf("unexpected readev entry for invalid label: %s", k)
			}
		}
	})

	t.Run("gate", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatalf("load fn: %v", err)
		}
		st := store.New(client)

		sprint := "s1"
		label := "gate-c1"
		attempt := "1"

		// Seed sprint policy and friends
		_ = client.HSet(ctx, "s:"+sprint, "status", "open").Err()
		_ = client.HSet(ctx, "s:"+sprint+":policy", "readers", "1", "readers_security", "2", "security_paths", "").Err()
		_ = client.SAdd(ctx, "friends", "ctl-a").Err()
		_ = client.HSet(ctx, "friend:ctl-a:desired", "slots", "4", "paused", "0").Err()
		_ = client.HSet(ctx, "friend:ctl-a:beat", "harness", "ctl", "at", "1").Err()

		// Setup card in harvested state
		head := "1111111111111111111111111111111111111111"
		cardKey := "s:" + sprint + ":card:" + label
		_ = client.HSet(ctx, cardKey,
			"kind", "fix",
			"repo", "mas-bandwidth/nova-tools",
			"base", "dev",
			"base_sha", "09fbedc9",
			"state", "harvested",
			"attempt", attempt,
			"pr", "10",
			"head", head,
			"pushed_sha", head,
			"author", "author1",
			"priority", "5",
		).Err()
		_ = client.SAdd(ctx, "s:"+sprint+":idx:card:harvested", label).Err()

		// Result hash missing CHECK (valid=0, field=CHECK, defect=missing)
		resKey := "s:" + sprint + ":card:" + label + ":result:a" + attempt
		_ = client.HSet(ctx, resKey,
			"valid", "0",
			"field", "CHECK",
			"defect", "missing",
			"line", "6",
		).Err()

		// Log entry for harvested transition
		logKey := "s:" + sprint + ":log"
		msgID, err := client.XAdd(ctx, &redis.XAddArgs{
			Stream: logKey,
			Values: []any{
				"kind", "card",
				"id", label,
				"from", "ended",
				"to", "harvested",
				"attempt", attempt,
				"token_sha", "abcdefabcdef",
				"actor", "card-harvest",
				"reason", "harvested",
				"evidence", "https://example.com/mas-bandwidth/nova-tools/pull/10",
				"idem", "harvest:" + label,
				"at", "1234567890",
			},
		}).Result()
		if err != nil {
			t.Fatalf("xadd: %v", err)
		}

		// Run okfriend consumer pass
		okfriend := &consume.OkFriend{
			Store:    st,
			Sprint:   sprint,
			Consumer: "worker1",
			Actor:    "ok-to-friend",
		}
		if err := okfriend.Start(ctx); err != nil {
			t.Fatalf("okfriend start: %v", err)
		}
		n, err := okfriend.Pass(ctx)
		if err != nil {
			t.Fatalf("okfriend pass: %v", err)
		}
		if n != 1 {
			t.Fatalf("handled %d events, want 1", n)
		}

		// Verify no friend read tasks created
		tasks, _ := client.SMembers(ctx, "s:"+sprint+":idx:task:open").Result()
		if len(tasks) != 0 {
			t.Fatalf("expected 0 friend read tasks, got %v", tasks)
		}

		// Verify skip line in idem names CHECK
		idemKey := "s:" + sprint + ":idem"
		skipVal, err := client.HGet(ctx, idemKey, consume.GroupOkFriend+":"+msgID).Result()
		if err != nil {
			t.Fatalf("hget skip: %v", err)
		}
		if !strings.Contains(skipVal, "CHECK") {
			t.Fatalf("skip val %q does not name CHECK", skipVal)
		}
	})

	// nova-merge (#2506 part B): the lander's typed APPROVE goes through
	// typedrec.ParseDisposition. An APPROVE with no head= is refused by field
	// name instead of quietly binding to the current head, and yields no
	// approve verdict; the same line with its head approves.
	t.Run("nova-merge", func(t *testing.T) {
		const head = "0123456789abcdef0123456789abcdef01234567"
		rs, err := merge.ParseReviewers(strings.NewReader("who\tlogins\tmay-hold\nstella\tstella-bot\tyes\n"))
		if err != nil {
			t.Fatalf("reviewers: %v", err)
		}
		valid := "DISPOSITION who=stella head=" + head + " verdict=APPROVE"
		noHead := "DISPOSITION who=stella verdict=APPROVE"

		c, isLine := typedrec.ParseDisposition(valid)
		if !isLine || !c.Valid || c.Head != head || c.Verdict != "APPROVE" || !c.Whole {
			t.Fatalf("valid APPROVE: isLine=%v claim=%+v", isLine, c)
		}
		v, ok := merge.ParseComment(1, "stella-bot", valid, "2026-09-24T00:00:00Z", rs, "rowan", head, false)
		if !ok || v.Word != "approve" || v.Who != "stella" || v.Head != head {
			t.Fatalf("valid APPROVE: want an approve verdict at %s, got ok=%v %+v", head, ok, v)
		}

		c, isLine = typedrec.ParseDisposition(noHead)
		if !isLine || c.Valid || c.Field != "head" || c.Defect != typedrec.DefectMissing {
			t.Fatalf("APPROVE without head: want refused field=head defect=missing, got isLine=%v claim=%+v", isLine, c)
		}
		refusal := c.Refusal()
		if !strings.Contains(refusal, "field=head defect=missing") {
			t.Fatalf("refusal line %q does not name field=head defect=missing", refusal)
		}
		t.Log(refusal)
		v, ok = merge.ParseComment(2, "stella-bot", noHead, "2026-09-24T00:00:00Z", rs, "rowan", head, false)
		if ok && v.Word == "approve" {
			t.Fatalf("APPROVE without head yielded an approve verdict: %+v", v)
		}
	})
}

// resultRedis is a Redis with the nsprint functions loaded.
func resultRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	return store.New(client), client
}

// withLine1 is doc with its line 1 replaced.
func withLine1(doc, line1 string) string {
	_, rest, _ := strings.Cut(doc, "\n")
	return line1 + "\n" + rest
}

func line1Of(doc string) string {
	l, _, _ := strings.Cut(doc, "\n")
	return l
}

// TestUnknownKindRefusedOnEveryPath is #3497 Stella 4 item 1 (KIND): every
// reader of a card or RESULT KIND checks it against the declared six. An
// unknown KIND is refused by the parser (in the file and as the card's
// expectation), by ValidateResultV2, by the cutter lint, and by
// ns_card_result even when the caller's parse claims valid.
func TestUnknownKindRefusedOnEveryPath(t *testing.T) {
	t.Parallel()

	report := typedrec.Exemplar(typedrec.KindReport)
	bogus := strings.Replace(report, "KIND: report", "KIND: bogus", 1)
	if bogus == report {
		t.Fatal("report exemplar has no KIND: report line")
	}

	if res := typedrec.ParseResult([]byte(bogus)); res.Valid || res.Field != "KIND" || res.Defect != typedrec.DefectMalformed {
		t.Fatalf("ParseResult KIND: bogus: valid=%v field=%s defect=%s, want KIND malformed", res.Valid, res.Field, res.Defect)
	}
	if res := typedrec.ParseResult([]byte(report), typedrec.ParseOptions{ExpectedKind: "bogus"}); res.Valid || res.Field != "KIND" || res.Defect != typedrec.DefectMalformed {
		t.Fatalf("ParseResult expected kind bogus: valid=%v field=%s defect=%s, want KIND malformed", res.Valid, res.Field, res.Defect)
	}
	if _, err := typedrec.ValidateResultV2(bogus, ""); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 KIND: bogus: err=%v, want a KIND refusal", err)
	}
	if _, err := typedrec.ValidateResultV2(report, "bogus"); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 card kind bogus: err=%v, want a KIND refusal", err)
	}
	if _, err := typedrec.ValidateResultV2(typedrec.Exemplar(typedrec.KindFix), typedrec.KindReport); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateResultV2 fix record on a report card: err=%v, want a KIND contradiction", err)
	}

	// The nova-pulse card KIND check that followed left with internal/pulse (deleted 2026-09-25, #3969).

	// ns_card_result judges KIND itself: a caller claiming valid with a kind
	// outside the six is persisted invalid.
	st, client := resultRedis(t)
	ctx := context.Background()
	const sprint, label = "s1", "k-bogus"
	if err := client.HSet(ctx, "s:"+sprint+":card:"+label, "state", "running", "kind", "model",
		"attempt", "1", "token", "tok").Err(); err != nil {
		t.Fatal(err)
	}
	ledger := &card.RedisLedger{Store: st, Sprint: sprint, Label: label, Token: "tok"}
	forged := typedrec.ParseResult([]byte(report))
	forged.Kind = "bogus"
	if code, err := ledger.Result(ctx, forged, t.TempDir()); err != nil || code != 0 {
		t.Fatalf("Result: code=%d err=%v", code, err)
	}
	h := client.HGetAll(ctx, "s:"+sprint+":card:"+label+":result:a1").Val()
	if h["valid"] != "0" || h["field"] != "KIND" || h["defect"] != "malformed" {
		t.Fatalf("result hash %v, want valid=0 field=KIND defect=malformed", h)
	}
}

// TestRecordResultEnforcesCardKindAndLine1 is #3497 Stella 4 item 1: the
// production record path (card.RecordResult, what the wrapper's finish
// calls) parses against the card's trusted KIND and contract line, and
// ns_card_result cross-checks both in Redis. A different-kind result and a
// line 1 with a forged suffix are each persisted invalid with the
// contradictory field named; the exact report record stays valid.
func TestRecordResultEnforcesCardKindAndLine1(t *testing.T) {
	t.Parallel()

	st, client := resultRedis(t)
	ctx := context.Background()
	const sprint = "s1"
	report := typedrec.Exemplar(typedrec.KindReport)
	contract := line1Of(report)
	seed := func(label string) {
		t.Helper()
		if err := client.HSet(ctx, "s:"+sprint+":card:"+label, "state", "running", "kind", typedrec.KindReport,
			"contract", contract, "attempt", "1", "token", "tok-"+label).Err(); err != nil {
			t.Fatal(err)
		}
	}
	hashOf := func(label string) map[string]string {
		return client.HGetAll(ctx, "s:"+sprint+":card:"+label+":result:a1").Val()
	}

	cases := []struct {
		label, doc, field, defect string
	}{
		{"k-ok", report, "", ""},
		{"k-other-kind", withLine1(typedrec.Exemplar(typedrec.KindFix), contract), "KIND", typedrec.DefectContradictory},
		{"k-line1-suffix", withLine1(report, contract+" forged"), "line 1", typedrec.DefectContradictory},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			seed(tc.label)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte(tc.doc), 0o644); err != nil {
				t.Fatal(err)
			}
			ledger := &card.RedisLedger{Store: st, Sprint: sprint, Label: tc.label, Token: "tok-" + tc.label}
			res, found, code, err := card.RecordResult(ctx, ledger, dir, 1)
			if err != nil || !found || code != 0 {
				t.Fatalf("RecordResult: found=%v code=%d err=%v", found, code, err)
			}
			h := hashOf(tc.label)
			if tc.field == "" {
				if !res.Valid || h["valid"] != "1" {
					t.Fatalf("control: parse valid=%v field=%s defect=%s, hash %v; want valid", res.Valid, res.Field, res.Defect, h)
				}
				return
			}
			if res.Valid || res.Field != tc.field || res.Defect != tc.defect {
				t.Fatalf("parse valid=%v field=%s defect=%s, want %s %s", res.Valid, res.Field, res.Defect, tc.field, tc.defect)
			}
			if h["valid"] != "0" || h["field"] != tc.field || h["defect"] != tc.defect {
				t.Fatalf("result hash %v, want valid=0 field=%s defect=%s", h, tc.field, tc.defect)
			}
		})
	}

	// Redis alone: a caller that skips the trusted facts and claims valid is
	// still judged against the card hash.
	for _, tc := range []struct {
		label, kind, line1, field string
	}{
		{"r-other-kind", typedrec.KindFix, contract, "KIND"},
		{"r-line1-suffix", typedrec.KindReport, contract + " forged", "line 1"},
	} {
		seed(tc.label)
		forged := typedrec.ParseResult([]byte(report))
		forged.Kind, forged.Line1 = tc.kind, tc.line1
		ledger := &card.RedisLedger{Store: st, Sprint: sprint, Label: tc.label, Token: "tok-" + tc.label}
		if code, err := ledger.Result(ctx, forged, t.TempDir()); err != nil || code != 0 {
			t.Fatalf("%s: Result code=%d err=%v", tc.label, code, err)
		}
		if h := hashOf(tc.label); h["valid"] != "0" || h["field"] != tc.field || h["defect"] != "contradictory" {
			t.Fatalf("%s: result hash %v, want valid=0 field=%s defect=contradictory", tc.label, h, tc.field)
		}
	}
}
