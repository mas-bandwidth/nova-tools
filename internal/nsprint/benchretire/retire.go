// Package benchretire takes one bench out of the fleet in one verb
// (nova-tools#3645): `nova-sprint bench retire --bench <b> --why <text>`.
//
// The unit half stops the bench's every-bench loops (the rowan-tools loops
// table: Loops) on the bench through internal/benchsh; the Redis
// half is one Function call, ns_bench_retire (bench_retire.lua): it
// refuses while the bench holds a lease, moves every card still pointing at
// the bench through the one card move, deletes the bench's rows, leaves the
// benches set and writes one bench-retire receipt to cap:log. The units stop
// first so a live beat cannot write the row back after the delete. A second
// run of a retired bench is ALREADY and exits 0.
package benchretire

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
)

// Function is the Redis half.
const Function = "ns_bench_retire"

// Rows are the suffixes of every per-bench key, bench:<b><suffix>, the retire
// deletes. TestRowsCoverEveryBenchKey fails when the tree writes a bench key
// shape that is missing here, so a retired bench leaves no row behind.
var Rows = []string{
	"", ":beat", ":state", ":desired", ":conform", ":owner", ":live", ":ssh",
	":hold", ":reset", ":land", ":queue", ":width", ":done",
	":starting", ":living",
	":cards:waiting", ":cards:ready", ":cards:working", ":cards:done",
	":cards:parked", ":cards:ok", ":cards:fail", ":cards:abstain",
	":mirrors",
}

// Loops are the every-bench rows of the rowan-tools loops table: the units
// com.nova.loop.<name> (launchd) or nova-loop-<name>.service (systemd).
var Loops = []string{"bench-row", "nova-sprint-bench-beat", "ci-run", "mirror-refresh"}

// Runner runs a script on a bench: benchsh.Run, or a fake in tests.
type Runner func(ctx context.Context, t benchsh.Target, script string, args ...string) (benchsh.Result, error)

// Request is one retire.
type Request struct {
	Bench   string
	Actor   string
	Why     string
	Offline bool   // skip the unit half: the bench is gone or unreachable
	Run     Runner // nil: benchsh.Run
	SSH     string // the ssh program; empty: ssh
}

// Result is the receipt. Code is the verb's exit: 0 retired or already
// retired, 1 refused with the remedy in Why, 2 leased.
type Result struct {
	Code      int
	Status    string // RETIRED, ALREADY, LEASED, UNREACHABLE, REFUSED
	Why       string
	Units     []string // "<loop>=stopped|absent" from the bench
	Requeued  int
	Repointed int
	Deleted   int
	Took      time.Duration
}

// unitsReply opens the bench-side reply: what ssh prints before the script
// runs (a host-key warning) stays above it.
const unitsReply = "UNITS-REPLY"

// unitsScript runs through internal/benchsh (`bash -s --`, script on stdin);
// $@ are the loop names. Each loop's unit is booted out and disabled in the
// user domain, else the system domain (sudo -n), and reported stopped,
// absent or failed; any failure exits 1.
const unitsScript = `printf '%s\n' ` + unitsReply + `
rc=0
uid=$(id -u)
for n in "$@"; do
  s=absent
  if [ "$(uname -s)" = Darwin ]; then
    l=com.nova.loop.$n
    if launchctl print "gui/$uid/$l" >/dev/null 2>&1; then
      launchctl bootout "gui/$uid/$l" && launchctl disable "gui/$uid/$l" && s=stopped || s=failed
    elif sudo -n launchctl print "system/$l" >/dev/null 2>&1; then
      sudo -n launchctl bootout "system/$l" && sudo -n launchctl disable "system/$l" && s=stopped || s=failed
    fi
  else
    u=nova-loop-$n.service
    if systemctl --user cat "$u" >/dev/null 2>&1; then
      systemctl --user disable --now "$u" >/dev/null 2>&1 && s=stopped || s=failed
    elif systemctl cat "$u" >/dev/null 2>&1; then
      sudo -n systemctl disable --now "$u" >/dev/null 2>&1 && s=stopped || s=failed
    fi
  fi
  [ "$s" = failed ] && rc=1
  printf 'UNIT %s %s\n' "$n" "$s"
done
exit "$rc"
`

