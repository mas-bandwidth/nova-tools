package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The coordinator preflight is the coordinator job's own chain (docs/SPEC-DOCTOR.md,
// "Coordinator preflight"). It calls the tools that already own each fact and prints one
// next command. It does not write, migrate, install, or switch a binary. A folder write,
// a queued delivery, an old pong, a forced presence or a row's asserted working count is
// not a receipt.

var (
	schemaSentence = regexp.MustCompile(`schema config is at version (\d+) and this binary carries (\d+)`)
	// serverActorDrift is the native seat result's server-actor drift
	// (sprint.SeatDrift): the server runs as a named actor other than the seat's
	// holder, whose fix is a restart, not `seat --repair`.
	serverActorDrift = regexp.MustCompile(`the server runs as (\S+) and the seat is (\S+)'s`)
)

// migrationOwed reports a store schema older than the binary that read it. The next
// command is migrate, even when the tool's own remedy names an install or a switch.
func migrationOwed(out, said string) (have, want int, ok bool) {
	text := out + "\n" + said
	if m := schemaSentence.FindStringSubmatch(text); m != nil {
		have, _ = strconv.Atoi(m[1])
		want, _ = strconv.Atoi(m[2])
		return have, want, want > have
	}
	return 0, 0, false
}

// emptyConfigFix names a recorded login's secret sources (docs/SPEC-DOCTOR.md,
// "Coordinator preflight"); the doctor only prints this writing remedy.
func (r *jobRun) emptyConfigFix() string {
	l := r.in.RedisLogin
	actor, dsn, pass := r.applyActor(), r.env.Getenv("NOVA_PG_DSN"), r.env.Getenv("NOVA_PG_PASSWORD_ENV")
	if actor == "" || dsn == "" || pass == "" || l.Missing() != "" {
		return r.again(" --as <actor> --redis-secrets <dir> --redis-seat <seat> --redis-key <file> --redis-sops <path>")
	}
	return fmt.Sprintf("nova-config login --store %s --as %s --key %s --sops %s --secret %s --dsn %s --actor %s", oneline.ShellWord(l.Store), oneline.ShellWord(l.As), oneline.ShellWord(l.Key), oneline.ShellWord(l.Sops), oneline.ShellWord(pass), oneline.ShellWord(dsn), oneline.ShellWord(actor))
}

// migrateFix probes ownership without changing the schema (docs/SPEC-DOCTOR.md,
// "Coordinator preflight"). The owner role is retained; no password is printed.
func (r *jobRun) migrateFix(ctx context.Context, status string) string {
	args := []string{"migrate", "--dry-run"}
	c := r.exec(ctx, "nova-config", args...)
	for _, line := range strings.Split(c.out, "\n") {
		if !strings.HasPrefix(line, "MIGRATE NOT-OWNED ") {
			continue
		}
		owner := lineField(line, "owner")
		dsn := lineField(status, "pg")
		if dsn == "" {
			dsn = r.env.Getenv("NOVA_PG_DSN")
		}
		if !strings.Contains(dsn, "://") {
			dsn = "postgres://" + dsn
		}
		u, err := url.Parse(dsn)
		if err == nil && u.Host != "" && owner != "" {
			u.User = url.User(owner)
			u.RawQuery = ""
			u.Fragment = ""
			return "nova-config migrate --pg " + oneline.ShellWord(u.String())
		}
		return "nova-config migrate --pg " + oneline.ShellWord("postgres://"+owner+"@<host>:<port>/<db>")
	}
	return "nova-config migrate"
}

func (r *jobRun) actor() string {
	if r.in.As != "" {
		return r.in.As
	}
	return r.applyActor()
}

