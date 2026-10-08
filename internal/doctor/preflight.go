package doctor

import (
	"context"
	"encoding/json"
	"fmt"
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
	schemaFact     = regexp.MustCompile(`(?:^|\s)schema=(\d+)`)
	binaryFact     = regexp.MustCompile(`(?:^|\s)binary=(\d+)`)
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
	sm, bm := schemaFact.FindStringSubmatch(text), binaryFact.FindStringSubmatch(text)
	if sm == nil || bm == nil {
		return 0, 0, false
	}
	have, _ = strconv.Atoi(sm[1])
	want, _ = strconv.Atoi(bm[1])
	return have, want, want > have
}

// emptyConfigFix previews the seat profile from named secret metadata, without
// recording a login or installing the seat (docs/SPEC-DOCTOR.md, "Coordinator preflight").
func (r *jobRun) emptyConfigFix() string {
	actor := r.applyActor()
	dsn := r.env.Getenv("NOVA_PG_DSN")
	pass := r.env.Getenv("NOVA_PG_PASSWORD_ENV")
	if actor == "" || dsn == "" || pass == "" {
		return r.again(" --as <actor>")
	}
	return fmt.Sprintf("%s --dry-run --config-seat %s --config-dsn %s --config-password-env %s",
		r.seatInstall(), oneline.ShellWord(actor), oneline.ShellWord(dsn), oneline.ShellWord(pass))
}

func (r *jobRun) actor() string {
	if r.in.As != "" {
		return r.in.As
	}
	return r.applyActor()
}

func (r *jobRun) seatInstall() string {
	return fmt.Sprintf("nova-sprint seat install --harness grok --target %s --actor %s --redis %s",
		oneline.ShellWord(r.home("session")), oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
}

func (r *jobRun) inboxPush() string {
	return fmt.Sprintf("nova-sprint inbox --wait --push seat --actor %s --redis %s",
		oneline.ShellWord(r.actor()), oneline.ShellWord(r.redisAddr()))
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
		return Result{Status: Fail, Evidence: "nova-sprint seat push --json printed no push status: " + err.Error(), Fix: r.inboxPush()}
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
	return Result{Status: Fail, Evidence: ev, Fix: r.inboxPush()}
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
	K  string `json:"k"`
	St string `json:"st"`
	W  int    `json:"w"`
	Wd int    `json:"wd"`
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