// Retire runs the preflight read, the unit half and the Redis half.
func Retire(ctx context.Context, c *redis.Client, req Request) (Result, error) {
	began := time.Now()
	if req.Bench == "" || req.Actor == "" || strings.TrimSpace(req.Why) == "" {
		return Result{}, errors.New("bench, actor and why are required")
	}
	pre := "bench:" + req.Bench
	pipe := c.Pipeline()
	member := pipe.SIsMember(ctx, "benches", req.Bench)
	beat := pipe.HGetAll(ctx, pre+":beat")
	working := pipe.ZCard(ctx, pre+":cards:working")
	starting := pipe.ZCard(ctx, pre+":starting")
	living := pipe.ZCard(ctx, pre+":living")
	harvest := pipe.Exists(ctx, "lease:harvest:"+req.Bench)
	rowKeys := make([]string, 0, len(Rows)+1)
	for _, s := range Rows {
		rowKeys = append(rowKeys, pre+s)
	}
	rowKeys = append(rowKeys, "ci:nomirror:"+req.Bench)
	rows := pipe.Exists(ctx, rowKeys...)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Result{}, err
	}
	var held []string
	for _, h := range []struct {
		key string
		n   int64
	}{{pre + ":cards:working", working.Val()}, {pre + ":starting", starting.Val()}, {pre + ":living", living.Val()}, {"lease:harvest:" + req.Bench, harvest.Val()}} {
		if h.n > 0 {
			held = append(held, h.key+"="+strconv.FormatInt(h.n, 10))
		}
	}
	if len(held) > 0 {
		return finish(Result{Code: 2, Status: "LEASED", Why: strings.Join(held, " ") + "; run: nova-sprint bench reset --bench " + req.Bench}, began), nil
	}
	if !member.Val() && rows.Val() == 0 {
		return finish(Result{Status: "ALREADY"}, began), nil
	}

	res := Result{}
	if !req.Offline {
		host := beat.Val()["host"]
		if host == "" {
			host = req.Bench
		}
		units, err := stopUnits(ctx, req, benchsh.Target{Host: host, User: beat.Val()["user"], SSH: req.SSH})
		res.Units = units
		if err != nil {
			res.Code, res.Status = 1, "UNREACHABLE"
			res.Why = oneLine(err.Error()) + "; nothing in Redis changed; a bench that is gone: rerun with --offline"
			return finish(res, began), nil
		}
	}

	args := []any{req.Bench, req.Actor, req.Why}
	for _, s := range Rows {
		args = append(args, s)
	}
	raw, err := c.FCall(ctx, Function, nil, args...).StringSlice()
	if err != nil {
		return res, fmt.Errorf("%s: %w", Function, err)
	}
	if len(raw) == 0 {
		return res, fmt.Errorf("%s: empty reply", Function)
	}
	res.Status = raw[0]
	switch raw[0] {
	case "RETIRED":
		if len(raw) != 5 {
			return res, fmt.Errorf("%s: reply %q", Function, raw)
		}
		res.Requeued, _ = strconv.Atoi(raw[1])
		res.Repointed, _ = strconv.Atoi(raw[2])
		res.Deleted, _ = strconv.Atoi(raw[3])
	case "ALREADY":
	case "LEASED":
		res.Code = 2
		res.Why = strings.Join(raw[1:], " ") + "; run: nova-sprint bench reset --bench " + req.Bench
	case "REFUSED":
		res.Code = 1
		if len(raw) == 5 {
			res.Requeued, _ = strconv.Atoi(raw[3])
			res.Repointed, _ = strconv.Atoi(raw[4])
		}
		res.Why = strings.Join(raw[1:min(3, len(raw))], " ") + "; run: nova-sprint card fsck --repair, then retire again"
	default:
		return res, fmt.Errorf("%s: reply %q", Function, raw)
	}
	return finish(res, began), nil
}

func stopUnits(ctx context.Context, req Request, t benchsh.Target) ([]string, error) {
	run := req.Run
	if run == nil {
		run = benchsh.Run
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := run(runCtx, t, unitsScript, Loops...)
	var units []string
	if i := strings.Index(out.Output, unitsReply+"\n"); i >= 0 {
		for _, line := range strings.Split(out.Output[i+len(unitsReply)+1:], "\n") {
			f := strings.Fields(line)
			if len(f) == 3 && f[0] == "UNIT" {
				units = append(units, f[1]+"="+f[2])
			}
		}
	} else if err == nil {
		return nil, fmt.Errorf("ssh %s: no %s line in %q", t.Dest(), unitsReply, oneLine(out.Output))
	}
	if err != nil {
		return units, err
	}
	if len(units) != len(Loops) {
		return units, fmt.Errorf("ssh %s: %d unit lines for %d loops", t.Dest(), len(units), len(Loops))
	}
	return units, nil
}

func finish(res Result, began time.Time) Result { res.Took = time.Since(began); return res }

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
