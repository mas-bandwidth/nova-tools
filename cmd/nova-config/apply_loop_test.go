package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateRedis fails a write when asked, and records each stamp.
type gateRedis struct {
	*fakeRedis
	failWrite bool
	stamps    []string
}

func (g *gateRedis) Write(ctx context.Context, kind string, row config.Row, prev config.View, actor, idem string) error {
	if g.failWrite {
		return errors.New("redis refused the write")
	}
	return g.fakeRedis.Write(ctx, kind, row, prev, actor, idem)
}

func (g *gateRedis) Remove(ctx context.Context, kind, name, actor, idem string) error {
	if g.failWrite {
		return errors.New("redis refused the remove")
	}
	return g.fakeRedis.Remove(ctx, kind, name, actor, idem)
}

func (g *gateRedis) Stamp(ctx context.Context, kind string, prev, rev int64) error {
	g.stamps = append(g.stamps, fmt.Sprintf("%s %d %d", kind, prev, rev))
	return g.fakeRedis.Stamp(ctx, kind, prev, rev)
}

func insertMachines(t *testing.T, h *harness, n int) {
	t.Helper()
	k, ok := config.Lookup(config.KindMachine)
	require.True(t, ok)
	for i := 1; i <= n; i++ {
		row, err := k.NewRow(fmt.Sprintf("m%d", i), map[string]string{"user": "login", "seat": "s1", "slots": "1"})
		require.NoError(t, err)
		_, err = h.store.Insert(context.Background(), config.KindMachine, row, "a1")
		require.NoError(t, err)
	}
}

func TestWritingVerbEndsWithApplyDisposition(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	code, out, errs := h.run(t, "machine", "add", "m1", "--user", "login", "--seat", "s1", "--slots", "1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG ADD kind=machine")
	assert.True(t, strings.HasSuffix(out, "UNAPPLIED rev=1: --redis is required: host:port (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-config apply\n"), out)

	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, errs = h.run(t, "machine", "set", "m1", "--slots", "2")
	require.Equal(t, 0, code, errs)
	assert.True(t, strings.HasSuffix(out, "APPLIED rev=2\n"), out)
	assert.Equal(t, int64(2), h.redis.revs[config.KindMachine])
}

func TestWritingVerbReportsFailedApplyAfterStoreCommit(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	g := &gateRedis{fakeRedis: h.redis, failWrite: true}
	d := h.deps()
	d.openRedis = func(context.Context, string) (redisSide, error) { return g, nil }
	var out, errb bytes.Buffer
	code := runKind(context.Background(), mustMachine(), []string{"add", "m1", "--user", "login", "--seat", "s1", "--slots", "1"}, &out, &errb, d)
	require.Equal(t, 1, code, errb.String())
	assert.True(t, strings.HasSuffix(out.String(), "UNAPPLIED rev=1: redis refused the write; run: nova-config apply\n"), out.String())
	rev, err := h.store.Rev(context.Background(), config.KindMachine)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)
	assert.Equal(t, int64(0), h.redis.revs[config.KindMachine])
}

func TestApplyInstallAndUninstallJSONStayOneObject(t *testing.T) {
	t.Parallel()
	h := newHarness()
	insertMachines(t, h, 1)
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	dir := t.TempDir()
	host := applyHost{
		goos:   "linux",
		exe:    func() (string, error) { return "/usr/local/bin/nova-config", nil },
		home:   func() (string, error) { return dir, nil },
		load:   func(string) error { return nil },
		unload: func(string) error { return nil },
	}
	g := &gateRedis{fakeRedis: h.redis}
	d := h.deps()
	d.openRedis = func(context.Context, string) (redisSide, error) { return g, nil }
	var out, errb bytes.Buffer
	code := runApplyInstall(context.Background(), []string{"--machine", "m1", "--dir", dir, "--json"}, &out, &errb, d, host)
	require.Equal(t, 0, code, errb.String())
	assert.True(t, json.Valid(out.Bytes()), out.String())
	assert.NotContains(t, out.String(), "CONFIG APPLY")
	out.Reset()
	g.failWrite = true
	code = runApplyUninstall(context.Background(), []string{"--dir", dir, "--json"}, &out, &errb, d, host)
	require.Equal(t, 1, code, errb.String())
	assert.True(t, json.Valid(out.Bytes()), out.String())
	assert.Contains(t, out.String(), "UNAPPLIED rev=")
	assert.NotContains(t, out.String(), "CONFIG APPLY")
}

