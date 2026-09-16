package pulse

// The fleet verbs: the machines that run our work, and the things a person did to them by
// hand on 2026-09-16 (issue #828 class E; the ad hoc inventory on #854, 19:05Z). Standing a
// bench up was a page of ssh: a Go toolchain unpacked under ~/sdk and symlinked where the
// workflow looks for it, one actions-runner tarball fetched and untarred N times, N
// `config.sh` calls each with a registration token minted a moment earlier, and N services
// started in whichever of three ways that machine starts things. Restarting one runner was
// three different commands depending on the machine. Probing a new machine before trusting
// it was a person reading `go version` and an `ls` and then running a test by eye. And when
// ten runs held eight Studio runners busy for two hours on 2026-09-16, freeing them was a
// person paging through the API by hand.
//
// Four verbs now, each one line out:
//
//	fleet add <name> --host <ssh> --runners <n> --labels <a,b> --queue <dir>
//	fleet restart <runner> --queue <dir>
//	fleet probe <name> --queue <dir>
//	fleet phantoms --repo <o/n> [--force-cancel]   (phantoms.go)
//
// <queue>/fleet.tsv is the machines and <queue>/runner-services.tsv (runners.go, rule E2) is
// the runners: `add` writes both, so a runner it stands up is one the reaper can already
// restart, and `restart` is the same service map the reaper drives rather than a second
// vocabulary for the same three commands.
//
// Everything that leaves this machine goes through Shell and Tokens, so no test here starts
// a runner, mints a token, or reaches the network.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FleetFile is the machine table: one row per machine, eleven fields,
// name<TAB>host<TAB>user<TAB>cores<TAB>ram_gb<TAB>os<TAB>runner_dir_pattern<TAB>service_kind<TAB>labels<TAB>card_slots<TAB>network.
const FleetFile = "fleet.tsv"

// DefaultGoVersion is the toolchain `add` puts on a new machine when --go is not given. The
// workflow looks for the compiler at ~/go/bin/go, so whatever version lands there, lands
// there by that name.
const DefaultGoVersion = "1.26.5"

// DefaultRunnerVersion is the actions-runner release `add` unpacks. It is a flag because a
// runner too old for the service refuses to configure, and that refusal must be one line
// and not a mystery.
const DefaultRunnerVersion = "2.328.0"

// DefaultRunnerDirPattern is where a runner lives on a machine; %d is its index, one-based,
// and the pattern is recorded per machine because the Studio and Space differ.
const DefaultRunnerDirPattern = "~/runner-nova-tools-%d"

// DefaultProbeCap is the six-minute cap a probe's card-shaped job runs under: the same cap
// a real card gets, because a probe that passes under a longer one proves nothing.
const DefaultProbeCap = 6 * time.Minute

// defaultProbeKeys are the files a bench needs before it can run a card. Only their
// presence is ever reported, never a byte of their contents.
var defaultProbeKeys = []string{
	"~/.config/gh/hosts.yml",
	"~/.ssh/id_ed25519",
	"~/keys/openrouter.key",
}

// Machine is one row of FleetFile.
type Machine struct {
	Name        string
	Host        string // the ssh destination; "-" or empty is this machine
	User        string
	Cores       int
	RAMGB       int
	OS          string
	RunnerDir   string // pattern, %d is the runner index
	ServiceKind string // systemd | svc | runsh
	Labels      []string
	CardSlots   int
	Network     string
}

// Shell is the one way out of this process onto a machine: one command, one stdin, the
// combined output back. The real one is ssh (or sh when the machine is this one); a test
// drives a fake and nothing leaves the process.
//
// stdin is how a secret travels: it is never a command-line argument here, never printed,
// and never written to a log.
type Shell interface {
	Run(host, command, stdin string) (string, error)
}

// Tokens mints a runner registration token for a repository. The real one is one bounded
// `gh api -X POST repos/<o/n>/actions/runners/registration-token`; the token it returns is
// carried to the machine over stdin and is redacted out of everything this package prints.
type Tokens interface {
	Registration(repo string) (string, error)
}

