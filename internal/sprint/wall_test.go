package sprint

import (
	"strings"
	"testing"
	"time"
)

func task(id, owner string, est int, paths ...string) Task {
	return Task{ID: id, Kind: KindFix, Ref: "o/n#1", Owner: owner, State: StateOpen, EstMinutes: est, Paths: paths}
}

// TestWallIsTheLongestLaneNotTheSum is the whole ruling in one assertion: three tasks on one
// owner and one on another is four hours of work and a three hour wall, and reporting the sum
// as if it were the time is the mistake this replaces.
func TestWallIsTheLongestLaneNotTheSum(t *testing.T) {
	tasks := []Task{
		task("a", "johnny", 60, "internal/swarm/provider.go"),
		task("b", "johnny", 60, "internal/merge/batch.go"),
		task("c", "johnny", 60, "internal/swarm/native/result.go"),
		task("d", "emma", 60, "cmd/nova-wake/serve.go"),
	}
	w, err := ComputeWall(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if w.Minutes != 180 {
		t.Fatalf("the wall is %d minutes, wants 180: one owner types one thing at a time", w.Minutes)
	}
	if w.WorkMinutes != 240 {
		t.Fatalf("the work is %d minutes, wants 240", w.WorkMinutes)
	}
	if w.Critical != "johnny" {
		t.Fatalf("the critical lane is %q, wants johnny", w.Critical)
	}
	if w.Line() != "~3h" {
		t.Fatalf("the wall line is %q, wants ~3h", w.Line())
	}
}

// TestATaskWaitsForAnotherOwnersTask: a true dependency crosses lanes, and the waiting lane
// is idle until it clears -- which is why an unowned decision at the head of a chain is worth
// seeing on the table.
func TestATaskWaitsForAnotherOwnersTask(t *testing.T) {
	decision := task("decision", "stella", 30)
	work := task("recut", "rowan", 60, "cmd/nova-pulse/cut.go")
	work.DependsOn = []string{"decision"}
	w, err := ComputeWall([]Task{decision, work})
	if err != nil {
		t.Fatal(err)
	}
	if w.Minutes != 90 {
		t.Fatalf("the wall is %d, wants 90: the recut cannot start before the decision", w.Minutes)
	}
	if w.Starts["recut"] != 30 {
		t.Fatalf("the recut starts at %d, wants 30", w.Starts["recut"])
	}
}

// TestSplittableIsWhatHasNoFileInCommon: the unit of parallelism is the file. Two tasks on
// one owner sharing a file are truly serial; two with disjoint paths are not, and only the
// second kind may be handed over.
func TestSplittableIsWhatHasNoFileInCommon(t *testing.T) {
	shared := []Task{
		task("head", "johnny", 60, "internal/merge/batch.go"),
		task("same-file", "johnny", 60, "internal/merge/batch.go"),
		task("other-file", "johnny", 60, "internal/swarm/provider.go"),
	}
	w, err := ComputeWall(shared)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range w.Splittable {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "other-file" {
		t.Fatalf("splittable is %v; only the task with no file in common may move", ids)
	}
}

// TestADependencyInsideALaneIsNotSplittable: a task that waits for its neighbour is serial
// wherever it runs, and offering it to another consumer would just move the wait.
func TestADependencyInsideALaneIsNotSplittable(t *testing.T) {
	head := task("head", "johnny", 60, "a.go")
	tail := task("tail", "johnny", 60, "b.go")
	tail.DependsOn = []string{"head"}
	w, err := ComputeWall([]Task{head, tail})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Splittable) != 0 {
		t.Fatalf("splittable is %v; a true dependency is not a false one", w.Splittable)
	}
}

// TestACycleIsARefusal: two tasks each waiting for the other have no honest wall, so the
// computation says so by name rather than printing a number.
func TestACycleIsARefusal(t *testing.T) {
	a := task("a", "johnny", 60, "a.go")
	b := task("b", "johnny", 60, "b.go")
	a.DependsOn = []string{"b"}
	b.DependsOn = []string{"a"}
	if _, err := ComputeWall([]Task{a, b}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("ComputeWall answered %v; a cycle is a refusal", err)
	}
}

// TestClosedTasksAreNotOnTheWall: the wall is what is LEFT.
func TestClosedTasksAreNotOnTheWall(t *testing.T) {
	done := task("done", "johnny", 600, "a.go")
	done.State = StateClosed
	w, err := ComputeWall([]Task{done, task("open", "johnny", 30, "b.go")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Minutes != 30 {
		t.Fatalf("the wall is %d, wants 30", w.Minutes)
	}
}

// TestAfterMovingIsTheNumberPutInFrontOfTheOwner: "yes -> wall ~2 h, no -> ~4 h" is a
// computation, not a feeling, and it changes nothing in the store.
func TestAfterMovingIsTheNumberPutInFrontOfTheOwner(t *testing.T) {
	tasks := []Task{
		task("a5", "johnny", 90, "internal/swarm/provider.go"),
		task("m2550", "johnny", 90, "internal/merge/batch.go"),
		task("m2548", "johnny", 60, "internal/swarm/native/result.go"),
		task("recut", "rowan", 120, "cmd/nova-pulse/cut.go"),
	}
	before, err := ComputeWall(tasks)
	if err != nil {
		t.Fatal(err)
	}
	after, err := AfterMoving(tasks, []string{"m2550", "m2548"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Minutes != 240 || after.Minutes != 120 {
		t.Fatalf("the wall went %d -> %d, wants 240 -> 120", before.Minutes, after.Minutes)
	}
	if tasks[1].Owner != "johnny" {
		t.Fatalf("AfterMoving changed the store's task: owner is now %q", tasks[1].Owner)
	}
}

// TestMinutesReadsAsHours pins the format the line carries.
func TestMinutesReadsAsHours(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{{0, "~0m"}, {45, "~45m"}, {60, "~1h"}, {90, "~1.5h"}, {240, "~4h"}, {470, "~7.8h"}} {
		if got := Minutes(tc.in); got != tc.want {
			t.Fatalf("Minutes(%d) is %q, wants %q", tc.in, got, tc.want)
		}
	}
}

// TestLineIsTheWholeInterface: `14/23 61% -> ~4h`, and nothing else.
func TestLineIsTheWholeInterface(t *testing.T) {
	var tasks []Task
	for i := 0; i < 14; i++ {
		done := task("done", "johnny", 30, "a.go")
		done.ID = "done-" + string(rune('a'+i))
		done.State = StateClosed
		tasks = append(tasks, done)
	}
	tasks = append(tasks,
		task("o1", "johnny", 90, "internal/swarm/provider.go"),
		task("o2", "johnny", 90, "internal/merge/batch.go"),
		task("o3", "johnny", 60, "internal/swarm/native/result.go"),
		task("o4", "stella", 30),
		task("o5", "rowan", 60, "cmd/nova-pulse/cut.go"),
		task("o6", "stella", 10),
		task("o7", "emma", 10),
		task("o8", "emma", 60, "cmd/nova-pulse/cut_kind.go"),
		task("o9", "rowan", 60, "internal/merge/land.go"),
	)
	line, err := Line(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if line != "14/23 61% -> ~4h" {
		t.Fatalf("the line is %q, wants 14/23 61%% -> ~4h", line)
	}
}

// TestLineWithLeftAddsTheClockOnlyWhenThereIsOne: the estimate and the clock are two facts.
func TestLineWithLeftAddsTheClockOnlyWhenThereIsOne(t *testing.T) {
	now := time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC)
	tasks := []Task{task("a", "johnny", 60, "a.go")}
	line, err := LineWithLeft(Sprint{Name: "s"}, tasks, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, "left") {
		t.Fatalf("the line is %q; a sprint with no planned close has no clock", line)
	}
	line, err = LineWithLeft(Sprint{Name: "s", PlannedCloseAt: now.Add(3 * time.Hour)}, tasks, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(line, "3h left") {
		t.Fatalf("the line is %q, wants a `3h left` on the end", line)
	}
}

// TestTableNamesEachSprintWhenSeveralAreOpen is Glenn's second format: one sprint, no name;
// several, a table with the name leftmost.
func TestTableNamesEachSprintWhenSeveralAreOpen(t *testing.T) {
	got := Table([]TableRow{{Name: "fixes-2026-09-22", Line: "14/23 61% -> ~4h"}, {Name: "week-39", Line: "3/9 33% -> ~12h"}})
	if !strings.HasPrefix(got, "fixes-2026-09-22  14/23") {
		t.Fatalf("the table is %q; the sprint name is the leftmost column", got)
	}
	if !strings.Contains(got, "week-39           3/9") {
		t.Fatalf("the table's columns do not line up:\n%s", got)
	}
}