// withoutDisposition lets older CRUD tests keep checking their original
// operation lines while also checking the new final apply result.
func withoutDisposition(t *testing.T, out string) string {
	t.Helper()
	first, _, ok := strings.Cut(out, "\n")
	if !ok || (!strings.HasPrefix(first, "CONFIG ADD ") && !strings.HasPrefix(first, "CONFIG SET ") && !strings.HasPrefix(first, "CONFIG REMOVE ")) {
		return out
	}
	trimmed := strings.TrimSuffix(out, "\n")
	pos := strings.LastIndex(trimmed, "\n")
	require.GreaterOrEqual(t, pos, 0, out)
	last := trimmed[pos+1:]
	assert.True(t, strings.HasPrefix(last, "APPLIED rev=") || strings.HasPrefix(last, "UNAPPLIED rev="), "write has no final disposition: %q", out)
	var rev string
	for _, word := range strings.Fields(first) {
		if strings.HasPrefix(word, "rev=") {
			rev = word
		}
	}
	require.NotEmpty(t, rev, first)
	assert.Contains(t, last, rev, out)
	return trimmed[:pos+1]
}

func TestStatusReportsGapAgeAndJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness()
	base := time.Unix(1_700_000_000, 0).UTC()
	h.store.Now = func() time.Time { return base.Add(-90 * time.Second) }
	insertMachines(t, h, 1)
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, _ := h.run(t, "status")
	require.Equal(t, 1, code)
	assert.Contains(t, out, "machine_rev=1")
	assert.Contains(t, out, "machine_applied=0 machine_gap_age=90s")
	assert.Contains(t, out, "JUDGMENT kind=machine store=1 applied=0 age=90s")
	assert.Equal(t, 1, strings.Count(out, "JUDGMENT kind=machine"), "the judgment is one line, never also a NOTE: %q", out)
}

// TestStatusMultiRevisionGapAgesFromTheFirstUnappliedRevision is a gap of
// more than one revision: the age is the first history row after the applied
// revision, so a write one second ago does not hide a change from hours ago
// or suppress the older-than-60s judgment.
func TestStatusMultiRevisionGapAgesFromTheFirstUnappliedRevision(t *testing.T) {
	t.Parallel()
	h := newHarness()
	base := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	h.store.Now = func() time.Time { return base.Add(-2 * time.Hour) }
	insertMachines(t, h, 1)
	h.store.Now = func() time.Time { return base.Add(-time.Second) }
	_, _, err := h.store.Update(ctx, config.KindMachine, "m1", map[string]string{"slots": "2"}, "a1")
	require.NoError(t, err)
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, _ := h.run(t, "status")
	require.Equal(t, 1, code)
	assert.Contains(t, out, "machine_rev=2")
	assert.Contains(t, out, "machine_applied=0 machine_gap_age=7200s")
	assert.NotContains(t, out, "machine_gap_age=1s")
	assert.Contains(t, out, "JUDGMENT kind=machine store=2 applied=0 age=7200s")
	assert.Equal(t, 1, strings.Count(out, "JUDGMENT kind=machine"), "the judgment is one line: %q", out)
}