// SSHShell is the real Shell: one bounded ssh per command, or sh -c when the host is this
// machine. BatchMode means a machine that wants a password is a refusal and not a hang.
type SSHShell struct{ Timeout time.Duration }

func (s SSHShell) Run(host, command, stdin string) (string, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if host == "" || host == "-" {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	} else {
		// The script goes over stdin ahead of whatever the script itself reads, so a `ps`
		// on the far side shows `bash -s` and never the script -- and never a secret the
		// script is about to read.
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, "bash -s")
		stdin = command + "\n" + stdin
	}
	cmd.Stdin = strings.NewReader(stdin)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return string(raw), fmt.Errorf("%s: %w", oneline.Cap(headLine(string(raw)), 200), err)
	}
	return string(raw), nil
}

// GHTokens is the real Tokens: one bounded gh call per runner. A registration token is
// single-use and short-lived, so one is minted per runner and none is ever kept.
type GHTokens struct{ Timeout time.Duration }

func (g GHTokens) Registration(repo string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "gh", "api", "-X", "POST",
		"repos/"+repo+"/actions/runners/registration-token", "--jq", ".token").Output()
	if err != nil {
		// The error carries the gh call, never the body: a body here would be the token.
		return "", fmt.Errorf("gh api -X POST repos/%s/actions/runners/registration-token: %w", repo, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("the registration token for %s came back empty", repo)
	}
	return token, nil
}

// FleetAddInput is `fleet add`, held apart from flag parsing.
type FleetAddInput struct {
	Name    string // the machine's name in the fleet, and the stem of every runner's name
	Host    string // the ssh destination
	Repo    string // owner/name the runners register against
	Queue   string // the queue directory holding fleet.tsv and runner-services.tsv
	Runners int
	Labels  []string

	Go            string // toolchain version, default DefaultGoVersion
	RunnerVersion string // actions-runner release, default DefaultRunnerVersion
	User          string
	Cores         int
	RAMGB         int
	OS            string
	RunnerDir     string // pattern, default DefaultRunnerDirPattern
	ServiceKind   string // systemd | svc | runsh; empty asks the machine
	CardSlots     int
	Network       string

	DryRun bool
	Shell  Shell
	Tokens Tokens
	Now    func() time.Time
	Stdout io.Writer
	Stderr io.Writer
}

