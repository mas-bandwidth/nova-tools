package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/harvest"
)

func TestHarvestDoesNotPushWithoutFenceAndRUNToken(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/5\n")

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cards := harvest.NewMemory()
	cards.Set(harvest.Projection{
		Card: "card-stale", Attempt: "attempt-new", FenceEpoch: 2, Attempts: 2, State: "STARTED",
	})
	contract := "RESULT card-stale sha=aaa"
	addCard(t, root, "card-stale", "0", "flash", contract,
		contract+"\nDONE\nBRANCH rowan/br-stale\nREPO owner/repo\n")

	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return now },
		Effect: &HarvestEffect{
			Owner: harvest.New(cards, ctrl),
			Creds: map[string]harvest.Credential{
				"card-stale": {Card: "card-stale", Attempt: "attempt-old", FenceEpoch: 1},
			},
			Result: map[string]harvest.Token{"card-stale": effectToken("card-stale", "attempt-old", harvest.ActionRESULT, 1, now)},
			Push:   map[string]harvest.Token{"card-stale": effectToken("card-stale", "attempt-old", harvest.ActionPush, 1, now)},
			Accept: map[string]harvest.Token{"card-stale": effectToken("card-stale", "attempt-old", harvest.ActionAccept, 1, now)},
		},
	})
	_ = code
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-stale") {
		t.Fatalf("stale fence must refuse harvest publication, stderr:\n%s\nstdout:\n%s", errs.String(), out.String())
	}
	if !strings.Contains(out.String(), "pushed=0") || !strings.Contains(out.String(), "prs=0") {
		t.Fatalf("stale fence must not push or open a PR:\n%s", out.String())
	}
	for _, line := range arglogLines(t, arglog) {
		if strings.HasPrefix(line, "git push ") {
			t.Fatalf("harvest pushed without the fence check: %s", line)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "lifecycle", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("effect owner wrote events.jsonl: %v", err)
	}
}

func TestHarvestDoesNotPushDuringPAUSE(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/6\n")

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	ctrl.Pause()
	cards := harvest.NewMemory()
	cards.Set(harvest.Projection{
		Card: "card-pause", Attempt: "att-pause", FenceEpoch: 1, Attempts: 1, State: "STARTED",
	})
	contract := "RESULT card-pause sha=bbb"
	addCard(t, root, "card-pause", "0", "flash", contract,
		contract+"\nDONE\nBRANCH rowan/br-pause\nREPO owner/repo\n")

	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return now },
		Effect: &HarvestEffect{
			Owner: harvest.New(cards, ctrl),
			Creds: map[string]harvest.Credential{
				"card-pause": {Card: "card-pause", Attempt: "att-pause", FenceEpoch: 1},
			},
			Result: map[string]harvest.Token{"card-pause": effectToken("card-pause", "att-pause", harvest.ActionRESULT, 1, now)},
			Push:   map[string]harvest.Token{"card-pause": effectToken("card-pause", "att-pause", harvest.ActionPush, 1, now)},
			Accept: map[string]harvest.Token{"card-pause": effectToken("card-pause", "att-pause", harvest.ActionAccept, 1, now)},
		},
	})
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-pause") {
		t.Fatalf("PAUSE must refuse harvest publication, stderr:\n%s\nstdout:\n%s", errs.String(), out.String())
	}
	if !strings.Contains(out.String(), "pushed=0") {
		t.Fatalf("PAUSE must not push:\n%s", out.String())
	}
	for _, line := range arglogLines(t, arglog) {
		if strings.HasPrefix(line, "git push ") {
			t.Fatalf("harvest pushed during PAUSE: %s", line)
		}
	}
}