// TestStatusRemovalGapAgesFromTheRemovedRowsHistory is a gap whose latest
// write removes the row. List excludes it; the Redis copy still holds the
// name. The age is the first history revision after the applied revision,
// including that removed row, not unknown and not the recent removal.
func TestStatusRemovalGapAgesFromTheRemovedRowsHistory(t *testing.T) {
	t.Parallel()
	h := newHarness()
	base := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	h.store.Now = func() time.Time { return base.Add(-3 * time.Hour) }
	insertMachines(t, h, 1)
	h.redis.revs[config.KindMachine] = 1
	h.redis.views[config.KindMachine] = map[string]config.View{"m1": {}}
	h.store.Now = func() time.Time { return base.Add(-2 * time.Hour) }
	_, _, err := h.store.Update(ctx, config.KindMachine, "m1", map[string]string{"slots": "2"}, "a1")
	require.NoError(t, err)
	h.store.Now = func() time.Time { return base.Add(-time.Second) }
	_, err = h.store.Delete(ctx, config.KindMachine, "m1", "a1")
	require.NoError(t, err)
	rows, err := h.store.List(ctx, config.KindMachine)
	require.NoError(t, err)
	require.Empty(t, rows)
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, _ := h.run(t, "status")
	require.Equal(t, 1, code)
	assert.Contains(t, out, "machine_rev=3")
	assert.Contains(t, out, "machine_applied=1 machine_gap_age=7200s")
	assert.NotContains(t, out, "machine_gap_age=1s")
	assert.NotContains(t, out, "machine_gap_age=unknown")
	assert.Contains(t, out, "JUDGMENT kind=machine store=3 applied=1 age=7200s")
	assert.Equal(t, 1, strings.Count(out, "JUDGMENT kind=machine"), "the judgment is one line: %q", out)
}

// TestStatusAddThenRemoveBeforeApplyAgesTheGap keeps the first unapplied
// revision even when no row survives in either the store or Redis view.
func TestStatusAddThenRemoveBeforeApplyAgesTheGap(t *testing.T) {
	t.Parallel()
	h := newHarness()
	base := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	h.store.Now = func() time.Time { return base.Add(-2 * time.Hour) }
	insertMachines(t, h, 1)
	h.store.Now = func() time.Time { return base.Add(-time.Second) }
	_, err := h.store.Delete(ctx, config.KindMachine, "m1", "a1")
	require.NoError(t, err)
	rows, err := h.store.List(ctx, config.KindMachine)
	require.NoError(t, err)
	require.Empty(t, rows)
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, _ := h.run(t, "status")
	require.Equal(t, 1, code)
	assert.Contains(t, out, "machine_rev=2")
	assert.Contains(t, out, "machine_applied=0 machine_gap_age=7200s")
	assert.Contains(t, out, "JUDGMENT kind=machine store=2 applied=0 age=7200s")
	assert.Equal(t, 1, strings.Count(out, "JUDGMENT kind=machine"))
}

func TestApplyPassClosesExactlyTheRevsBetweenAppliedAndStore(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 5)
	ctx := context.Background()
	rev, err := h.store.Rev(ctx, config.KindMachine)
	require.NoError(t, err)
	require.Equal(t, int64(5), rev)
	h.redis.revs[config.KindMachine] = 3
	g := &gateRedis{fakeRedis: h.redis}
	var out bytes.Buffer
	err = applyPass(ctx, h.store, g, h.deps(), "a1", []string{config.KindMachine, config.KindRoute}, false, false, "", &out)
	require.NoError(t, err)
	assert.Equal(t, []string{"machine 3 5"}, g.stamps, "one pass stamps the store revision, not each history id between")
	assert.Equal(t, int64(5), h.redis.revs[config.KindMachine])
	assert.Contains(t, out.String(), "CONFIG GAP kind=machine store=5 applied=3 age=0s")
	assert.Contains(t, out.String(), "CONFIG APPLY kind=machine add=5 set=0 remove=0 rev=5")
	assert.NotContains(t, out.String(), "kind=route", "a kind whose revisions match is left alone")
	assert.NotContains(t, out.String(), "JUDGMENT")
	writes := len(h.redis.log)

	var second bytes.Buffer
	err = applyPass(ctx, h.store, g, h.deps(), "a1", []string{config.KindMachine, config.KindRoute}, false, false, "", &second)
	require.NoError(t, err)
	assert.Equal(t, []string{"machine 3 5"}, g.stamps, "a second pass of the same store writes nothing")
	assert.Equal(t, writes, len(h.redis.log))
	assert.Equal(t, "", second.String())
}