// FleetAdd stands a machine up: the toolchain where the workflow looks for it, the runner
// tarball once, N configured runners under their own names and labels, N services started
// the way this machine starts things, and the rows that make all of it addressable
// afterwards. One FLEET line, counts not lists.
func FleetAdd(in FleetAddInput) int {
	if bad := fleetAddRefusal(in); bad != "" {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: %s\n", bad)
		return 2
	}
	goVersion := orDefault(in.Go, DefaultGoVersion)
	runnerVersion := orDefault(in.RunnerVersion, DefaultRunnerVersion)
	pattern := orDefault(in.RunnerDir, DefaultRunnerDirPattern)
	labels := fleetLabels(in.Labels)

	if in.DryRun {
		// Nothing leaves this process: the same line, the same counts, the plan readable
		// before it is trusted.
		fmt.Fprintf(in.Stdout, "FLEET add name=%s host=%s runners=%d configured=%d started=%d go=%s kind=%s labels=%s dry-run=true\n",
			oneline.Field(in.Name), oneline.Field(in.Host), in.Runners, in.Runners, in.Runners,
			oneline.Field(goVersion), oneline.Field(orDefault(in.ServiceKind, "detect")), oneline.Field(labels))
		return 0
	}

	notes := &boundedNotes{w: in.Stderr, kind: "FLEET", max: 5}

	// 1. The toolchain, where the workflow looks for it: ~/sdk/go<ver> unpacked once and
	// ~/go/bin/go pointing at it. A machine that already has it does nothing.
	if _, err := in.Shell.Run(in.Host, goInstallScript(goVersion), ""); err != nil {
		notes.note("FLEET NOTE go %s could not be ensured on %s: %s", goVersion, oneline.Field(in.Name), oneline.Err(err))
	}

	// 2. The runner tarball, once per machine, into a cache the N unpacks read.
	if _, err := in.Shell.Run(in.Host, runnerTarballScript(runnerVersion), ""); err != nil {
		notes.note("FLEET NOTE the actions-runner %s tarball could not be fetched: %s", runnerVersion, oneline.Err(err))
	}

	kind := normalizeServiceKind(in.ServiceKind)
	if kind == "" {
		kind = detectServiceKind(in)
	}

	configured, started := 0, 0
	services := make([]RunnerService, 0, in.Runners)
	for i := 1; i <= in.Runners; i++ {
		dir := runnerDir(pattern, i)
		name := fmt.Sprintf("%s-nova-%d", in.Name, i)

		// A registration token per runner: single-use, minted a moment before it is spent,
		// carried over stdin, redacted out of anything that is printed, never kept.
		token, err := in.Tokens.Registration(in.Repo)
		if err != nil {
			notes.note("FLEET NOTE runner=%s got no registration token: %s", oneline.Field(name), redact(oneline.Err(err), token))
			continue
		}
		out, err := in.Shell.Run(in.Host, configureScript(dir, runnerVersion, in.Repo, name, labels), token+"\n")
		if err != nil {
			notes.note("FLEET NOTE runner=%s could not be configured: %s", oneline.Field(name), redact(oneline.Err(err), token))
			continue
		}
		_ = out // the configure output is the runner's own chatter; only the line below is ours
		configured++

		svc := RunnerService{Name: name, Host: orDash(in.Host), Kind: kind, Target: serviceTarget(kind, name, dir)}
		services = append(services, svc)
		command, err := restartCommand(svc)
		if err != nil {
			notes.note("FLEET NOTE runner=%s was configured but not started: %s", oneline.Field(name), oneline.Err(err))
			continue
		}
		if _, err := in.Shell.Run(in.Host, startCommand(svc, command), ""); err != nil {
			notes.note("FLEET NOTE runner=%s did not start: %s", oneline.Field(name), oneline.Err(err))
			continue
		}
		started++
	}

	notes.close()

	machine := Machine{
		Name: in.Name, Host: orDash(in.Host), User: in.User, Cores: in.Cores, RAMGB: in.RAMGB,
		OS: in.OS, RunnerDir: pattern, ServiceKind: kind, Labels: in.Labels,
		CardSlots: in.CardSlots, Network: in.Network,
	}
	if err := upsertMachine(in.Queue, machine); err != nil {
		fmt.Fprintf(in.Stderr, "FLEET NOTE the %s row could not be written: %s\n", FleetFile, oneline.Err(err))
	}
	if err := upsertRunnerServices(in.Queue, services); err != nil {
		fmt.Fprintf(in.Stderr, "FLEET NOTE the %s rows could not be written: %s\n", RunnerServicesFile, oneline.Err(err))
	}

	fmt.Fprintf(in.Stdout, "FLEET add name=%s host=%s runners=%d configured=%d started=%d go=%s kind=%s labels=%s dry-run=false\n",
		oneline.Field(in.Name), oneline.Field(orDash(in.Host)), in.Runners, configured, started,
		oneline.Field(goVersion), oneline.Field(kind), oneline.Field(labels))
	if configured < in.Runners || started < configured {
		return 1
	}
	return 0
}

// fleetAddRefusal is every way the invocation is unusable, as one line. A machine half
// stood up is worse than one not started, so nothing runs until all of this holds.
func fleetAddRefusal(in FleetAddInput) string {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return "the machine's name is required (it is the stem of every runner's name); refusing to guess"
	case strings.TrimSpace(in.Host) == "":
		return "--host is required (the ssh destination, or - for this machine); refusing to guess"
	case strings.TrimSpace(in.Repo) == "":
		return "--repo is required, owner/name (the repository the runners register against)"
	case strings.TrimSpace(in.Queue) == "":
		return "--queue is required (the directory holding " + FleetFile + " and " + RunnerServicesFile + ")"
	case in.Runners < 1:
		return fmt.Sprintf("--runners is at least 1, got %d", in.Runners)
	case in.Shell == nil:
		return "no shell is wired (this is a bug in the caller, not in the invocation)"
	case in.Tokens == nil && !in.DryRun:
		return "no registration token source is wired (this is a bug in the caller)"
	case normalizeServiceKind(in.ServiceKind) == "" && strings.TrimSpace(in.ServiceKind) != "":
		return fmt.Sprintf("--service-kind %q is not one of systemd, svc.sh, runsh", in.ServiceKind)
	}
	return ""
}

