package typedrec_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