func TestHarvestPushesWhenCurrentFenceAndRUNTokenMatch(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/7\n")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE POOL EMPTY in-flight=0\n",
	}})

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cards := harvest.NewMemory()
	cards.Set(harvest.Projection{
		Card: "card-ok", Attempt: "att-ok", FenceEpoch: 1, Attempts: 1, State: "STARTED",
	})
	contract := "RESULT card-ok sha=ccc"
	addCard(t, root, "card-ok", "0", "flash", contract,
		contract+"\nDONE\nBRANCH rowan/br-ok\nREPO owner/repo\n")

	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return now },
		Effect: &HarvestEffect{
			Owner: harvest.New(cards, ctrl),
			Creds: map[string]harvest.Credential{
				"card-ok": {Card: "card-ok", Attempt: "att-ok", FenceEpoch: 1},
			},
			Result: map[string]harvest.Token{"card-ok": effectToken("card-ok", "att-ok", harvest.ActionRESULT, 1, now)},
			Push:   map[string]harvest.Token{"card-ok": effectToken("card-ok", "att-ok", harvest.ActionPush, 1, now)},
			Accept: map[string]harvest.Token{"card-ok": effectToken("card-ok", "att-ok", harvest.ActionAccept, 1, now)},
		},
	})
	if code != 0 {
		t.Fatalf("Harvest code=%d stderr:\n%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "pushed=1") || !strings.Contains(out.String(), "prs=1") {
		t.Fatalf("current worker must push and open a PR:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "lifecycle", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("effect owner wrote events.jsonl: %v", err)
	}
}

func TestReview2153ConfiguredEffectNilOwnerMustRefuse(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/8\n")
	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	contract := "RESULT card-nil sha=ddd"
	addCard(t, root, "card-nil", "0", "flash", contract,
		contract+"\nDONE\nBRANCH rowan/br-nil\nREPO owner/repo\n")
	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return now },
		Effect: &HarvestEffect{
			Owner: nil,
			Creds: map[string]harvest.Credential{
				"card-nil": {Card: "card-nil", Attempt: "att-nil", FenceEpoch: 1},
			},
		},
	})
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-nil") {
		t.Fatalf("configured Effect with nil Owner must refuse, stderr:\n%s\nstdout:\n%s", errs.String(), out.String())
	}
	if !strings.Contains(out.String(), "pushed=0") {
		t.Fatalf("nil Owner must not fall through to push:\n%s", out.String())
	}
	for _, line := range arglogLines(t, arglog) {
		if strings.HasPrefix(line, "git push ") {
			t.Fatalf("configured Effect with nil Owner pushed: %s", line)
		}
	}
}

func TestReview2153EachActionReadsCurrentTime(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/9\n")
	base := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, base.Add(time.Hour))
	cards := harvest.NewMemory()
	cards.Set(harvest.Projection{
		Card: "card-time", Attempt: "att-time", FenceEpoch: 1, Attempts: 1, State: "STARTED",
	})
	contract := "RESULT card-time sha=eee"
	addCard(t, root, "card-time", "0", "flash", contract,
		contract+"\nDONE\nBRANCH rowan/br-time\nREPO owner/repo\n")
	n := 0
	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time {
			n++
			if n <= 2 {
				return base
			}
			return base.Add(2 * time.Hour)
		},
		Effect: &HarvestEffect{
			Owner: harvest.New(cards, ctrl),
			Creds: map[string]harvest.Credential{
				"card-time": {Card: "card-time", Attempt: "att-time", FenceEpoch: 1},
			},
			Result: map[string]harvest.Token{"card-time": effectToken("card-time", "att-time", harvest.ActionRESULT, 1, base)},
			Push:   map[string]harvest.Token{"card-time": effectToken("card-time", "att-time", harvest.ActionPush, 1, base)},
			Accept: map[string]harvest.Token{"card-time": effectToken("card-time", "att-time", harvest.ActionAccept, 1, base)},
		},
	})
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-time") {
		t.Fatalf("accept/push after expiry must refuse using current time, stderr:\n%s\nstdout:\n%s", errs.String(), out.String())
	}
	if !strings.Contains(out.String(), "pushed=0") {
		t.Fatalf("entry-time must not authorize a later expired push:\n%s", out.String())
	}
	for _, line := range arglogLines(t, arglog) {
		if strings.HasPrefix(line, "git push ") {
			t.Fatalf("push used harvest-entry time past expiry: %s", line)
		}
	}
}

func effectToken(card, attempt string, action harvest.Action, gen int, now time.Time) harvest.Token {
	return harvest.Token{
		ID: "tok-" + string(action) + "-" + attempt, Card: card, Attempt: attempt,
		Action: action, Generation: gen, Scope: "fleet", Expires: now.Add(time.Hour),
	}
}