// detectServiceKind asks the machine how it starts things: systemd when a passwordless
// `sudo -n systemctl` answers, the runner's own svc.sh when the machine is a Mac, and
// otherwise the supervised run.sh loop, which needs nothing but a shell.
func detectServiceKind(in FleetAddInput) string {
	if out, err := in.Shell.Run(in.Host, serviceKindScript, ""); err == nil {
		switch strings.TrimSpace(headLine(out)) {
		case "systemd":
			return "systemd"
		case "svc":
			return "svc"
		}
	}
	return "runsh"
}

// serviceKindScript prints one word: how this machine starts a runner.
const serviceKindScript = `if sudo -n systemctl --version >/dev/null 2>&1; then echo systemd
elif [ "$(uname -s)" = "Darwin" ]; then echo svc
else echo runsh
fi`

// goInstallScript puts go<ver> under ~/sdk and points ~/go/bin/go at it, which is where the
// workflow looks. A machine that already has that compiler does nothing: `add` is run again
// on a machine that half succeeded, and the second run must be cheap.
func goInstallScript(version string) string {
	return `set -eu
ver=` + shellQuote(version) + `
sdk="$HOME/sdk/go$ver"
if [ ! -x "$sdk/bin/go" ]; then
  case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) pkg="go$ver.darwin-arm64.tar.gz" ;;
    Darwin-x86_64) pkg="go$ver.darwin-amd64.tar.gz" ;;
    Linux-aarch64) pkg="go$ver.linux-arm64.tar.gz" ;;
    *) pkg="go$ver.linux-amd64.tar.gz" ;;
  esac
  mkdir -p "$HOME/sdk" "$HOME/dl"
  [ -f "$HOME/dl/$pkg" ] || curl -fsSL -o "$HOME/dl/$pkg" "https://go.dev/dl/$pkg"
  rm -rf "$HOME/sdk/go.tmp" && mkdir -p "$HOME/sdk/go.tmp"
  tar xzf "$HOME/dl/$pkg" -C "$HOME/sdk/go.tmp"
  rm -rf "$sdk" && mv "$HOME/sdk/go.tmp/go" "$sdk"
fi
mkdir -p "$HOME/go/bin"
ln -sfn "$sdk/bin/go" "$HOME/go/bin/go"
ln -sfn "$sdk/bin/gofmt" "$HOME/go/bin/gofmt"
"$HOME/go/bin/go" version`
}

// runnerTarballScript fetches the actions-runner release once into ~/dl. The N unpacks read
// it from there: on 2026-09-16 the same tarball was downloaded eight times by hand.
func runnerTarballScript(version string) string {
	return `set -eu
ver=` + shellQuote(version) + `
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) pkg="actions-runner-osx-arm64-$ver.tar.gz" ;;
  Darwin-x86_64) pkg="actions-runner-osx-x64-$ver.tar.gz" ;;
  Linux-aarch64) pkg="actions-runner-linux-arm64-$ver.tar.gz" ;;
  *) pkg="actions-runner-linux-x64-$ver.tar.gz" ;;
esac
mkdir -p "$HOME/dl"
[ -f "$HOME/dl/$pkg" ] || curl -fsSL -o "$HOME/dl/$pkg" "https://github.com/actions/runner/releases/download/v$ver/$pkg"
echo "$HOME/dl/$pkg"`
}

