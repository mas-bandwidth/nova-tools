package main

// The two things fill does to a machine -- the capacity probe and the per-card launch --
// are built from the machine's REGISTRY ROW and from nothing else: the ssh target is the
// row's ssh column, and the capacity formula is chosen by the row's os. Neither is probed
// and neither is guessed from the bench name.
//
// Every test here is over the argv builders, which are pure: no test opens an ssh
// connection and no test spawns a process.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// airMachine is the Air's registry row as internal/fleet reads it.
var airMachine = fleet.Machine{
	Name: "air", SSH: "glenn@100.117.59.68", OS: "darwin", Arch: "arm64",
	Roles: []string{"bench", "runner"}, Seat: "swarm-air", Cores: 8,
}

var hulkMachine = fleet.Machine{
	Name: "hulk", SSH: "hulk", OS: "linux", Arch: "x64",
	Roles: []string{"bench"}, Seat: "swarm-hulk", Cores: 64,
}

// TestTheCapacityFormulaIsChosenByTheRowsOS: /proc and nproc are Linux, and a darwin bench
// answered `exit status 255` forever because the script it was handed could not run there.
func TestTheCapacityFormulaIsChosenByTheRowsOS(t *testing.T) {
	linux, err := capacityScript("linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nproc", "/proc/loadavg", "/proc/meminfo", "df -BG"} {
		if !strings.Contains(linux, want) {
			t.Errorf("the linux formula lost %q: %s", want, linux)
		}
	}
	darwin, err := capacityScript("darwin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sysctl -n hw.ncpu", "sysctl -n vm.loadavg", "sysctl -n hw.memsize", "df -g"} {
		if !strings.Contains(darwin, want) {
			t.Errorf("the darwin formula lost %q: %s", want, darwin)
		}
	}
	for _, never := range []string{"nproc", "/proc/"} {
		if strings.Contains(darwin, never) {
			t.Errorf("the darwin formula still reads %q: %s", never, darwin)
		}
	}
}

// TestAnOSWithNoFormulaIsRefusedByName: the row says what the machine is, so an os the
// tool has no formula for is a named refusal and not a Linux script sent hopefully.
func TestAnOSWithNoFormulaIsRefusedByName(t *testing.T) {
	_, err := capacityScript("plan9")
	if err == nil {
		t.Fatal("an unknown os was handed a formula")
	}
	for _, want := range []string{"plan9", "linux", "darwin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
}

// TestTheCapacityArgvSshesTheRegistrysTarget: `air` is a row name, not a hostname. The
// probe goes to the ssh column.
func TestTheCapacityArgvSshesTheRegistrysTarget(t *testing.T) {
	argv, err := capacityArgv(airMachine)
	if err != nil {
		t.Fatal(err)
	}
	if argv[len(argv)-2] != "glenn@100.117.59.68" {
		t.Fatalf("the ssh target is %q, want the registry's ssh column; argv=%q", argv[len(argv)-2], argv)
	}
	if !strings.Contains(argv[len(argv)-1], "sysctl -n hw.ncpu") {
		t.Errorf("the darwin row was handed %q", argv[len(argv)-1])
	}
	if argv[0] != "-n" {
		t.Errorf("the probe does not read the card's stdin: argv=%q", argv)
	}
	linuxArgv, err := capacityArgv(hulkMachine)
	if err != nil {
		t.Fatal(err)
	}
	if linuxArgv[len(linuxArgv)-2] != "hulk" {
		t.Errorf("the linux target is %q, want hulk", linuxArgv[len(linuxArgv)-2])
	}
}

// TestARowWithNoSSHTargetIsRefused: the registry validates the column, so this is the
// belt-and-braces case -- a row handed in by a test or a hand-built machine.
func TestARowWithNoSSHTargetIsRefused(t *testing.T) {
	if _, err := capacityArgv(fleet.Machine{Name: "air", OS: "darwin"}); err == nil {
		t.Fatal("a row with no ssh target was sshed to anyway")
	}
}

// TestTheLauncherRunsNovaSwarmNativeOnTheBench: flash-native-bench.sh was retired on
// 2026-09-18 and is on no machine. The launcher is `nova-swarm native`, over the same ssh
// the capacity probe uses, in the bench's own swarm root.
func TestTheLauncherRunsNovaSwarmNativeOnTheBench(t *testing.T) {
	l := benchLauncher{harness: "opencode", model: "deepseek/deepseek-chat"}
	argv, err := l.nativeArgv(airMachine, "card-9601.md")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(argv, " ")
	if argv[len(argv)-2] != "glenn@100.117.59.68" && !strings.Contains(line, "glenn@100.117.59.68") {
		t.Fatalf("the launch does not ssh the registry's target: %q", line)
	}
	for _, want := range []string{
		"nova-swarm native",
		"--harness opencode",
		"--model deepseek/deepseek-chat",
		"--label card-9601",
		"--deadline 2400s",
		"$HOME/swarm-air",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the launch argv lost %q: %s", want, line)
		}
	}
	if strings.Contains(line, "flash-native-bench.sh") {
		t.Errorf("the retired script is still the launcher: %s", line)
	}
}

