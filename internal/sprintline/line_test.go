package sprintline

import (
	"strings"
	"testing"
)

func TestEvaluateOutputChangesXAndY(t *testing.T) {
	t.Parallel()

	cal := "KIND fix n=2 est=~4h actual=~3h error=-25%\nSUGGEST fix 90m (default 120m)\n"
	tasks := []Task{
		{ID: "gate-fix-holds", Kind: "fix", Owner: "johnny", State: "open", EstMinutes: 120},
		{ID: "wait-verb", Kind: "fix", Owner: "emma", State: "open", EstMinutes: 120, DependsOn: []string{"gate-fix-holds"}},
	}
	a, err := Compose(evalLine(42, 26, 61), cal, tasks)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compose(evalLine(40, 27, 99), cal, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, "26/42 ") || !strings.HasPrefix(b, "27/40 ") {
		t.Fatalf("evaluate output did not change x and y:\n A %s\n B %s", a, b)
	}
	if !strings.Contains(b, " 99% -> ") {
		t.Fatalf("percent was recomputed instead of taken from SET DONE: %s", b)
	}
	const (
		units = 42
		done  = 26
	)
	formula := (units-done)*10/3 + 12
	if strings.Contains(a, "~"+itoa(formula)+"m") {
		t.Fatalf("eta is the hand formula ~%dm: %s", formula, a)
	}
	if !strings.HasSuffix(a, "-> ~3h") {
		t.Fatalf("eta = %q, want the calibrated wall ~3h (two 90m tasks, one depending on the other)", a)
	}
}

func TestHandMarkedReceiptIsNotAnInput(t *testing.T) {
	t.Parallel()

	// The sexp is not a parameter of Compose. This test is the negative the
	// command runs too: a caller that still has the file must not be able to
	// feed it in here. Counting :status is what sprint-xy did, and the two
	// strings disagree under that count, so a line built from them would move.
	marked := `(unit "a12-receipt" :status "done" :evidence "done receipt 2026-09-22")
(unit "tools-2547" :status "landed")`
	open := `(unit "a12-receipt" :status "open" :evidence "not a done receipt")`
	if statusMarks(marked) == statusMarks(open) {
		t.Fatal("fixture does not hand-mark a receipt; the old count would not have moved")
	}
	cal := "SUGGEST fix 90m (default 120m)\n"
	tasks := []Task{{ID: "a", Kind: "fix", Owner: "johnny", State: "open", EstMinutes: 120}}
	line, err := Compose(evalLine(42, 26, 61), cal, tasks)
	if err != nil {
		t.Fatal(err)
	}
	// Neither sexp is readable from the line. 26 is the evaluate count, not
	// the two marks and not the zero marks.
	if strings.HasPrefix(line, "2/") || strings.HasPrefix(line, "0/") || strings.HasPrefix(line, "1/") {
		t.Fatalf("line %q follows a :status count, not SET DONE done=26", line)
	}
	if line != "26/42 61% -> ~1.5h" {
		t.Fatalf("line = %q, want 26/42 61%% -> ~1.5h from evaluate and the 90m actual", line)
	}
}