func TestApplyPassErrorLeavesAppliedRevAndIsReportedOnce(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 5)
	ctx := context.Background()
	h.redis.revs[config.KindMachine] = 3
	g := &gateRedis{fakeRedis: h.redis, failWrite: true}
	var out bytes.Buffer
	err := applyPass(ctx, h.store, g, h.deps(), "a1", []string{config.KindMachine}, false, false, "", &out)
	require.Error(t, err)
	var ae *applyErr
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, int64(5), ae.rev)
	assert.Contains(t, err.Error(), "UNAPPLIED rev=5: redis refused the write; run: nova-config apply")
	assert.Empty(t, g.stamps)
	assert.Equal(t, int64(3), h.redis.revs[config.KindMachine], "a failed apply leaves the applied revision")
	assert.Empty(t, h.redis.log)

	k, ok := config.Lookup(config.KindMachine)
	require.True(t, ok)
	var reports []string
	passes := 0
	err = applyEvery(ctx, time.Second, func(ctx context.Context) error {
		passes++
		switch passes {
		case 3:
			g.failWrite = false
		case 4:
			row, err := k.NewRow("m6", map[string]string{"user": "login", "seat": "s1", "slots": "1"})
			require.NoError(t, err)
			_, err = h.store.Insert(ctx, config.KindMachine, row, "a1")
			require.NoError(t, err)
			g.failWrite = true
		}
		return applyPass(ctx, h.store, g, h.deps(), "a1", []string{config.KindMachine}, false, false, "", &out)
	}, func(context.Context, time.Duration) error {
		if passes >= 4 {
			return errApplyStop
		}
		return nil
	}, func(err error) { reports = append(reports, err.Error()) })
	require.NoError(t, err)
	require.Equal(t, 4, passes)
	require.Len(t, reports, 2, "the same failure is reported once, and a later failure after a success is reported again")
	assert.Equal(t, reports[0], "UNAPPLIED rev=5: redis refused the write; run: nova-config apply")
	assert.Contains(t, reports[1], "UNAPPLIED rev=6:")
	assert.Equal(t, []string{"machine 3 5"}, g.stamps)
	assert.Equal(t, int64(5), h.redis.revs[config.KindMachine])
}

func TestApplyEveryWaitsWithoutRealTime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var got time.Duration
	passes := 0
	err := applyEvery(ctx, 5*time.Second, func(context.Context) error {
		passes++
		return nil
	}, func(_ context.Context, d time.Duration) error {
		got = d
		return errApplyStop
	}, func(error) { t.Fatal("a clean pass reports nothing") })
	require.NoError(t, err)
	assert.Equal(t, 1, passes)
	assert.Equal(t, 5*time.Second, got)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err = applyEvery(canceled, time.Second, func(context.Context) error {
		t.Fatal("a canceled loop does not take a pass")
		return nil
	}, func(context.Context, time.Duration) error { return nil }, func(error) { t.Fatal("a cancel is not a failure") })
	require.NoError(t, err)
}

func TestApplyLoopJudgmentWhenGapOlderThan60s(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		age   time.Duration
		judge bool
	}{
		{age: 90 * time.Second, judge: true},
		{age: 30 * time.Second, judge: false},
		{age: 60 * time.Second, judge: false},
	}
	for _, tc := range cases {
		t.Run(tc.age.String(), func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.store.Now = func() time.Time { return base }
			insertMachines(t, h, 5)
			h.redis.revs[config.KindMachine] = 3
			d := h.deps()
			d.now = func() time.Time { return base.Add(tc.age) }
			var out bytes.Buffer
			err := applyPass(context.Background(), h.store, h.redis, d, "a1", []string{config.KindMachine}, false, false, "", &out)
			require.NoError(t, err)
			text := out.String()
			assert.Contains(t, text, fmt.Sprintf("CONFIG GAP kind=machine store=5 applied=3 age=%s", ageText(tc.age)))
			assert.Contains(t, text, "CONFIG APPLY kind=machine")
			assert.Equal(t, int64(5), h.redis.revs[config.KindMachine])
			if tc.judge {
				assert.Contains(t, text, "JUDGMENT kind=machine store=5 applied=3 age=90s: the Redis copy is behind the store; run: nova-config apply")
				return
			}
			assert.NotContains(t, text, "JUDGMENT")
		})
	}
}