func (r *jobRun) seatInstall() string {
	harness, dir := r.in.Harness, r.in.Dir
	if harness == "" {
		harness = "<harness>"
	}
	if dir == "" {
		dir = "<session dir>"
	}
	return fmt.Sprintf("nova-sprint seat install --harness %s --target %s --actor %s --redis %s", oneline.ShellWord(harness), oneline.ShellWord(dir), oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
}

// seat agreement and installed machinery precede proof (docs/SPEC-DOCTOR.md,
// "Coordinator preflight"). The native `seat` result names two drifts, each with
// its own one supported command: a key that names someone the record does not is
// `seat --repair`; a running server acting as a named actor other than the holder
// is `install server` (the unit is reinstalled as the holder, whose
// NOVA_SPRINT_ACTOR the loop reads). The server drift's own line is an instruction
// ("change ... and restart it"), which is not a command, so it is never printed as
// the fix.
func stepSeatAgreement(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-sprint", "seat", "--actor", r.actor(), "--redis", r.redisAddr())
	if c.code == -1 {
		return notRun("nova-sprint", c)
	}
	text := c.out + " " + c.said
	switch {
	case strings.Contains(text, "the key says"):
		return Result{Status: Fail, Evidence: firstLine(text), Fix: r.seatRepair()}
	case strings.Contains(text, "the server runs as"):
		return Result{Status: Fail, Evidence: firstLine(text), Fix: r.serverInstall(holderFromServerDrift(text, r.actor()))}
	case strings.Contains(text, "DRIFT"):
		return Result{Status: Fail, Evidence: firstLine(text), Fix: r.seatRepair()}
	}
	return Result{Status: OK, Evidence: firstLine(c.out)}
}

// seatRepair is the key/record drift's command: the record's holder or the owner
// writes the key from the record.
func (r *jobRun) seatRepair() string {
	return fmt.Sprintf("nova-sprint seat --repair --reason 'doctor detects seat drift' --actor %s --redis %s", oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
}

// serverInstall is the server-actor drift's command: the server unit is reinstalled
// as the holder, so the loop restarts as the actor the seat names. --listen is the
// server's fleet address, which no check reports, so it is named as a placeholder;
// --actor names the holder, whose NOVA_SPRINT_ACTOR the unit's loop reads.
func (r *jobRun) serverInstall(holder string) string {
	return fmt.Sprintf("nova-sprint install server --listen <address:port> --redis %s --actor %s", oneline.ShellWord(r.redisAddr()), oneline.ShellWord(holder))
}

// holderFromServerDrift is the holder the server drift names (the seat's), read
// from the native result so the fix names the seat's holder, not the caller's --as.
func holderFromServerDrift(text, def string) string {
	if m := serverActorDrift.FindStringSubmatch(text); m != nil && m[2] != "-" && m[2] != "" {
		return m[2]
	}
	return def
}

func stepSeatService(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-sprint", "seat", "check", "--actor", r.actor(), "--redis", r.redisAddr())
	if c.code == -1 {
		return notRun("nova-sprint", c)
	}
	seen := false
	for _, line := range strings.Split(c.out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "MACHINERY" {
			continue
		}
		switch fields[1] {
		case "server", "store", "loop", "dashboard", "bus", "inbox", "versions":
		default:
			continue // capacity, progress, push proof and the summary have their own steps
		}
		seen = true
		if strings.Contains(line, " DOWN") {
			return Result{Status: Fail, Evidence: line, Fix: remedy(line, r.seatInstall())}
		}
	}
	if !seen {
		return Result{Status: Fail, Evidence: "seat check printed no installed machinery", Fix: r.seatInstall()}
	}
	return Result{Status: OK, Evidence: strings.TrimSpace(c.out)}
}

func (r *jobRun) pendingPushFix(ctx context.Context) string {
	c := r.exec(ctx, "nova-sprint", "seat", "push", "--actor", r.actor(), "--redis", r.redisAddr())
	fix := remedy(c.out+" "+c.said, r.seatInstall())
	fix, _, _ = strings.Cut(fix, "; then,")
	return fix
}

func (r *jobRun) viewCoordinator() string {
	return fmt.Sprintf("nova-sprint view coordinator --all --json --actor %s --redis %s",
		oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
}

func (r *jobRun) releaseCheckCmd() string {
	return fmt.Sprintf("nova-sprint release check --actor %s --redis %s",
		oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
}

func (r *jobRun) friendJob(name string) string {
	return "nova-doctor --job friend --as " + oneline.ShellWord(name) + " --redis " + oneline.ShellWord(r.redisAddr())
}

func namePush(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if lineField(l, "name") == name {
			return lineField(l, "push")
		}
	}
	return ""
}

func seatGeneration(out string) int {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "SEAT ") {
			n, _ := strconv.Atoi(lineField(l, "generation"))
			return n
		}
	}
	return 0
}

type pushStatus struct {
	Recorded bool   `json:"recorded"`
	Live     bool   `json:"live"`
	Proof    string `json:"proof"`
	Why      string `json:"why"`
}

// stepPushRoundtrip is the coordinator's own bus name proven and seat push live.
// A candidate folder or queue write stays pending until both answers are in.
func stepPushRoundtrip(ctx context.Context, r *jobRun) Result {
	actor := r.actor()
	if actor == "" {
		return Result{Status: Fail, Evidence: "the coordinator preflight needs the seat's actor", Fix: r.again(" --as <actor>")}
	}
	seat := r.exec(ctx, "nova-sprint", "seat", "--actor", actor, "--redis", r.redisAddr())
	if seat.code == -1 {
		return notRun("nova-sprint", seat)
	}
	push := r.exec(ctx, "nova-sprint", "seat", "push", "--json", "--actor", actor, "--redis", r.redisAddr())
	if push.code == -1 {
		return notRun("nova-sprint", push)
	}
	bus := r.exec(ctx, "nova-bus", "names", "--redis", r.redisAddr())
	if bus.code == -1 {
		return notRun("nova-bus", bus)
	}
	var status pushStatus
	if err := json.Unmarshal([]byte(push.out), &status); err != nil {
		return Result{Status: Fail, Evidence: "nova-sprint seat push --json printed no push status: " + err.Error(), Fix: r.pendingPushFix(ctx)}
	}
	if seatGeneration(seat.out) == 0 || !status.Recorded {
		return Result{Status: Fail, Evidence: "the seat has no generation or push target", Fix: r.seatInstall()}
	}
	root := namePush(bus.out, actor) == "proven"
	live := status.Live && status.Proof == "proven"
	if root && live {
		return Result{Status: OK, Evidence: fmt.Sprintf("root bus name=%s push=proven; sprint PUSH OK; actor=%s generation=%d", actor, actor, seatGeneration(seat.out))}
	}
	ev := "pending: no root bus and sprint answer"
	if status.Proof == "pending" {
		ev += "; a check was delivered but its nonce is only in the session"
	}
	if status.Why != "" {
		ev += "; " + status.Why
	}
	return Result{Status: Fail, Evidence: ev, Fix: r.pendingPushFix(ctx)}
}

// coordView is the slice of `nova-sprint view coordinator --all --json` the preflight reads.
// W is the row's asserted working count and is not the observed one.
type coordView struct {
	Rows []coordRow `json:"rows"`
	N    struct {
		Review int `json:"review"`
		Merge  int `json:"merge"`
	} `json:"n"`
}

type coordRow struct {
	K   string `json:"k"`
	St  string `json:"st"`
	W   int    `json:"w"`
	Wd  int    `json:"wd"`
	Rep string `json:"rep"`
}

type workerView struct {
	Cards []struct {
		ID string `json:"id"`
		St string `json:"st"`
	} `json:"cards"`
}

func (r *jobRun) coordinatorView(ctx context.Context) (coordView, call, error) {
	c := r.exec(ctx, "nova-sprint", "view", "coordinator", "--all", "--json", "--actor", r.actor(), "--redis", r.redisAddr())
	var v coordView
	if c.code == -1 {
		return v, c, fmt.Errorf("%s", c.said)
	}
	if err := json.Unmarshal([]byte(c.out), &v); err != nil {
		return v, c, fmt.Errorf("nova-sprint view coordinator --json printed no view: %v", err)
	}
	return v, c, nil
}

func (r *jobRun) observedRunning(ctx context.Context, name string) ([]string, call, error) {
	c := r.exec(ctx, "nova-sprint", "view", "worker", "--as", name, "--json", "--actor", r.actor(), "--redis", r.redisAddr())
	var v workerView
	if c.code == -1 {
		return nil, c, fmt.Errorf("%s", c.said)
	}
	if err := json.Unmarshal([]byte(c.out), &v); err != nil {
		return nil, c, fmt.Errorf("nova-sprint view worker --json printed no view: %v", err)
	}
	var ids []string
	for _, card := range v.Cards {
		if card.St == "working" && card.ID != "" {
			ids = append(ids, card.ID)
		}
	}
	return ids, c, nil
}

func reducedCapacity(state string) bool {
	switch state {
	case "held", "down", "offline":
		return true
	default:
		return false
	}
}

func runningWord(ids []string) string {
	if len(ids) == 0 {
		return "-"
	}
	return strings.Join(ids, ",")
}

// stepFriendCapacity is every configured friend. An available friend without a proven
// bus push fails. A held or offline friend is reduced capacity, not a startup failure.
// Working is how many job ids view worker lists, never the row's width or its W.
func stepFriendCapacity(ctx context.Context, r *jobRun) Result {
	if r.actor() == "" {
		return Result{Status: Fail, Evidence: "friend capacity needs the coordinator's actor", Fix: r.again(" --as <actor>")}
	}
	v, c, err := r.coordinatorView(ctx)
	if c.code == -1 {
		return notRun("nova-sprint", c)
	}
	if err != nil {
		return Result{Status: Fail, Evidence: err.Error(), Fix: r.viewCoordinator()}
	}
	bus := r.exec(ctx, "nova-bus", "names", "--redis", r.redisAddr())
	if bus.code == -1 {
		return notRun("nova-bus", bus)
	}
	var parts []string
	var missing string
	warn := false
	for _, row := range v.Rows {
		if strings.HasPrefix(row.K, "m:") {
			ids, wc, err := r.observedRunning(ctx, strings.TrimPrefix(row.K, "m:"))
			if wc.code == -1 {
				return notRun("nova-sprint", wc)
			}
			if err != nil {
				return Result{Status: Fail, Evidence: err.Error(), Fix: r.viewCoordinator()}
			}
			parts = append(parts, fmt.Sprintf("fleet %s state=%s width=%d running=%s working=%d report=%s", row.K, row.St, row.Wd, runningWord(ids), len(ids), row.Rep))
			if reducedCapacity(row.St) || row.Rep == "never" {
				warn = true
			}
			continue
		}
		if !strings.HasPrefix(row.K, "f:") {
			continue
		}
		name := strings.TrimPrefix(row.K, "f:")
		ids, wc, err := r.observedRunning(ctx, name)
		if wc.code == -1 {
			return notRun("nova-sprint", wc)
		}
		if err != nil {
			return Result{Status: Fail, Evidence: err.Error(), Fix: r.viewCoordinator()}
		}
		push := namePush(bus.out, name)
		if push == "" {
			push = "none"
		}
		part := fmt.Sprintf("%s state=%s width=%d running=%s working=%d push=%s", name, row.St, row.Wd, runningWord(ids), len(ids), push)
		if reducedCapacity(row.St) {
			part += " capacity warning: reduced capacity, not a startup failure"
			warn = true
		} else if push != "proven" && missing == "" {
			missing = name
		}
		parts = append(parts, part)
	}
	ev := strings.Join(parts, "; ")
	if ev == "" {
		ev = "no configured friend"
	}
	if missing != "" {
		return Result{Status: Fail, Evidence: "available " + missing + " has no push receipt; " + ev, Fix: r.friendJob(missing)}
	}
	if warn {
		return Result{Status: Warn, Evidence: ev, Fix: r.viewCoordinator()}
	}
	return Result{Status: OK, Evidence: ev}
}

// stepRuntimeProgress is a backed-up review or lander: a warning, not a failed preflight.
func stepRuntimeProgress(ctx context.Context, r *jobRun) Result {
	v, c, err := r.coordinatorView(ctx)
	if c.code == -1 {
		return notRun("nova-sprint", c)
	}
	if err != nil {
		return Result{Status: Fail, Evidence: err.Error(), Fix: r.viewCoordinator()}
	}
	if v.N.Review > 0 || v.N.Merge > 0 {
		return Result{Status: Warn, Evidence: fmt.Sprintf("runtime warning: review=%d lander=%d is backed up and is not a coordination failure", v.N.Review, v.N.Merge), Fix: r.viewCoordinator()}
	}
	return Result{Status: OK, Evidence: "queue, review and lander are not backed up"}
}

// stepReleaseBlockers keeps the release gate off the coordination result. A red gate warns.
func stepReleaseBlockers(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-sprint", "release", "check", "--actor", r.actor(), "--redis", r.redisAddr())
	if c.code == -1 {
		return notRun("nova-sprint", c)
	}
	switch {
	case strings.Contains(c.out, "RELEASE NOT READY"):
		ev := "release blockers stay separate from coordination readiness"
		if s := lineWith(c.out, "RELEASE NOT READY"); s != "" {
			ev += ": " + s
		}
		return Result{Status: Warn, Evidence: ev, Fix: r.releaseCheckCmd()}
	case strings.Contains(c.out, "RELEASE OK"):
		return Result{Status: OK, Evidence: "release blockers stay separate from coordination readiness: " + lineWith(c.out, "RELEASE OK")}
	default:
		return Result{Status: Fail, Evidence: "nova-sprint release check printed no release summary: " + firstLine(c.out+" "+c.said), Fix: r.releaseCheckCmd()}
	}
}