func TestWallUsesKindActualNotTheSumOrTheStoredEstimate(t *testing.T) {
	t.Parallel()

	suggest := map[string]int{"fix": 90}
	parallel := []Task{
		{ID: "a", Kind: "fix", Owner: "johnny", State: "open", EstMinutes: 120},
		{ID: "b", Kind: "fix", Owner: "emma", State: "open", EstMinutes: 120},
	}
	got, err := WallMinutes(parallel, suggest)
	if err != nil {
		t.Fatal(err)
	}
	if got != 90 {
		t.Fatalf("parallel wall = %d, want 90 (one calibrated actual), not the sum 180 and not the stored 120", got)
	}
	same := []Task{
		{ID: "a", Kind: "fix", Owner: "Johnny", State: "open", EstMinutes: 120},
		{ID: "b", Kind: "fix", Owner: "johnny", State: "working", EstMinutes: 120},
	}
	got, err = WallMinutes(same, suggest)
	if err != nil {
		t.Fatal(err)
	}
	if got != 180 {
		t.Fatalf("one owner's lane = %d, want 180 serial minutes", got)
	}
	kept, err := WallMinutes([]Task{{ID: "a", Kind: "read", Owner: "rowan", State: "open", EstMinutes: 12}}, suggest)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 12 {
		t.Fatalf("a kind with no measurement kept %d, want the stored 12", kept)
	}
	closed, err := WallMinutes([]Task{
		{ID: "a", Kind: "fix", Owner: "johnny", State: "closed", EstMinutes: 100},
		{ID: "b", Kind: "fix", Owner: "emma", State: "open", EstMinutes: 30, DependsOn: []string{"a"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 30 {
		t.Fatalf("a closed dependency still waited: wall %d, want 30", closed)
	}
	_, err = WallMinutes([]Task{
		{ID: "a", Kind: "fix", Owner: "johnny", State: "open", DependsOn: []string{"b"}},
		{ID: "b", Kind: "fix", Owner: "emma", State: "open", DependsOn: []string{"a"}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle err = %v, want a cycle refusal", err)
	}
}

func TestOpenFileRejectsAStatusFraction(t *testing.T) {
	t.Parallel()

	_, err := ParseOpenFile("14/23 61% -> ~9h\n")
	if err == nil {
		t.Fatal("an open file accepted a status fraction; that is the hand line, not a task")
	}
	tasks, err := ParseStatus("14/23 61% -> ~9h\nTASK id=a kind=fix owner=johnny state=open est=120\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "a" || len(tasks[0].Paths) != 0 {
		t.Fatalf("status parse = %+v, want the one TASK line and not the fraction", tasks)
	}
	withPath, err := ParseOpenFile("TASK id=a kind=fix owner=johnny est=10 paths=cmd/a.go,cmd/b.go depends=z\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(withPath) != 1 || len(withPath[0].Paths) != 2 || len(withPath[0].DependsOn) != 1 || withPath[0].DependsOn[0] != "z" {
		t.Fatalf("paths/depends = %+v", withPath)
	}
}

func TestStatusRowWithoutKindOrDependsIsRefused(t *testing.T) {
	t.Parallel()

	_, err := ParseStatus("Open a mas-bandwidth/nova-tools#1 owner=johnny route=- est=~2h\n")
	if err == nil || !strings.Contains(err.Error(), "kind=") {
		t.Fatalf("err = %v, want a kind= refusal", err)
	}
	_, err = ParseStatus("Open a mas-bandwidth/nova-tools#1 owner=johnny route=- est=~2h kind=fix\n")
	if err == nil || !strings.Contains(err.Error(), "depends=") {
		t.Fatalf("err = %v, want a depends= refusal", err)
	}
	_, err = ParseStatus("Open a mas-bandwidth/nova-tools#1 owner=johnny route=- est=~2h kind=- depends=-\n")
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("err = %v, want an empty kind refused", err)
	}
}

func TestMinutesMatchesTheSprintLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		m    int
		want string
	}{
		{0, "~0m"},
		{45, "~45m"},
		{60, "~1h"},
		{90, "~1.5h"},
		{180, "~3h"},
	} {
		if got := Minutes(tc.m); got != tc.want {
			t.Errorf("Minutes(%d) = %s, want %s", tc.m, got, tc.want)
		}
	}
}

func evalLine(units, done, percent int) string {
	return "SET EVAL unit=only criterion=c1 kind=landed subject=pr:example.invalid/nova-tools#1 holds=yes why=merged\n" +
		"SET OK units=" + itoa(units) + " ready=7 blocked=9 owned=0\n" +
		"SET DONE done=" + itoa(done) + " percent=" + itoa(percent) + "\n"
}

func statusMarks(sexp string) int {
	return strings.Count(sexp, `:status "done"`) + strings.Count(sexp, `:status "landed"`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
