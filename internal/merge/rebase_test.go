package merge

import (
	"context"
	"strings"
	"testing"
)

type rebaseLaunchRecorder struct {
	command string
	args    []string
}

func (r *rebaseLaunchRecorder) Run(_ context.Context, _ string, command string, args ...string) (string, error) {
	r.command = command
	r.args = append([]string(nil), args...)
	return "", nil
}

func TestBenchLauncherPlanIsExactLaunchCommand(t *testing.T) {
	run := &rebaseLaunchRecorder{}
	launcher := BenchLauncher{Run: run}
	card := "/owned/cards/card-17.md"
	want := "flash-native-bench.sh space swarm-space /owned/cards/card-17.md card-17 900"

	if plan := launcher.LaunchCommand(card, "card-17"); plan != want {
		t.Fatalf("plan = %q, want production command %q", plan, want)
	}
	if err := launcher.Launch(card); err != nil {
		t.Fatal(err)
	}
	if actual := strings.Join(append([]string{run.command}, run.args...), " "); actual != want {
		t.Fatalf("actual launch = %q, want %q", actual, want)
	}
}