func TestApplyLoopOnePassThenStop(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 5)
	h.redis.revs[config.KindMachine] = 3
	var out, errb bytes.Buffer
	code := runApplyLoop(context.Background(), 5*time.Second, &out, &errb, h.deps(), conn{}, "a1", dsn, "127.0.0.1:6379", []string{config.KindMachine}, false, false, "--as is --actor", func(context.Context, time.Duration) error {
		return errApplyStop
	})
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, "", errb.String())
	text := out.String()
	assert.True(t, strings.HasPrefix(text, "NOTE --as is --actor\n"), "the alias note is said before the loop waits: %q", text)
	assert.Contains(t, text, "CONFIG APPLY kind=machine add=5 set=0 remove=0 rev=5")
	assert.Equal(t, int64(5), h.redis.revs[config.KindMachine])
}

func TestApplyLoopReportsAFleetGapOnceAndDoesNotOpenRedis(t *testing.T) {
	t.Parallel()

	h := newHarness()
	var out bytes.Buffer
	var reports []string
	passes := 0
	err := applyEvery(context.Background(), time.Second, func(ctx context.Context) error {
		passes++
		return oneLoopPass(ctx, &out, h.deps(), conn{}, "a1", dsn, "127.0.0.1:6379", config.KindNames(), false, false, "")
	}, func(context.Context, time.Duration) error {
		if passes >= 2 {
			return errApplyStop
		}
		return nil
	}, func(err error) { reports = append(reports, err.Error()) })
	require.NoError(t, err)
	require.Equal(t, 2, passes)
	require.Len(t, reports, 1)
	assert.Contains(t, reports[0], "endpoints are unset")
	assert.Equal(t, 0, h.redis.opens, "unset fleet endpoints refuse before Redis is opened")
}

func TestApplyEveryZeroRefuses(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	code, out, errs := h.run(t, "apply", "--every", "0s")
	assert.Equal(t, 2, code)
	assert.Equal(t, "", out)
	assert.Contains(t, errs, "--every wants a duration above zero, or omit it for one pass (apply install uses 5s)")
	assert.Equal(t, 0, h.opens)
	assert.Equal(t, 0, h.redis.opens)

	code, out, errs = h.run(t, "apply", "--every", "5s", "--check")
	assert.Equal(t, 2, code)
	assert.Equal(t, "", out)
	assert.Contains(t, errs, "a dry run is one pass without --every")
	assert.Equal(t, 0, h.opens)
	assert.Equal(t, 0, h.redis.opens)
}

