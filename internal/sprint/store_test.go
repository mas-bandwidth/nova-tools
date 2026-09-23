package sprint

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestTheFakeStoreIsStrictLikeRedis (AGENTS.md): a lenient fake ships the verb broken on the
// one store that matters, so every refusal RedisStore makes, the fake makes too.
func TestTheFakeStoreIsStrictLikeRedis(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()

	if err := f.PutSprint(ctx, Sprint{Name: "fixes: today"}); err == nil || !strings.Contains(err.Error(), `": "`) {
		t.Fatalf("PutSprint answered %v; a name carrying the one-line separator is refused", err)
	}
	if err := f.PutSprint(ctx, Sprint{Name: ""}); err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("PutSprint answered %v; an empty name is a refusal", err)
	}
	if err := f.PutTask(ctx, Task{ID: "t", Kind: "vibes"}); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("PutTask answered %v; an unknown kind is refused by name", err)
	}
	if err := f.PutTask(ctx, Task{ID: "t", Kind: KindFix, State: "nearly"}); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("PutTask answered %v; a state outside COWS is refused", err)
	}
	if err := f.AddTask(ctx, "never-opened", "t"); err == nil || !strings.Contains(err.Error(), "no sprint") {
		t.Fatalf("AddTask answered %v; a task cannot join a sprint nobody opened", err)
	}
	if _, err := f.GetTask(ctx, "nope"); err == nil {
		t.Fatalf("GetTask invented a task")
	}
	if _, err := f.Place(ctx, "", Task{ID: "t"}); err == nil {
		t.Fatalf("Place took an empty queue name")
	}
}

// TestTheStoreRoundTripsEveryField pins the Redis hash's fields against the struct: a field
// that is written and not read is a field that silently becomes empty on the next status.
func TestTheStoreRoundTripsEveryField(t *testing.T) {
	want := Task{
		ID: "tools-2550", Kind: KindFix, Ref: "mas-bandwidth/nova-tools#2550", Owner: "johnny",
		Route: "bench:hulk", RouteReason: "leg go, locality datacenter", State: StateWorking,
		EstMinutes: 90, LeasedAt: time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC),
		DoneAt: time.Date(2026, 9, 22, 16, 30, 0, 0, time.UTC), Actual: 90,
		Evidence: "nova-tools#2550 merged abc123", DependsOn: []string{"a5-2544"},
		Paths: []string{"internal/merge/batch.go"}, Leg: "go", Locality: "datacenter",
		Isolation: "net", Routes: []string{"flash", "pro"}, CostCeilingUSD: 1.5,
		Repo: "mas-bandwidth/nova-tools", Base: "dev", Reader: "johnny", Priority: true,
		CreatedAt: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC),
	}
	fields := taskFields(want)
	m := map[string]string{}
	for k, v := range fields {
		switch value := v.(type) {
		case string:
			m[k] = value
		case int:
			m[k] = itoa(value)
		default:
			t.Fatalf("field %s is %T; the store holds strings and counts", k, v)
		}
	}
	got := taskFrom(want.ID, m)
	if got.Kind != want.Kind || got.Ref != want.Ref || got.Owner != want.Owner || got.Route != want.Route ||
		got.RouteReason != want.RouteReason || got.State != want.State || got.EstMinutes != want.EstMinutes ||
		!got.LeasedAt.Equal(want.LeasedAt) || !got.DoneAt.Equal(want.DoneAt) || got.Actual != want.Actual ||
		got.Evidence != want.Evidence || strings.Join(got.DependsOn, ",") != strings.Join(want.DependsOn, ",") ||
		strings.Join(got.Paths, ",") != strings.Join(want.Paths, ",") || got.Leg != want.Leg ||
		got.Locality != want.Locality || got.Isolation != want.Isolation ||
		strings.Join(got.Routes, ",") != strings.Join(want.Routes, ",") || got.CostCeilingUSD != want.CostCeilingUSD ||
		got.Repo != want.Repo || got.Base != want.Base || got.Reader != want.Reader ||
		got.Priority != want.Priority || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("the task did not survive the hash:\n got %+v\nwant %+v", got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// TestPackagesIsHowASeamIsFound: the split rule is about packages, so what counts as one is
// pinned here rather than implied.
func TestPackagesIsHowASeamIsFound(t *testing.T) {
	task := Task{Paths: []string{"internal/merge/batch.go", "internal/merge/land.go", "cmd/nova-pulse/sprint.go", "Makefile"}}
	got := strings.Join(task.Packages(), " ")
	if got != "Makefile cmd/nova-pulse internal/merge" {
		t.Fatalf("the packages are %q", got)
	}
}

// TestCalibrationCountsAMissingActualAsMissing, never as a zero that pulls the average down.
func TestCalibrationCountsAMissingActualAsMissing(t *testing.T) {
	tasks := []Task{
		{ID: "a", Kind: KindFix, Owner: "johnny", State: StateClosed, EstMinutes: 60, Actual: 90},
		{ID: "b", Kind: KindFix, Owner: "johnny", State: StateClosed, EstMinutes: 60, Actual: 0},
		{ID: "c", Kind: KindRead, Owner: "stella", State: StateOpen, EstMinutes: 10},
	}
	c := Calibrate(tasks)
	if len(c.ByKind) != 1 || c.ByKind[0].N != 1 || c.ByKind[0].Missing != 1 {
		t.Fatalf("the kind rows are %+v; one measured, one missing", c.ByKind)
	}
	if c.ByKind[0].ErrorPct() != 50 {
		t.Fatalf("the error is %d%%, wants +50%%", c.ByKind[0].ErrorPct())
	}
	if got := c.Suggest[KindFix]; got != 90 {
		t.Fatalf("the suggestion is %d, wants the measured 90", got)
	}
	if !strings.Contains(c.String(), "SUGGEST fix 90m (default 120m)") {
		t.Fatalf("the report does not print the suggestion beside today's default:\n%s", c.String())
	}
}

// TestPercentOfNothingIsNotDone: an empty sprint is 0%, never 100%.
func TestPercentOfNothingIsNotDone(t *testing.T) {
	if Percent(0, 0) != 0 {
		t.Fatalf("Percent(0,0) is %d", Percent(0, 0))
	}
	if Percent(14, 23) != 61 {
		t.Fatalf("Percent(14,23) is %d, wants 61", Percent(14, 23))
	}
}