// configureScript unpacks one runner and registers it. The registration token arrives on
// stdin and is read into a shell variable: it is never an argument of ours, never in the
// ssh command line, and never printed -- the one place it exists on the far side is that
// variable, for the length of one config.sh call.
func configureScript(dir, runnerVersion, repo, name, labels string) string {
	return `set -eu
IFS= read -r NOVA_RUNNER_TOKEN
ver=` + shellQuote(runnerVersion) + `
dir=` + shellQuote(dir) + `
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) pkg="actions-runner-osx-arm64-$ver.tar.gz" ;;
  Darwin-x86_64) pkg="actions-runner-osx-x64-$ver.tar.gz" ;;
  Linux-aarch64) pkg="actions-runner-linux-arm64-$ver.tar.gz" ;;
  *) pkg="actions-runner-linux-x64-$ver.tar.gz" ;;
esac
mkdir -p "$dir"
cd "$dir"
[ -x ./config.sh ] || tar xzf "$HOME/dl/$pkg" -C "$dir"
./config.sh --unattended --replace --url ` + shellQuote("https://github.com/"+repo) +
		` --name ` + shellQuote(name) + ` --labels ` + shellQuote(labels) +
		` --work _work --token "$NOVA_RUNNER_TOKEN" >/dev/null
unset NOVA_RUNNER_TOKEN
echo configured`
}

// serviceTarget is what RunnerServicesFile records for a runner of each kind: the unit for
// systemd, the runner's directory for the other two (svc.sh lives in it, and so does
// run.sh).
func serviceTarget(kind, name, dir string) string {
	if kind == "systemd" {
		return "actions.runner." + strings.ReplaceAll(strings.TrimSpace(name), "/", "-")
	}
	return dir
}

// startCommand is the first start of a runner that has just been configured. For systemd
// the unit is installed first (svc.sh install writes it); for the other two the restart
// command already starts a runner that is not running.
func startCommand(svc RunnerService, restart string) string {
	if svc.Kind == "systemd" {
		return "cd " + shellQuote(svc.Target) + " 2>/dev/null; sudo -n ./svc.sh install 2>/dev/null; " + restart
	}
	return restart
}

// runnerDir expands the machine's pattern for runner i.
func runnerDir(pattern string, i int) string {
	if strings.Contains(pattern, "%d") {
		return fmt.Sprintf(pattern, i)
	}
	return fmt.Sprintf("%s-%d", pattern, i)
}

// FleetRestartInput is `fleet restart <runner>`.
type FleetRestartInput struct {
	Runner string
	Queue  string
	DryRun bool
	Shell  Shell
	Stdout io.Writer
	Stderr io.Writer
}