// TestTheLauncherRefusesWithoutAHarness: it does not guess a harness or a model, it says
// which flag to pass.
func TestTheLauncherRefusesWithoutAHarness(t *testing.T) {
	_, err := benchLauncher{model: "deepseek/deepseek-chat"}.nativeArgv(airMachine, "card-1.md")
	if err == nil || !strings.Contains(err.Error(), "--harness") {
		t.Fatalf("err = %v, want a refusal naming --harness", err)
	}
	_, err = benchLauncher{harness: "opencode"}.nativeArgv(airMachine, "card-1.md")
	if err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("err = %v, want a refusal naming --model", err)
	}
}

// TestTheSwarmRootComesFromTheRowsSeat: swarm-air is the Air's seat, so $HOME/swarm-air is
// its swarm root; --swarm-root overrides it, and a row with no seat and no flag is refused.
func TestTheSwarmRootComesFromTheRowsSeat(t *testing.T) {
	l := benchLauncher{harness: "opencode", model: "m/m", root: "/data/swarm"}
	argv, err := l.nativeArgv(airMachine, "card-1.md")
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.Join(argv, " "); !strings.Contains(line, "/data/swarm") || strings.Contains(line, "swarm-air") {
		t.Errorf("--swarm-root did not override the seat: %s", line)
	}
	seatless := fleet.Machine{Name: "air", SSH: "a@b", OS: "darwin"}
	if _, err := (benchLauncher{harness: "opencode", model: "m/m"}).nativeArgv(seatless, "card-1.md"); err == nil {
		t.Fatal("a row with no seat guessed a swarm root")
	}
}

// TestTheOverrideLauncherKeepsTheOldArgv: --launcher <path> is the escape hatch and runs a
// local program with the hand loop's own five arguments.
func TestTheOverrideLauncherKeepsTheOldArgv(t *testing.T) {
	l := benchLauncher{bin: "./bin/echo-card"}
	name, argv := l.overrideArgv(airMachine, "/q/launched/card-9601.md")
	if name != "./bin/echo-card" {
		t.Fatalf("the override program is %q", name)
	}
	want := []string{"air", "swarm-air", "/q/launched/card-9601.md", "card-9601", "2400"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("the override argv is %q, want %q", argv, want)
	}
}

// TestACapacityFailureCarriesTheChildsLastLine: io.Discard on the probe's stderr was the
// whole dogfood edge -- `FILL NOTE ... exit status 255` with the reason thrown away. The
// tail is bounded and its last line joins the exit status.
func TestACapacityFailureCarriesTheChildsLastLine(t *testing.T) {
	said := &tail{}
	said.Write([]byte(strings.Repeat("x", tailBytes*2) + "\n"))
	said.Write([]byte("ssh: connect to host 100.117.59.68 port 22: Connection refused\n"))
	if len(said.buf) > tailBytes {
		t.Fatalf("the tail kept %d bytes, want at most %d", len(said.buf), tailBytes)
	}
	err := said.wrap(errStatus255{})
	if !strings.Contains(err.Error(), "exit status 255") || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("the wrapped error is %q, want the status and the reason", err)
	}
}

type errStatus255 struct{}

func (errStatus255) Error() string { return "exit status 255" }
