package typedrec_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

func TestEveryConsumerRefusesMissingFieldByName(t *testing.T) {
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

			// ValidateCardV2 refuses template minus its first required field with that field named
			// First required field key in Fields(kind) is "SCHEMA"
			firstFieldKey, _, _ := strings.Cut(wantFields[0], ":")
			var filtered []string
			for _, line := range strings.Split(tmpl, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), firstFieldKey+":") {
					continue
				}
				filtered = append(filtered, line)
			}
			badTmpl := strings.Join(filtered, "\n")
			card, err := pulse.RenderCardV2(pulse.CardV2Input{
				Kind:         kind,
				Number:       400,
				Repo:         "mas-bandwidth/nova-tools",
				Title:        "test card " + kind,
				Branch:       "worker/test-" + kind,
				Base:         "dev",
				Location:     "foo.go:1",
				TestPackage:  "./pkg",
				TestFunction: "TestFoo",
				TestCommand:  "go test ./pkg -run TestFoo",
				Paths:        "foo.go",
				Symbol:       "TestFoo",
				RedWhen:      "fails",
			})
			if err != nil {
				t.Fatalf("RenderCardV2 failed: %v", err)
			}
			cardBad := strings.Replace(card, tmpl, badTmpl, 1)
			err = pulse.ValidateCardV2(cardBad)
			if err == nil {
				t.Fatalf("expected ValidateCardV2 to refuse template for %s without %s", kind, firstFieldKey)
			}
			if !strings.Contains(err.Error(), firstFieldKey) {
				t.Fatalf("ValidateCardV2 error %q does not name missing field %s", err.Error(), firstFieldKey)
			}
		}
	})

	t.Run("harvest", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatalf("load fn: %v", err)
		}
		st := store.New(client)

		sprint := "s1"
		bench := "b1"
		validLabel := "fix-valid"
		invalidLabel := "fix-invalid"

		// Set up beat for bench
		if err := client.HSet(ctx, "bench:"+bench+":beat", "host", "localhost", "user", "glenn").Err(); err != nil {
			t.Fatalf("set beat: %v", err)
		}

		tmpDir := t.TempDir()
		validResDir := filepath.Join(tmpDir, "valid-res")
		invalidResDir := filepath.Join(tmpDir, "invalid-res")
		_ = os.MkdirAll(validResDir, 0o755)
		_ = os.MkdirAll(invalidResDir, 0o755)

		validContent := typedrec.Exemplar("fix")
		var invalidLines []string
		for _, l := range strings.Split(validContent, "\n") {
			if strings.HasPrefix(l, "CHECK:") {
				continue
			}
			invalidLines = append(invalidLines, l)
		}
		invalidContent := strings.Join(invalidLines, "\n")

		validResFile := filepath.Join(validResDir, "RESULT.md")
		invalidResFile := filepath.Join(invalidResDir, "RESULT.md")
		if err := os.WriteFile(validResFile, []byte(validContent), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(invalidResFile, []byte(invalidContent), 0o644); err != nil {
			t.Fatal(err)
		}

		// Setup both cards in Redis
		for _, label := range []string{validLabel, invalidLabel} {
			resDir := validResDir
			if label == invalidLabel {
				resDir = invalidResDir
			}
			cardKey := "s:" + sprint + ":card:" + label
			_ = client.HSet(ctx, cardKey,
				"state", "ended",
				"outcome", "DONE",
				"reason", "done",
				"kind", "fix",
				"bench", bench,
				"repo", "mas-bandwidth/nova-tools",
				"base", "dev",
				"base_sha", "09fbedc9",
				"attempt", "1",
				"pushed_sha", "1111111111111111111111111111111111111111",
				"identity", sprint+"/"+label+"/09fbedc9/"+bench+"/1",
				"results", resDir,
				"token", "tok-"+label,
				"token_sha", "abcdefabcdef",
			).Err()
			_ = client.SAdd(ctx, "s:"+sprint+":idx:card:ended", label).Err()
			_ = client.SAdd(ctx, "s:"+sprint+":bench:"+bench+":ended", label).Err()
		}

		// Record results in Redis
		parsedValid := typedrec.ParseResult([]byte(validContent))
		parsedInvalid := typedrec.ParseResult([]byte(invalidContent))

		// Write result hashes via ns_card_result
		argsValid := []any{sprint, validLabel, "1", "tok-" + validLabel, parsedValid.Schema, parsedValid.Kind, "1", "", "", "0", parsedValid.RawSHA256, string(parsedValid.RawBytes), validResDir, "c_check", "pass", "c_repo", "mas-bandwidth/nova-tools", "c_branch", "nova/" + sprint + "/" + validLabel + "-a1"}
		argsInvalid := []any{sprint, invalidLabel, "1", "tok-" + invalidLabel, parsedInvalid.Schema, parsedInvalid.Kind, "0", parsedInvalid.Field, parsedInvalid.Defect, strconv.Itoa(parsedInvalid.Line), parsedInvalid.RawSHA256, string(parsedInvalid.RawBytes), invalidResDir, "c_repo", "mas-bandwidth/nova-tools", "c_branch", "nova/" + sprint + "/" + invalidLabel + "-a1"}

		repValid, errValid := client.FCall(ctx, "ns_card_result", nil, argsValid...).Text()
		repInvalid, errInvalid := client.FCall(ctx, "ns_card_result", nil, argsInvalid...).Text()
		if errValid != nil || errInvalid != nil {
			t.Fatalf("ns_card_result failed: valid=%v invalid=%v", errValid, errInvalid)
		}
		if !strings.HasPrefix(repValid, "0|OK|") || !strings.HasPrefix(repInvalid, "0|OK|") {
			t.Fatalf("ns_card_result bad reply: valid=%s invalid=%s", repValid, repInvalid)
		}

		// Intercept stdout to check HARVEST-REFUSED print
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		mockF := &mockForge{}
		mockP := &mockPusher{}
		_ = harvest.Run(ctx, st, harvest.Options{
			Sprint:   sprint,
			Benches:  []string{bench},
			Forge:    mockF,
			Pusher:   mockP,
			Instance: "inst1",
		})

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		printed := buf.String()

		wantRefuse := fmt.Sprintf("HARVEST-REFUSED %s %s field=CHECK defect=missing", sprint, invalidLabel)
		if !strings.Contains(printed, wantRefuse) {
			t.Fatalf("stdout = %q, want to contain %q", printed, wantRefuse)
		}

		// Verify fix-invalid state in Redis is refused
		invalidState, _ := client.HGet(ctx, "s:"+sprint+":card:"+invalidLabel, "state").Result()
		if invalidState != "refused" {
			t.Fatalf("invalid card state = %q, want refused", invalidState)
		}

		// Verify fix-invalid/RESULT.md is byte-identical to original
		gotRaw, err := os.ReadFile(invalidResFile)
		if err != nil || string(gotRaw) != invalidContent {
			t.Fatalf("invalid card raw file changed: %v", err)
		}

		// Verify valid sibling was harvested
		validState, _ := client.HGet(ctx, "s:"+sprint+":card:"+validLabel, "state").Result()
		if validState != "harvested" {
			t.Fatalf("valid card state = %q, want harvested", validState)
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
}

type mockForge struct{}

func (f *mockForge) FindOpenPR(ctx context.Context, repo, branch string) (harvest.PR, bool, error) {
	return harvest.PR{Number: 42, Head: "1111111111111111111111111111111111111111"}, true, nil
}
func (f *mockForge) OpenPR(ctx context.Context, repo, branch, base, title, body string) (harvest.PR, error) {
	return harvest.PR{Number: 42, Head: "1111111111111111111111111111111111111111"}, nil
}
func (f *mockForge) ReadPR(ctx context.Context, repo string, number int) (harvest.PR, error) {
	return harvest.PR{Number: number, Head: "1111111111111111111111111111111111111111"}, nil
}

type mockPusher struct{}

func (p *mockPusher) Push(ctx context.Context, bench harvest.BenchInfo, card harvest.Card) error {
	return nil
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

	cardText, err := pulse.RenderCardV2(pulse.CardV2Input{
		Kind: typedrec.KindFix, Number: 401, Repo: "mas-bandwidth/nova-tools", Title: "kind card",
		Branch: "worker/kind", Base: "dev", Location: "foo.go:1", TestPackage: "./pkg",
		TestFunction: "TestFoo", TestCommand: "go test ./pkg -run TestFoo", Paths: "foo.go",
		Symbol: "TestFoo", RedWhen: "fails",
	})
	if err != nil {
		t.Fatalf("RenderCardV2: %v", err)
	}
	if err := pulse.ValidateCardV2(cardText); err != nil {
		t.Fatalf("control: ValidateCardV2 refused the rendered fix card: %v", err)
	}
	badCard := strings.Replace(cardText, "KIND: fix", "KIND: bogus", -1)
	if err := pulse.ValidateCardV2(badCard); err == nil || !strings.Contains(err.Error(), "KIND") {
		t.Fatalf("ValidateCardV2 KIND: bogus: err=%v, want a KIND refusal", err)
	}

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

// failingRecorder is a Redis ledger whose result record fails.
type failingRecorder struct{ *card.RedisLedger }

func (failingRecorder) Result(context.Context, typedrec.Result, string) (int, error) {
	return card.WrapperExitRedis, errors.New("record failed")
}

// TestWrapperRecordFailureIsNotHarvestDue is #3497 Stella 4 item 2: a
// harness that exits 0 with an unreadable RESULT.md, or whose result record
// fails, ends FAILED other and is never offered by ns_harvest_due; the same
// run with a readable, recorded RESULT.md ends DONE and is due.
func TestWrapperRecordFailureIsNotHarvestDue(t *testing.T) {
	st, client := resultRedis(t)
	ctx := context.Background()
	const sprint, bench = "s-wrap", "wrap-bench"

	root := t.TempDir()
	harness := filepath.Join(root, "harness.sh")
	script := "#!/bin/sh\nif [ \"$TYPEDREC_HARNESS\" = unreadable ]; then mkdir -p \"$NOVA_CARD_OUT/RESULT.md\"; else cp \"$TYPEDREC_RESULT\" \"$NOVA_CARD_OUT/RESULT.md\"; fi\n"
	if err := os.WriteFile(harness, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	never := make(chan time.Time)
	cases := []struct {
		mode    string
		outcome string
		due     bool
	}{
		{"valid", "DONE", true},
		{"unreadable", "FAILED", false},
		{"recorder-fails", "FAILED", false},
	}
	for i, tc := range cases {
		label := "card-" + tc.mode
		id := card.Identity{Sprint: sprint, Label: label, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
		token := fmt.Sprintf("1.%032x", i+1)
		if err := client.HSet(ctx, card.CardKey(sprint, label), map[string]string{
			"state": "dealt", "attempt": "1", "token": token, "token_sha": card.TokenSHA(token),
			"identity": id.String(), "bench": bench, "base_sha": id.BaseSHA, "kind": typedrec.KindReport,
		}).Err(); err != nil {
			t.Fatal(err)
		}
		// The report record names the attempt's own branch, so the only
		// thing that can keep the valid case from harvest is the record path.
		report := strings.Replace(typedrec.Exemplar(typedrec.KindReport), "BRANCH: worker/report-throughput",
			"BRANCH: "+card.WrapperBranch(sprint, label, 1), 1)
		resultFile := filepath.Join(root, label+".RESULT.md")
		if err := os.WriteFile(resultFile, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TYPEDREC_RESULT", resultFile)
		t.Setenv("TYPEDREC_HARNESS", tc.mode)
		cfg := card.WrapperConfig{
			Sprint: sprint, Label: label, Attempt: 1, Bench: bench, Harness: harness,
			JobsRoot: filepath.Join(root, "jobs"), ResultsRoot: filepath.Join(root, "results"),
			Clock: 45 * time.Minute, BeatEvery: time.Minute,
			After: func(time.Duration) <-chan time.Time { return never },
			Tick:  func(time.Duration) (<-chan time.Time, func()) { return never, func() {} },
		}
		var ledger card.WrapperLedger = &card.RedisLedger{Store: st, Sprint: sprint, Label: label, Token: token}
		if tc.mode == "recorder-fails" {
			ledger = failingRecorder{ledger.(*card.RedisLedger)}
		}
		rep := card.RunWrapper(ctx, cfg, ledger)
		if rep.Code != card.WrapperExitEnded || rep.Outcome != tc.outcome {
			t.Fatalf("%s: %s why=%q; want outcome=%s code=0", tc.mode, rep.Line(), rep.Why, tc.outcome)
		}
		if tc.outcome == "FAILED" && (rep.Reason != "other" || !strings.Contains(rep.Why, "result not recorded")) {
			t.Fatalf("%s: reason=%s why=%q; want other and the record failure named", tc.mode, rep.Reason, rep.Why)
		}
		if h := client.HGetAll(ctx, card.CardKey(sprint, label)+":result:a1").Val(); (h["valid"] == "1") != tc.due {
			t.Fatalf("%s: result hash valid=%q field=%s defect=%s, want a validated hash only for the recorded case", tc.mode, h["valid"], h["field"], h["defect"])
		}
		// The branch was pushed: only the result gate stands between the card and harvest.
		if err := client.HSet(ctx, card.CardKey(sprint, label), "pushed_sha", strings.Repeat("ab", 20)).Err(); err != nil {
			t.Fatal(err)
		}
	}

	out, err := client.FCallRO(ctx, "ns_harvest_due", nil, sprint, bench, "256").StringSlice()
	if err != nil || len(out) < 3 || out[0] != "OK" {
		t.Fatalf("ns_harvest_due: %v %v", out, err)
	}
	due := map[string]bool{}
	for i := 3; i+8 < len(out); i += 9 {
		due[out[i]] = true
	}
	for _, tc := range cases {
		if due["card-"+tc.mode] != tc.due {
			t.Fatalf("%s: harvest due=%v, want %v (due rows %v)", tc.mode, due["card-"+tc.mode], tc.due, due)
		}
	}
}
