package card_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestLaunchedRefusesAnExpiredBatchDeadline proves the final Redis transition,
// not only the parent wait, fences a child that reaches launch after its one
// batch deadline. The dealt card and event log remain untouched.
func TestLaunchedRefusesAnExpiredBatchDeadline(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	id := card.Identity{Sprint: "deadline", Label: "expired", BaseSHA: "0123abcd", Bench: "ctl-bench", Attempt: 1}
	token := attemptToken(1, "0123456789abcdef0123456789abcdef")
	seedCard(t, ctx, client, id, "dealt", token)

	got, err := card.Launched(ctx, st, card.LaunchRequest{
		Sprint: id.Sprint, Label: id.Label, Token: token,
		Branch: "nova/deadline/expired-a1", JobDir: "/jobs/deadline/expired/1",
		Deadline: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 2 || got.Reason != "TIMEOUT" || got.Resolved {
		t.Fatalf("expired launch = %+v, want code=2 TIMEOUT unresolved", got)
	}
	if state := stateOf(t, ctx, client, id.Sprint, id.Label); state != "dealt" {
		t.Fatalf("expired launch moved card to %q", state)
	}
	if n := xlen(t, ctx, client, id.Sprint); n != 0 {
		t.Fatalf("expired launch wrote %d log entries", n)
	}
}

func TestEndRecordIsTheOnlyEnd(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const (
		sprint = "control-end"
		label  = "only-end"
		base   = "0123abcd"
		bench  = "ctl-bench"
	)
	id := card.Identity{Sprint: sprint, Label: label, BaseSHA: base, Bench: bench, Attempt: 1}
	token := attemptToken(1, "0123456789abcdef0123456789abcdef")
	fenced := attemptToken(1, "ffffffffffffffffffffffffffffffff")
	seedCard(t, ctx, client, id, "dealt", token)
	member := fmt.Sprintf("%s/%s/%d", sprint, label, id.Attempt)
	if err := client.ZAdd(ctx, card.BenchStartingKey(bench), redis.Z{Score: 1, Member: member}).Err(); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(t.TempDir(), "job")
	branch := "nova/control-end/only-end-a1"

	got, err := card.Launched(ctx, st, card.LaunchRequest{
		Sprint: sprint, Label: label, Token: fenced, Branch: branch, JobDir: job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 3 || got.Reason != "FENCED" || got.Resolved {
		t.Fatalf("fenced launched: %+v", got)
	}
	if stateOf(t, ctx, client, sprint, label) != "dealt" {
		t.Fatalf("fenced launched moved the card to %s", stateOf(t, ctx, client, sprint, label))
	}

	got, err = card.Launched(ctx, st, card.LaunchRequest{
		Sprint: sprint, Label: label, Token: token, Branch: branch, JobDir: job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 0 || !got.Resolved || got.Attempt != 1 || got.Receipt == "" {
		t.Fatalf("launched: %+v", got)
	}
	if stateOf(t, ctx, client, sprint, label) != "launched" || !setHas(t, ctx, client, card.IdxKey(sprint, "launched"), label) {
		t.Fatalf("launched did not record the attempt: %+v", hashOf(t, ctx, client, sprint, label))
	}

	got, err = card.Beat(ctx, st, card.BeatRequest{Sprint: sprint, Label: label, Token: fenced})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 3 || got.Reason != "FENCED" || got.Resolved {
		t.Fatalf("fenced beat: %+v", got)
	}
	if stateOf(t, ctx, client, sprint, label) != "launched" || zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
		t.Fatal("fenced beat moved the card onto the living set")
	}

	got, err = card.Beat(ctx, st, card.BeatRequest{Sprint: sprint, Label: label, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 0 || !got.Resolved || stateOf(t, ctx, client, sprint, label) != "running" {
		t.Fatalf("beat: %+v state %s", got, stateOf(t, ctx, client, sprint, label))
	}
	if zHas(t, ctx, client, card.BenchStartingKey(bench), member) || !zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
		t.Fatal("first beat did not move starting to living")
	}
	if !setHas(t, ctx, client, card.IdxKey(sprint, "running"), label) || setHas(t, ctx, client, card.IdxKey(sprint, "launched"), label) {
		t.Fatal("first beat did not move the card index to running")
	}
	baseline := xlen(t, ctx, client, sprint)

	results := canonicalResults(t, id)
	refused := func(step string, wantCode int, wantReason string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got.Code != wantCode || got.Reason != wantReason || got.Resolved {
			t.Fatalf("%s: %+v", step, got)
		}
		if stateOf(t, ctx, client, sprint, label) != "running" {
			t.Fatalf("%s ended the card (%s)", step, stateOf(t, ctx, client, sprint, label))
		}
		if xlen(t, ctx, client, sprint) != baseline {
			t.Fatalf("%s wrote a receipt", step)
		}
		if !zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
			t.Fatalf("%s freed the slot", step)
		}
	}

	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	refused("no end record", 2, "NO-RECORD")

	if err := os.WriteFile(filepath.Join(results, "branch.txt"), []byte(branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(results, "pr.txt"), []byte("pr 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	refused("branch and pr without an end record", 2, "NO-RECORD")

	if err := os.WriteFile(filepath.Join(results, card.EndRecordName), []byte("not a record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	refused("unreadable end record", 2, "NO-RECORD")

	other := id
	other.Attempt = 2
	if err := card.WriteEndRecord(results, card.EndRecord{
		Identity: other, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "FAILED", Reason: "tests-red", ResultsDir: results,
	})
	refused("end record for another attempt", 2, "IDENTITY")

	if err := card.WriteEndRecord(results, card.EndRecord{
		Identity: id, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	refused("caller outcome disagrees with the record", 2, "RECORD")

	pushed := "0123456789abcdef0123456789abcdef01234567"
	if err := card.WriteEndRecord(results, card.EndRecord{
		Identity: id, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: pushed, At: "1970-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: fenced, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	refused("fenced token", 3, "FENCED")
	if _, err := os.Stat(filepath.Join(results, card.EndRecordName)); err != nil {
		t.Fatal("fenced end removed the record")
	}

	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 0 || !got.Resolved || got.Attempt != 1 || got.Receipt == "" {
		t.Fatalf("end: %+v", got)
	}
	body := hashOf(t, ctx, client, sprint, label)
	if body["state"] != "ended" || body["outcome"] != "DONE" || body["reason"] != "done" || body["exit"] != "0" || body["pushed_sha"] != pushed || body["results"] != results || body["end_receipt"] != got.Receipt {
		t.Fatalf("ended hash = %+v", body)
	}
	if zHas(t, ctx, client, card.BenchLivingKey(bench), member) || zHas(t, ctx, client, card.BenchStartingKey(bench), member) {
		t.Fatal("end left the slot leased")
	}
	if !setHas(t, ctx, client, card.IdxKey(sprint, "ended"), label) || setHas(t, ctx, client, card.IdxKey(sprint, "running"), label) {
		t.Fatal("end did not move the card index")
	}
	if !setHas(t, ctx, client, card.BenchEndedKey(sprint, bench), label) {
		t.Fatal("end did not record the bench")
	}
	if xlen(t, ctx, client, sprint) != baseline+1 {
		t.Fatalf("end wrote %d log entries from %d", xlen(t, ctx, client, sprint), baseline)
	}
	assertReceipt(t, ctx, client, sprint, got.Receipt, map[string]string{
		"kind": "card", "id": label, "from": "running", "to": "ended",
		"attempt": "1", "token_sha": card.TokenSHA(token), "actor": "card-end",
		"reason": "done", "evidence": results, "idem": "end:" + id.String(),
	}, token)

	again, err := card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Code != 0 || !again.Resolved || again.Receipt != got.Receipt || xlen(t, ctx, client, sprint) != baseline+1 {
		t.Fatalf("replay: %+v len %d", again, xlen(t, ctx, client, sprint))
	}
	got, err = card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: fenced, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 3 || got.Reason != "FENCED" || got.Resolved {
		t.Fatalf("fenced after end: %+v", got)
	}
	if hashOf(t, ctx, client, sprint, label)["outcome"] != "DONE" || xlen(t, ctx, client, sprint) != baseline+1 {
		t.Fatal("fenced call after end rewrote the card")
	}
}

// TestEndAfterPushAndDealRecordsEnded is nova-tools #3351's production-shape
// regression. Card push stores the linted 40-hex base_sha while deal binds the
// attempt identity to its 8-hex prefix. End must compare those two shapes
// without weakening the identity check, free the bench slot, and expose the
// ended card to harvest.
func TestEndAfterPushAndDealRecordsEnded(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const (
		sprint = "control-3351"
		label  = "end-after-push-deal"
		bench  = "ctl-bench"
	)

	srv := repoServer(t)
	fixture := validCard(srv.URL + "/acme/public.git")
	fixture.label = label
	if got := card.Push(ctx, client, sprint, fixture.render()); got.Code != 0 {
		t.Fatalf("card push: exit %d stdout %q stderr %q", got.Code, got.Stdout, got.Stderr)
	}
	if err := client.HSet(ctx, "s:"+sprint, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "lease:reconciler", "token", "live-fence").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "benches", bench).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "bench:"+bench+":beat", "host", "bench.invalid", "user", "worker").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "bench:"+bench+":state", "state", "UP").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "bench:"+bench+":desired", "slots", "1", "paused", "0").Err(); err != nil {
		t.Fatal(err)
	}

	token := attemptToken(1, "0123456789abcdef0123456789abcdef")
	reply, err := client.FCall(ctx, "ns_card_deal", nil,
		bench, "live-fence", "test", "3351",
		sprint, label, "1", token, card.TokenSHA(token)).StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(reply) != 5 || reply[0] != "DEALT" {
		t.Fatalf("deal reply = %v, want one DEALT card", reply)
	}
	body := hashOf(t, ctx, client, sprint, label)
	id, err := card.ParseIdentity(body["identity"])
	if err != nil {
		t.Fatal(err)
	}
	if len(body["base_sha"]) != 40 || id.BaseSHA != body["base_sha"][:8] {
		t.Fatalf("push/deal identity shape: base_sha=%q identity=%q", body["base_sha"], body["identity"])
	}
	member := sprint + "/" + label + "/1"
	if !zHas(t, ctx, client, card.BenchStartingKey(bench), member) {
		t.Fatal("deal did not lease the slot")
	}

	if got, err := card.Launched(ctx, st, card.LaunchRequest{
		Sprint: sprint, Label: label, Token: token, Branch: card.WrapperBranch(sprint, label, 1), JobDir: "/jobs/" + label,
	}); err != nil || !got.Resolved {
		t.Fatalf("launched = %+v, %v", got, err)
	}
	if got, err := card.Beat(ctx, st, card.BeatRequest{Sprint: sprint, Label: label, Token: token}); err != nil || !got.Resolved {
		t.Fatalf("beat = %+v, %v", got, err)
	}
	results := canonicalResults(t, id)
	pushed := "89abcdef0123456789abcdef0123456789abcdef"
	writeRecord(t, results, card.EndRecord{
		Identity: id, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: pushed, At: "1970-01-01T00:00:00Z",
	})
	ended, err := card.End(ctx, st, card.EndRequest{
		Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results,
	})
	if err != nil || !ended.Resolved || ended.Code != 0 {
		t.Fatalf("end after push and deal = %+v, %v", ended, err)
	}
	body = hashOf(t, ctx, client, sprint, label)
	if body["state"] != "ended" || body["results"] != results || body["pushed_sha"] != pushed {
		t.Fatalf("ended card = %+v", body)
	}
	if zHas(t, ctx, client, card.BenchStartingKey(bench), member) || zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
		t.Fatal("end left the bench slot leased")
	}

	due, err := client.FCall(ctx, "ns_harvest_due", nil, sprint, bench, "10").StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(due) < 4 || due[0] != "OK" || due[3] != label {
		t.Fatalf("harvest due = %v, want %s", due, label)
	}
	msgs, err := client.XRange(ctx, card.LogKey(sprint), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	ends := 0
	for _, msg := range msgs {
		if fmt.Sprint(msg.Values["kind"]) == "card" && fmt.Sprint(msg.Values["id"]) == label && fmt.Sprint(msg.Values["to"]) == "ended" {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("card-end log entries = %d, want 1", ends)
	}
}

func TestControl26OtherAttemptResolvesNothing(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const (
		sprint = "control26"
		label  = "other-attempt"
		base   = "89abcdef"
		bench  = "ctl-bench"
	)
	current := card.Identity{Sprint: sprint, Label: label, BaseSHA: base, Bench: bench, Attempt: 2}
	token := attemptToken(2, "0123456789abcdef0123456789abcdef")
	seedCard(t, ctx, client, current, "reconcile-required", token)
	branch := "nova/control26/other-attempt-a1"
	if err := client.HSet(ctx, card.CardKey(sprint, label), "branch", branch).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, card.IdxKey(sprint, "reconcile-required"), label).Err(); err != nil {
		t.Fatal(err)
	}
	member1 := fmt.Sprintf("%s/%s/1", sprint, label)
	member2 := fmt.Sprintf("%s/%s/2", sprint, label)
	if err := client.ZAdd(ctx, card.BenchLivingKey(bench),
		redis.Z{Score: 1, Member: member1},
		redis.Z{Score: 2, Member: member2},
	).Err(); err != nil {
		t.Fatal(err)
	}
	orphan := card.Identity{Sprint: sprint, Label: "stays-orphan", BaseSHA: base, Bench: bench, Attempt: 1}
	seedCard(t, ctx, client, orphan, "orphan-effect", attemptToken(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	results := canonicalResults(t, current)
	if err := os.WriteFile(filepath.Join(results, "branch.txt"), []byte(branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nothing := func(step string) {
		t.Helper()
		got, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: sprint, Label: label, ResultsDir: results})
		if err != nil {
			t.Fatal(err)
		}
		if got.Resolved || got.Code != 0 || got.Reason != "NOTHING" || got.Receipt != "" {
			t.Fatalf("%s: %+v", step, got)
		}
		body := hashOf(t, ctx, client, sprint, label)
		if body["state"] != "reconcile-required" || body["outcome"] != "" || body["branch"] != branch {
			t.Fatalf("%s resolved the card: %+v", step, body)
		}
		if hashOf(t, ctx, client, sprint, orphan.Label)["state"] != "orphan-effect" {
			t.Fatalf("%s resolved the orphan", step)
		}
		if !setHas(t, ctx, client, card.IdxKey(sprint, "reconcile-required"), label) || setHas(t, ctx, client, card.IdxKey(sprint, "ended"), label) {
			t.Fatalf("%s moved the index", step)
		}
		if !zHas(t, ctx, client, card.BenchLivingKey(bench), member1) || !zHas(t, ctx, client, card.BenchLivingKey(bench), member2) {
			t.Fatalf("%s touched a living attempt", step)
		}
		if xlen(t, ctx, client, sprint) != 0 {
			t.Fatalf("%s wrote a receipt", step)
		}
	}

	nothing("branch file and no end record")

	attempt1 := current
	attempt1.Attempt = 1
	writeRecord(t, results, card.EndRecord{
		Identity: attempt1, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", At: "1970-01-01T00:00:00Z",
	})
	nothing("end record names attempt 1")

	otherCut := current
	otherCut.BaseSHA = "11111111"
	writeRecord(t, results, card.EndRecord{
		Identity: otherCut, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	})
	nothing("end record names another cut sha")

	writeRecord(t, results, card.EndRecord{
		Identity: current, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: "000000000000", PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	})
	nothing("end record token_sha is not this attempt")

	orphanDir := canonicalResults(t, orphan)
	writeRecord(t, orphanDir, card.EndRecord{
		Identity: card.Identity{Sprint: sprint, Label: orphan.Label, BaseSHA: base, Bench: bench, Attempt: 2},
		Outcome:  "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(attemptToken(1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")), PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	})
	got, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: sprint, Label: orphan.Label, ResultsDir: orphanDir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Resolved || got.Reason != "NOTHING" || hashOf(t, ctx, client, sprint, orphan.Label)["state"] != "orphan-effect" {
		t.Fatalf("orphan other attempt: %+v state %s", got, hashOf(t, ctx, client, sprint, orphan.Label)["state"])
	}
	nothing("current attempt still unresolved after the orphan")

	writeRecord(t, results, card.EndRecord{
		Identity: current, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "1970-01-01T00:00:00Z",
	})
	got, err = card.Resolve(ctx, st, card.ResolveRequest{Sprint: sprint, Label: label, ResultsDir: results})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Resolved || got.Code != 0 || got.Attempt != 2 || got.Receipt == "" {
		t.Fatalf("matching record: %+v", got)
	}
	body := hashOf(t, ctx, client, sprint, label)
	if body["state"] != "ended" || body["outcome"] != "FAILED" || body["reason"] != "tests-red" || body["branch"] != branch || body["exit"] != "1" || body["pushed_sha"] != "-" {
		t.Fatalf("matching record wrote %+v", body)
	}
	if zHas(t, ctx, client, card.BenchLivingKey(bench), member2) || !zHas(t, ctx, client, card.BenchLivingKey(bench), member1) {
		t.Fatal("resolve took attempt 1's slot or left attempt 2 leased")
	}
	if setHas(t, ctx, client, card.IdxKey(sprint, "reconcile-required"), label) || !setHas(t, ctx, client, card.IdxKey(sprint, "ended"), label) {
		t.Fatal("resolve did not move only the ended index")
	}
	if hashOf(t, ctx, client, sprint, orphan.Label)["state"] != "orphan-effect" {
		t.Fatal("matching record ended the orphan")
	}
	if xlen(t, ctx, client, sprint) != 1 {
		t.Fatalf("log len %d, want the one matching end", xlen(t, ctx, client, sprint))
	}
	assertReceipt(t, ctx, client, sprint, got.Receipt, map[string]string{
		"kind": "card", "id": label, "from": "reconcile-required", "to": "ended",
		"attempt": "2", "token_sha": card.TokenSHA(token), "actor": "card-resolve",
		"reason": "tests-red", "idem": "end:" + current.String(),
	}, token)
}

func TestEndRecordInWrongDirectoryResolvesNothing(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const (
		sprint = "wrong-dir"
		label  = "copied-record"
		base   = "89abcdef"
		bench  = "ctl-bench"
	)
	id := card.Identity{Sprint: sprint, Label: label, BaseSHA: base, Bench: bench, Attempt: 2}
	token := attemptToken(2, "0123456789abcdef0123456789abcdef")
	seedCard(t, ctx, client, id, "reconcile-required", token)
	if err := client.SAdd(ctx, card.IdxKey(sprint, "reconcile-required"), label).Err(); err != nil {
		t.Fatal(err)
	}
	member := fmt.Sprintf("%s/%s/%d", sprint, label, id.Attempt)
	if err := client.ZAdd(ctx, card.BenchLivingKey(bench), redis.Z{Score: 2, Member: member}).Err(); err != nil {
		t.Fatal(err)
	}
	canonical := canonicalResults(t, id)
	writeRecord(t, canonical, card.EndRecord{
		Identity: id, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: "0123456789abcdef0123456789abcdef01234567", At: "1970-01-01T00:00:00Z",
	})
	wrong := filepath.Join(t.TempDir(), "wrong")
	if err := os.Mkdir(wrong, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(canonical, card.EndRecordName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrong, card.EndRecordName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	copied, err := card.ReadEndRecord(wrong)
	if err != nil || copied.Identity != id || copied.TokenSHA != card.TokenSHA(token) {
		t.Fatalf("copy is not a valid record for this attempt: %v %+v", err, copied)
	}

	got, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: sprint, Label: label, ResultsDir: wrong})
	if err != nil {
		t.Fatal(err)
	}
	if got.Resolved || got.Code != 0 || got.Reason != "NOTHING" || got.Receipt != "" {
		t.Fatalf("copied record resolved the attempt: %+v", got)
	}
	cardBody := hashOf(t, ctx, client, sprint, label)
	if cardBody["state"] != "reconcile-required" || cardBody["outcome"] != "" || cardBody["results"] != "" || cardBody["end_receipt"] != "" {
		t.Fatalf("wrong directory wrote the card: %+v", cardBody)
	}
	if !setHas(t, ctx, client, card.IdxKey(sprint, "reconcile-required"), label) || setHas(t, ctx, client, card.IdxKey(sprint, "ended"), label) {
		t.Fatal("wrong directory moved the index")
	}
	if !zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
		t.Fatal("wrong directory freed the slot")
	}
	if xlen(t, ctx, client, sprint) != 0 {
		t.Fatal("wrong directory wrote a receipt")
	}
	if _, err := os.Stat(filepath.Join(wrong, card.EndRecordName)); err != nil {
		t.Fatal("resolve removed the copied record")
	}
	if _, err := os.Stat(filepath.Join(canonical, card.EndRecordName)); err != nil {
		t.Fatal("resolve removed the canonical record")
	}
}

func attemptToken(attempt int, bits string) string {
	return fmt.Sprintf("%d.%s", attempt, bits)
}

func canonicalResults(t *testing.T, id card.Identity) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id.Sprint, id.Label, id.BaseSHA, id.Bench, fmt.Sprintf("%d", id.Attempt))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeRecord(t *testing.T, dir string, rec card.EndRecord) {
	t.Helper()
	if err := card.WriteEndRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
}

func seedCard(t *testing.T, ctx context.Context, client *redis.Client, id card.Identity, state, token string) {
	t.Helper()
	err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
		"state":     state,
		"attempt":   fmt.Sprintf("%d", id.Attempt),
		"token":     token,
		"token_sha": card.TokenSHA(token),
		"identity":  id.String(),
		"bench":     id.Bench,
		"base_sha":  id.BaseSHA,
	}).Err()
	if err != nil {
		t.Fatal(err)
	}
}

func newSprint(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return store.New(client), client
}

func hashOf(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string) map[string]string {
	t.Helper()
	got, err := client.HGetAll(ctx, card.CardKey(sprint, label)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func stateOf(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string) string {
	t.Helper()
	return hashOf(t, ctx, client, sprint, label)["state"]
}

func setHas(t *testing.T, ctx context.Context, client *redis.Client, key, member string) bool {
	t.Helper()
	ok, err := client.SIsMember(ctx, key, member).Result()
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func zHas(t *testing.T, ctx context.Context, client *redis.Client, key, member string) bool {
	t.Helper()
	_, err := client.ZScore(ctx, key, member).Result()
	if err == redis.Nil {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

func xlen(t *testing.T, ctx context.Context, client *redis.Client, sprint string) int64 {
	t.Helper()
	n, err := client.XLen(ctx, card.LogKey(sprint)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func assertReceipt(t *testing.T, ctx context.Context, client *redis.Client, sprint, id string, want map[string]string, rawToken string) {
	t.Helper()
	msgs, err := client.XRange(ctx, card.LogKey(sprint), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var found map[string]interface{}
	for _, msg := range msgs {
		blob := fmt.Sprint(msg.Values)
		if strings.Contains(blob, rawToken) {
			t.Fatalf("log entry %s carries the raw token", msg.ID)
		}
		if msg.ID == id {
			found = msg.Values
		}
	}
	if found == nil {
		t.Fatalf("receipt %s is not in the log", id)
	}
	for key, value := range want {
		if fmt.Sprint(found[key]) != value {
			t.Fatalf("receipt %s %s = %v, want %s", id, key, found[key], value)
		}
	}
	if _, ok := found["at"]; !ok || fmt.Sprint(found["at"]) == "" {
		t.Fatal("receipt has no Redis time")
	}
}

func startRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}