func TestApplyInstallWritesUnitAndLoopRow(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 1)
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_PG_PASSWORD_ENV"] = "NOVA_SECRET_PG"
	h.env["NOVA_SECRET_PG"] = "s3cret-value"
	h.env["NOVA_PG_PASSWORD"] = "other-secret"
	dir := t.TempDir()
	var loaded, unloaded []string
	host := applyHost{
		goos: "linux",
		exe:  func() (string, error) { return "/usr/local/bin/nova-config", nil },
		home: func() (string, error) { return dir, nil },
		load: func(path string) error {
			loaded = append(loaded, path)
			return nil
		},
		unload: func(path string) error {
			unloaded = append(unloaded, path)
			return nil
		},
	}
	ctx := context.Background()
	var out, errb bytes.Buffer
	code := runApplyInstall(ctx, []string{"--machine", "m1", "--dir", dir}, &out, &errb, h.deps(), host)
	require.Equal(t, 0, code, "stderr %q stdout %q", errb.String(), out.String())
	assert.Equal(t, "", errb.String())
	text := out.String()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	require.NotEmpty(t, lines)
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "APPLIED rev="), "last line %q", lines[len(lines)-1])
	assert.Contains(t, text, "APPLY ADD kind=loop name=nova-config-apply")
	assert.Contains(t, text, "APPLY INSTALL OK unit="+dir+"/nova-config-apply.service written=true loaded=true loop=nova-config-apply every=5s")
	body, err := os.ReadFile(filepath.Join(dir, applyLoopService))
	require.NoError(t, err)
	unit := string(body)
	assert.Contains(t, unit, "--every")
	assert.Contains(t, unit, "5s")
	assert.Contains(t, unit, "/usr/local/bin/nova-config")
	assert.Contains(t, unit, "RestartSec=10")
	assert.Contains(t, unit, "NOVA_PG_PASSWORD_ENV=NOVA_SECRET_PG")
	assert.NotContains(t, unit, "s3cret-value", "the unit carries the variable name, not the password")
	assert.NotContains(t, unit, "other-secret")
	assert.NotContains(t, unit, "NOVA_PG_PASSWORD=")
	row, found, err := h.store.Get(ctx, config.KindLoop, applyLoopRow)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "m1", row.Fields["machine"])
	assert.Equal(t, "true", row.Fields["keepalive"])
	assert.Equal(t, "0", row.Fields["every"])
	assert.Equal(t, "true", row.Fields["enabled"])
	assert.Contains(t, row.Fields["argv"], "nova-config")
	assert.NotContains(t, row.Fields["argv"], "s3cret-value")
	assert.NotContains(t, row.Fields["argv"], "other-secret")
	require.NotNil(t, h.redis.views[config.KindLoop][applyLoopRow])
	rev, err := h.store.Rev(ctx, config.KindLoop)
	require.NoError(t, err)
	assert.Equal(t, rev, h.redis.revs[config.KindLoop])
	require.Len(t, loaded, 1)

	out.Reset()
	code = runApplyInstall(ctx, []string{"--machine", "m1", "--dir", dir}, &out, &errb, h.deps(), host)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "written=false")
	rev2, err := h.store.Rev(ctx, config.KindLoop)
	require.NoError(t, err)
	assert.Equal(t, rev, rev2, "the same loop row is left as it is")
	assert.Len(t, loaded, 2, "an install loads the unit again")

	out.Reset()
	code = runApplyUninstall(ctx, []string{"--dir", dir}, &out, &errb, h.deps(), host)
	require.Equal(t, 0, code, "stderr %q stdout %q", errb.String(), out.String())
	text = out.String()
	lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "APPLIED rev="), "last line %q", lines[len(lines)-1])
	assert.Contains(t, text, "APPLY UNINSTALL OK unit="+dir+"/nova-config-apply.service removed=true loop=nova-config-apply")
	_, err = os.Stat(filepath.Join(dir, applyLoopService))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, found, err = h.store.Get(ctx, config.KindLoop, applyLoopRow)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, h.redis.views[config.KindLoop][applyLoopRow])
	require.Len(t, unloaded, 1)
}

func TestApplyInstallDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 1)
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	dir := t.TempDir()
	loaded := 0
	host := applyHost{
		goos:   "linux",
		exe:    func() (string, error) { return "/usr/local/bin/nova-config", nil },
		home:   func() (string, error) { return dir, nil },
		load:   func(string) error { loaded++; return nil },
		unload: func(string) error { return nil },
	}
	var out, errb bytes.Buffer
	code := runApplyInstall(context.Background(), []string{"--machine", "m1", "--dir", dir, "--dry-run"}, &out, &errb, h.deps(), host)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "APPLY INSTALL DRY-RUN")
	assert.Contains(t, out.String(), "[Service]")
	_, err := os.Stat(filepath.Join(dir, applyLoopService))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, found, err := h.store.Get(context.Background(), config.KindLoop, applyLoopRow)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, 0, loaded)
	assert.Equal(t, 0, h.redis.opens)
}