// FleetRestart restarts one runner through its own service: the systemd unit on Space, the
// runner's svc.sh on the Studio, the supervised run.sh loop where there is neither. The
// service map is rule E2's map (runners.go) and not a second one, so a runner the reaper
// can restart is one this verb can restart and the other way round.
func FleetRestart(in FleetRestartInput) int {
	if strings.TrimSpace(in.Runner) == "" {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: the runner's name is required; refusing to guess\n")
		return 2
	}
	services, err := ReadRunnerServices(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	svc, ok := services[in.Runner]
	if !ok {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: runner %s has no row in %s (add name<TAB>host<TAB>kind<TAB>target; refusing to guess a service)\n",
			oneline.Field(in.Runner), oneline.Field(filepath.Join(in.Queue, RunnerServicesFile)))
		return 2
	}
	command, err := restartCommand(svc)
	if err != nil {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if in.DryRun {
		fmt.Fprintf(in.Stdout, "FLEET restart runner=%s kind=%s host=%s ok=true dry-run=true\n",
			oneline.Field(svc.Name), oneline.Field(svc.Kind), oneline.Field(orDash(svc.Host)))
		return 0
	}
	if in.Shell == nil {
		fmt.Fprintf(in.Stderr, "FLEET REFUSED: no shell is wired (this is a bug in the caller)\n")
		return 2
	}
	out, err := in.Shell.Run(svc.Host, command, "")
	if err != nil {
		fmt.Fprintf(in.Stderr, "FLEET NOTE runner=%s did not restart: %s\n", oneline.Field(svc.Name), oneline.Err(err))
		fmt.Fprintf(in.Stdout, "FLEET restart runner=%s kind=%s host=%s ok=false dry-run=false\n",
			oneline.Field(svc.Name), oneline.Field(svc.Kind), oneline.Field(orDash(svc.Host)))
		return 1
	}
	_ = out
	fmt.Fprintf(in.Stdout, "FLEET restart runner=%s kind=%s host=%s ok=true dry-run=false\n",
		oneline.Field(svc.Name), oneline.Field(svc.Kind), oneline.Field(orDash(svc.Host)))
	return 0
}

// restartCommand is the one place the three mechanisms live. ServiceRestarter (rule E2) and
// `fleet restart` both come here, so the reaper and the verb can never drift into restarting
// a runner two different ways.
func restartCommand(svc RunnerService) (string, error) {
	switch normalizeServiceKind(svc.Kind) {
	case "systemd":
		return "sudo -n systemctl restart " + shellQuote(svc.Target), nil
	case "svc":
		return shellQuote(svc.Target) + "/svc.sh stop; " + shellQuote(svc.Target) + "/svc.sh start", nil
	case "runsh":
		// No service manager here: a supervised loop, stopped by the pid it wrote and by a
		// pattern for the run.sh it supervises, then started again the same way `add`
		// started it. `|| true` on the stops: a loop that is already down is not an error.
		dir := shellQuote(svc.Target)
		return "{ [ -f " + dir + "/nova-runsh.pid ] && kill \"$(cat " + dir + "/nova-runsh.pid)\" 2>/dev/null; } || true; " +
			"pkill -f " + dir + "/run.sh || true; sleep 1; " + runshStart(svc.Target), nil
	}
	return "", fmt.Errorf("runner %s has kind %q in %s (the kinds are systemd, svc.sh and runsh)",
		svc.Name, svc.Kind, RunnerServicesFile)
}

// runshStart is the supervised loop: run.sh in a `while true` under setsid, its pid written
// where the stop half can find it, its output in the runner's own directory.
func runshStart(dir string) string {
	q := shellQuote(dir)
	return "cd " + q + " && setsid nohup bash -c 'while true; do ./run.sh; sleep 5; done' " +
		">> " + q + "/nova-runsh.log 2>&1 < /dev/null & echo $! > " + q + "/nova-runsh.pid"
}

// normalizeServiceKind takes the three spellings a person writes (fleet.tsv says svc.sh,
// the service map says svc) to the one this package switches on.
func normalizeServiceKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "systemd", "systemctl":
		return "systemd"
	case "svc", "svc.sh", "launchd":
		return "svc"
	case "runsh", "run.sh", "loop":
		return "runsh"
	}
	return ""
}

// FleetProbeInput is `fleet probe <name>`.
type FleetProbeInput struct {
	Name  string
	Queue string
	Repo  string        // the repository the card-shaped job clones
	Job   string        // the job itself, default `go test ./internal/ci`
	Keys  []string      // key files whose presence is reported, names only
	Cap   time.Duration // default DefaultProbeCap (six minutes)

	Shell  Shell
	Now    func() time.Time
	Stdout io.Writer
	Stderr io.Writer
}

// FleetProbe reports the three facts that decide whether a machine can take a card: the
// toolchain it would compile with, which key files are in place (names only -- never a byte
// of a key), and whether a card-shaped job passes on it inside the cap a real card gets.
// One PROBE line. `nova-update watch --adopt` runs this before it switches anything over.
func FleetProbe(in FleetProbeInput) int {
	if strings.TrimSpace(in.Name) == "" {
		fmt.Fprintf(in.Stderr, "PROBE REFUSED: the machine's name is required; refusing to guess\n")
		return 2
	}
	if in.Shell == nil {
		fmt.Fprintf(in.Stderr, "PROBE REFUSED: no shell is wired (this is a bug in the caller)\n")
		return 2
	}
	machines, err := ReadFleet(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PROBE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	machine, ok := machines[in.Name]
	if !ok {
		fmt.Fprintf(in.Stderr, "PROBE REFUSED: %s has no row in %s (run: nova-pulse fleet add %s --host <ssh> ...)\n",
			oneline.Field(in.Name), oneline.Field(filepath.Join(in.Queue, FleetFile)), oneline.Field(in.Name))
		return 2
	}
	limit := in.Cap
	if limit <= 0 {
		limit = DefaultProbeCap
	}
	job := orDefault(in.Job, "go test ./internal/ci")
	repo := orDefault(in.Repo, "mas-bandwidth/nova-tools")
	keys := in.Keys
	if len(keys) == 0 {
		keys = defaultProbeKeys
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	// 1. The toolchain, as the workflow would find it.
	goVersion := "-"
	if out, err := in.Shell.Run(machine.Host, `"$HOME/go/bin/go" version 2>/dev/null || go version`, ""); err == nil {
		goVersion = goVersionOf(out)
	}

	// 2. The key files, by name and never by content.
	present, missing := probeKeys(in.Shell, machine.Host, keys)

	// 3. A card-shaped job under the cap a card gets.
	start := now()
	verdict := "fail"
	if _, err := in.Shell.Run(machine.Host, probeJobScript(repo, job, limit), ""); err == nil {
		verdict = "pass"
	}
	secs := int(now().Sub(start).Round(time.Second) / time.Second)

	fmt.Fprintf(in.Stdout, "PROBE name=%s host=%s go=%s keys=%d/%d missing=%s job=%s secs=%d cap=%ds\n",
		oneline.Field(machine.Name), oneline.Field(orDash(machine.Host)), oneline.Field(goVersion),
		len(present), len(keys), oneline.Field(orDash(strings.Join(missing, ","))),
		verdict, secs, int(limit/time.Second))
	if verdict != "pass" || len(missing) > 0 || goVersion == "-" {
		return 1
	}
	return 0
}

// probeKeys asks once for every key file and gets back one line per file: its name and
// whether it is there. The contents never cross the wire.
func probeKeys(sh Shell, host string, keys []string) (present, missing []string) {
	var b strings.Builder
	for _, k := range keys {
		// The ~ is expanded here rather than on the far side: the far side is `sh`, and the
		// one thing every sh agrees on is that a quoted word is a word.
		path := k
		if rest, ok := strings.CutPrefix(k, "~/"); ok {
			path = `"$HOME"/` + shellQuote(rest)
		} else {
			path = shellQuote(path)
		}
		fmt.Fprintf(&b, "if [ -e %s ]; then echo present %s; else echo missing %s; fi\n",
			path, shellQuote(k), shellQuote(k))
	}
	out, err := sh.Run(host, b.String(), "")
	if err != nil {
		return nil, keyNames(keys)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		state, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		seen[path] = true
		if state == "present" {
			present = append(present, filepath.Base(path))
		} else if state == "missing" {
			missing = append(missing, filepath.Base(path))
		}
	}
	for _, k := range keys {
		if !seen[k] {
			missing = append(missing, filepath.Base(k))
		}
	}
	return present, missing
}

func keyNames(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, filepath.Base(k))
	}
	return out
}

// probeJobScript is the card-shaped job: a shallow clone in a directory of its own, the
// job under the cap, the directory gone either way. A probe that leaves a clone behind is a
// probe that fills a bench.
func probeJobScript(repo, job string, limit time.Duration) string {
	return `set -eu
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
git clone -q --depth 1 ` + shellQuote("https://github.com/"+repo) + ` "$d/repo"
cd "$d/repo"
PATH="$HOME/go/bin:$PATH" timeout ` + strconv.Itoa(int(limit/time.Second)) + ` ` + job
}

// goVersionOf takes `go version go1.26.5 darwin/arm64` to `go1.26.5`.
func goVersionOf(out string) string {
	fields := strings.Fields(headLine(out))
	for _, f := range fields {
		if strings.HasPrefix(f, "go1.") || strings.HasPrefix(f, "go2.") {
			return f
		}
	}
	if len(fields) > 0 {
		return oneline.Cap(fields[len(fields)-1], 40)
	}
	return "-"
}