func TestApplyInstallRefusals(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 1)
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	dir := t.TempDir()
	host := applyHost{
		goos:   "linux",
		exe:    func() (string, error) { return "/usr/local/bin/nova-config", nil },
		home:   func() (string, error) { return dir, nil },
		load:   func(string) error { return nil },
		unload: func(string) error { return nil },
	}
	var out, errb bytes.Buffer
	code := runApplyInstall(context.Background(), []string{"--machine", "m1", "--redis", "mem:local", "--dir", dir}, &out, &errb, h.deps(), host)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "this process's alone")
	assert.Equal(t, 0, h.opens)
	assert.Equal(t, 0, h.redis.opens)

	errb.Reset()
	code = runApplyInstall(context.Background(), []string{"--file", "local.json", "--machine", "m1", "--redis", "127.0.0.1:6379", "--actor", "a1"}, &out, &errb, h.deps(), host)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--file is this process's alone")
	assert.Equal(t, 0, h.opens)

	errb.Reset()
	code = runApplyInstall(context.Background(), []string{"--machine", "missing", "--dir", dir, "--redis", "127.0.0.1:6379"}, &out, &errb, h.deps(), host)
	assert.Equal(t, 1, code)
	assert.Contains(t, errb.String(), "names no machine row")
	_, err := os.Stat(filepath.Join(dir, applyLoopService))
	assert.ErrorIs(t, err, os.ErrNotExist)

	errb.Reset()
	code = runApplyInstall(context.Background(), []string{"--every", "0s", "--machine", "m1", "--dir", dir, "--redis", "127.0.0.1:6379"}, &out, &errb, h.deps(), host)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--every wants a duration above zero")

	errb.Reset()
	code = runApplyInstall(context.Background(), []string{"--machine", "m1", "--dir", dir, "--redis", "127.0.0.1:6379", "--pg", "postgres://user:s3cret-value@127.0.0.1:5432/nova"}, &out, &errb, h.deps(), host)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "carries a password")
	assert.NotContains(t, errb.String(), "s3cret-value")
}

func TestApplyInstallSaysUnappliedWhenRedisRefuses(t *testing.T) {
	t.Parallel()

	h := newHarness()
	insertMachines(t, h, 1)
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_PG_DSN"] = dsn
	dir := t.TempDir()
	host := applyHost{
		goos:   "linux",
		exe:    func() (string, error) { return "/usr/local/bin/nova-config", nil },
		home:   func() (string, error) { return dir, nil },
		load:   func(string) error { return nil },
		unload: func(string) error { return nil },
	}
	d := h.deps()
	g := &gateRedis{fakeRedis: h.redis, failWrite: true}
	d.openRedis = func(context.Context, string) (redisSide, error) { return g, nil }
	var out, errb bytes.Buffer
	code := runApplyInstall(context.Background(), []string{"--machine", "m1", "--dir", dir, "--redis", "127.0.0.1:6379"}, &out, &errb, d, host)
	assert.Equal(t, 1, code, errb.String())
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.NotEmpty(t, lines)
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "UNAPPLIED rev="), "last line %q", lines[len(lines)-1])
	assert.Contains(t, lines[len(lines)-1], "run: nova-config apply")
	assert.Empty(t, g.stamps)
	_, err := os.Stat(filepath.Join(dir, applyLoopService))
	assert.NoError(t, err, "the unit is written so the service can retry")
	_, found, err := h.store.Get(context.Background(), config.KindLoop, applyLoopRow)
	require.NoError(t, err)
	assert.True(t, found)
}

func TestApplyUnitTextIsLaunchdOrSystemd(t *testing.T) {
	t.Parallel()

	args := []string{"/usr/local/bin/nova-config", "apply", "--every", "5s", "--redis", "127.0.0.1:6379"}
	linux, err := applyUnitText("linux", "/usr/local/bin/nova-config", "", args, [][2]string{{"NOVA_PG_PASSWORD_ENV", "NOVA_SECRET_PG"}})
	require.NoError(t, err)
	assert.Contains(t, linux, "Description=nova-config apply loop (apply --every)")
	assert.Contains(t, linux, "RestartSec=10")
	assert.Contains(t, linux, "--every")
	assert.NotContains(t, linux, "s3cret-value")

	darwin, err := applyUnitText("darwin", "/usr/local/bin/nova-config", "/Users/me/Library/Logs/nova-config-apply.log", args, nil)
	require.NoError(t, err)
	assert.Contains(t, darwin, "nova-config.apply")
	assert.Contains(t, darwin, "ThrottleInterval")
	assert.Contains(t, darwin, "--every")

	_, err = applyUnitText("windows", "/usr/local/bin/nova-config", "", args, nil)
	assert.Error(t, err)
	_, err = applyUnitText("linux", "nova-config", "", args, nil)
	assert.Error(t, err)
}