// ReadFleet reads FleetFile. A file nobody wrote is an empty fleet, and every verb against
// it is then a refusal naming the file rather than a guess about a machine.
func ReadFleet(queue string) (map[string]Machine, error) {
	out := map[string]Machine{}
	path := filepath.Join(queue, FleetFile)
	for n, l := range readLines(path) {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, "\t")
		if len(f) != fleetFields {
			return nil, fmt.Errorf("%s line %d wants %d tab-separated fields (name, host, user, cores, ram_gb, os, runner_dir_pattern, service_kind, labels, card_slots, network), got %d",
				path, n+1, fleetFields, len(f))
		}
		m := Machine{
			Name: strings.TrimSpace(f[0]), Host: strings.TrimSpace(f[1]), User: undash(f[2]),
			Cores: atoiOrZero(f[3]), RAMGB: atoiOrZero(f[4]), OS: undash(f[5]),
			RunnerDir: undash(f[6]), ServiceKind: strings.TrimSpace(f[7]),
			CardSlots: atoiOrZero(f[9]), Network: undash(f[10]),
		}
		if labels := undash(f[8]); labels != "" {
			m.Labels = strings.Split(labels, ",")
		}
		out[m.Name] = m
	}
	return out, nil
}

// fleetFields is the row's width, named once so the reader and the writer cannot disagree.
const fleetFields = 11

// upsertMachine writes the machine's row, replacing the row of the same name. The whole
// file is written in a stable order by rename: `add` run twice on one machine leaves one
// row, not two.
func upsertMachine(queue string, m Machine) error {
	machines, err := ReadFleet(queue)
	if err != nil {
		return err
	}
	machines[m.Name] = m
	names := make([]string, 0, len(machines))
	for n := range machines {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("# name\thost\tuser\tcores\tram_gb\tos\trunner_dir_pattern\tservice_kind\tlabels\tcard_slots\tnetwork\n")
	for _, n := range names {
		b.WriteString(machineRow(machines[n]) + "\n")
	}
	return writeByRename(filepath.Join(queue, FleetFile), b.String())
}

func machineRow(m Machine) string {
	return strings.Join([]string{
		m.Name, orDash(m.Host), orDash(m.User), strconv.Itoa(m.Cores), strconv.Itoa(m.RAMGB),
		orDash(m.OS), orDash(m.RunnerDir), orDash(m.ServiceKind), orDash(fleetLabels(m.Labels)),
		strconv.Itoa(m.CardSlots), orDash(m.Network),
	}, "\t")
}

// upsertRunnerServices adds rows to rule E2's service map, replacing rows of the same name.
// This is why `add` and the reaper agree: there is one map and `add` writes into it.
func upsertRunnerServices(queue string, added []RunnerService) error {
	if len(added) == 0 {
		return nil
	}
	services, err := ReadRunnerServices(queue)
	if err != nil {
		return err
	}
	for _, s := range added {
		services[s.Name] = s
	}
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		s := services[n]
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", s.Name, orDash(s.Host), s.Kind, s.Target)
	}
	return writeByRename(filepath.Join(queue, RunnerServicesFile), b.String())
}

func writeByRename(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// redact takes a secret out of anything about to be printed. The token is never meant to
// reach a log; this is the second lock on that door, for the day a shell echoes its stdin.
func redact(s, secret string) string {
	if len(strings.TrimSpace(secret)) < 8 {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// shellQuote makes one single-quoted shell word out of a string, so a path with a space in
// it is one word and a path with a quote in it cannot end the word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// headLine is the first line of some output, trimmed: what a script that prints one word
// said, with whatever a shell added around it dropped. (status.go's firstLine reads a file;
// this reads a string, and the two never want the same name.)
func headLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func undash(s string) string {
	t := strings.TrimSpace(s)
	if t == "-" {
		return ""
	}
	return t
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func fleetLabels(labels []string) string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

// boundedNotes is rule 18 applied to the notes a verb writes beside its one line: the first
// few, then a count. A machine with sixteen broken runners must not print sixteen lines.
type boundedNotes struct {
	w       io.Writer
	kind    string // the verb's word, so the count line reads like the notes it counts
	max     int
	written int
	dropped int
}

func (b *boundedNotes) note(format string, args ...any) {
	if b.written >= b.max {
		b.dropped++
		return
	}
	b.written++
	fmt.Fprintf(b.w, format+"\n", args...)
}

func (b *boundedNotes) close() {
	if b.dropped > 0 {
		fmt.Fprintf(b.w, "%s NOTE ...+%d more notes\n", orDefault(b.kind, "FLEET"), b.dropped)
	}
}
