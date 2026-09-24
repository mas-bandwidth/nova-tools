package cost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The write, the read-back and the one retry (nova-tools #3159, Write outcomes). EXEC is
// not a rollback: a queued command that fails at run time fails alone, and a connection
// lost after EXEC leaves the outcome unknown. So the verb says `nothing written` only when
// it can prove it, and otherwise reads back.

// dayKey is the day's hash.
func dayKey(provider, day string) string { return "cost:" + provider + ":" + day }

// member is the day's cost:idx member.
func member(provider, day string) string { return provider + ":" + day }

// plan is one day of the import: what Redis held before and what the file wants.
type plan struct {
	day        Day
	key, mem   string
	want       map[string]string
	before     map[string]string
	beforeZ    float64
	beforeHasZ bool
	state      string // new, same, replaced or repaired
	recovered  bool
}

func (p *plan) changed() bool { return p.state != "same" }

// wantHash is the full hash the file wants for one day, `at` included.
func wantHash(e *Export, d Day, at time.Time) map[string]string {
	h := map[string]string{
		"total":         formatMicro(d.Micro),
		"rows":          strconv.Itoa(d.Rows),
		"unrouted_rows": strconv.Itoa(d.Unrouted),
		"source_sha256": e.SHA256,
		"source_name":   e.SourceName,
		"writer":        Writer,
		"at":            strconv.FormatInt(at.UTC().UnixMilli(), 10),
	}
	for f, v := range d.Fields {
		h[f] = formatMicro(v)
	}
	return h
}

// equalHash compares two hashes field by field; withAt false ignores the `at` field.
func equalHash(a, b map[string]string, withAt bool) bool {
	n := func(h map[string]string) int {
		if _, ok := h["at"]; ok && !withAt {
			return len(h) - 1
		}
		return len(h)
	}
	if n(a) != n(b) {
		return false
	}
	for k, v := range a {
		if k == "at" && !withAt {
			continue
		}
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// isRedisError reports whether err is a Redis error reply: the command's reply was
// received, and it is an error. redis.Nil is a reply too, but callers test it first.
func isRedisError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// cmdLine names a command and its key, for stderr: `ZADD cost:idx: WRONGTYPE ...`.
func cmdLine(c redis.Cmder, err error) string {
	args := c.Args()
	name := strings.ToUpper(c.Name())
	if len(args) > 1 {
		return fmt.Sprintf("%s %v: %v", name, args[1], err)
	}
	return fmt.Sprintf("%s: %v", name, err)
}

// Import writes an export to Redis and prints one line per day and a closing line. It
// returns ExitOK, ExitPreWrite, ExitUnknown or ExitPartial. A clean import is two round
// trips; a failed write is at most five.
func Import(ctx context.Context, client *redis.Client, e *Export, now time.Time, out, errOut io.Writer) int {
	plans := make([]*plan, len(e.Days))
	for i, d := range e.Days {
		plans[i] = &plan{day: d, key: dayKey(e.Provider, d.Day), mem: member(e.Provider, d.Day), want: wantHash(e, d, now)}
	}
	prewrite := func(format string, args ...any) int {
		fmt.Fprintf(errOut, "nova-sprint cost import: %s; nothing written\n", fmt.Sprintf(format, args...))
		return ExitPreWrite
	}

	// Round trip 1: the index's type and each day's hash and score.
	pipe := client.Pipeline()
	typ := pipe.Type(ctx, IndexKey)
	hashes := make([]*redis.MapStringStringCmd, len(plans))
	scores := make([]*redis.FloatCmd, len(plans))
	for i, p := range plans {
		hashes[i] = pipe.HGetAll(ctx, p.key)
		scores[i] = pipe.ZScore(ctx, IndexKey, p.mem)
	}
	_, _ = pipe.Exec(ctx)
	if err := typ.Err(); err != nil {
		return prewrite("TYPE %s: %v", IndexKey, err)
	}
	if t := typ.Val(); t != "zset" && t != "none" {
		return prewrite("%s is a %s, not a zset", IndexKey, t)
	}
	for i, p := range plans {
		if err := hashes[i].Err(); err != nil {
			return prewrite("%s", cmdLine(hashes[i], err))
		}
		p.before = hashes[i].Val()
		switch err := scores[i].Err(); {
		case err == nil:
			p.beforeZ, p.beforeHasZ = scores[i].Val(), true
		case errors.Is(err, redis.Nil):
		default:
			return prewrite("%s", cmdLine(scores[i], err))
		}
		switch {
		case equalHash(p.before, p.want, false) && p.beforeHasZ && p.beforeZ == p.day.Score():
			p.state = "same"
		case p.before["source_sha256"] == e.SHA256:
			p.state = "repaired"
		case len(p.before) == 0:
			p.state = "new"
		default:
			p.state = "replaced"
		}
	}
	var pending []*plan
	for _, p := range plans {
		if p.changed() {
			pending = append(pending, p)
		}
	}

	if len(pending) > 0 {
		// Round trip 2: one MULTI/EXEC, a whole hash per changed day.
		cmds, err := exec(ctx, client, pending)
		switch {
		case err == nil:
		case isRedisError(err) && strings.HasPrefix(err.Error(), "EXECABORT"):
			return prewrite("EXEC: %v", err)
		default:
			// A command error (some applied, some not) or an unknown outcome: read back.
			code := recoverWrite(ctx, client, e, plans, pending, cmds, err, out, errOut)
			if code != ExitOK {
				return code
			}
		}
	}
	return done(e, plans, out)
}

// exec sends the MULTI for the given days: DEL, one HSET of every field, ZADD.
func exec(ctx context.Context, client *redis.Client, days []*plan) ([]redis.Cmder, error) {
	return client.TxPipelined(ctx, func(tx redis.Pipeliner) error {
		for _, p := range days {
			tx.Del(ctx, p.key)
			fields := make([]string, 0, len(p.want))
			for f := range p.want {
				fields = append(fields, f)
			}
			sort.Strings(fields)
			args := make([]any, 0, 2*len(fields))
			for _, f := range fields {
				args = append(args, f, p.want[f])
			}
			tx.HSet(ctx, p.key, args...)
			tx.ZAdd(ctx, IndexKey, redis.Z{Score: p.day.Score(), Member: p.mem})
		}
		return nil
	})
}

// execErrors names each command the EXEC answered with an error reply.
func execErrors(cmds []redis.Cmder, errOut io.Writer) {
	for _, c := range cmds {
		if c.Name() == "exec" || c.Name() == "multi" {
			continue
		}
		if err := c.Err(); err != nil && isRedisError(err) {
			fmt.Fprintf(errOut, "nova-sprint cost import: %s\n", cmdLine(c, err))
		}
	}
}

// readBack is one pipeline of HGETALL and ZSCORE per day. A day's result is written,
// unchanged or partial when both replies were received (a Redis error reply is received),
// and unknown when either got no reply (a transport failure).
func readBack(ctx context.Context, client *redis.Client, days []*plan, errOut io.Writer) map[*plan]string {
	pipe := client.Pipeline()
	hashes := make([]*redis.MapStringStringCmd, len(days))
	scores := make([]*redis.FloatCmd, len(days))
	for i, p := range days {
		hashes[i] = pipe.HGetAll(ctx, p.key)
		scores[i] = pipe.ZScore(ctx, IndexKey, p.mem)
	}
	_, _ = pipe.Exec(ctx)
	out := map[*plan]string{}
	for i, p := range days {
		hErr, zErr := hashes[i].Err(), scores[i].Err()
		noReply := func(err error) bool {
			return err != nil && !errors.Is(err, redis.Nil) && !isRedisError(err)
		}
		if noReply(hErr) || noReply(zErr) {
			out[p] = "unknown"
			continue
		}
		for _, c := range []redis.Cmder{hashes[i], scores[i]} {
			if err := c.Err(); err != nil && isRedisError(err) {
				fmt.Fprintf(errOut, "nova-sprint cost import: %s\n", cmdLine(c, err))
			}
		}
		hash, hasZ := hashes[i].Val(), zErr == nil
		z := scores[i].Val()
		switch {
		case hErr == nil && zErr == nil && equalHash(hash, p.want, true) && z == p.day.Score():
			out[p] = "written"
		case hErr == nil && (zErr == nil || errors.Is(zErr, redis.Nil)) &&
			equalHash(hash, p.before, true) && hasZ == p.beforeHasZ && (!hasZ || z == p.beforeZ):
			out[p] = "unchanged"
		default:
			out[p] = "partial"
		}
	}
	return out
}

// recoverWrite runs after a MULTI whose outcome is not proven: read back, retry the days not
// written once, read back again.
func recoverWrite(ctx context.Context, client *redis.Client, e *Export, plans, pending []*plan, cmds []redis.Cmder, execErr error, out, errOut io.Writer) int {
	if isRedisError(execErr) {
		execErrors(cmds, errOut)
	} else {
		fmt.Fprintf(errOut, "nova-sprint cost import: EXEC reply lost: %v; reading back\n", execErr)
	}
	result := readBack(ctx, client, pending, errOut)
	if code, stop := verdict(e, plans, result, out, errOut, false); stop {
		return code
	}
	var retry []*plan
	for _, p := range pending {
		if result[p] != "written" {
			retry = append(retry, p)
		}
	}
	if cmds, err := exec(ctx, client, retry); err != nil {
		if isRedisError(err) {
			execErrors(cmds, errOut)
			fmt.Fprintf(errOut, "nova-sprint cost import: retry EXEC: %v\n", err)
		} else {
			fmt.Fprintf(errOut, "nova-sprint cost import: retry EXEC reply lost: %v; reading back\n", err)
		}
	}
	for p, s := range readBack(ctx, client, retry, errOut) {
		result[p] = s
	}
	code, _ := verdict(e, plans, result, out, errOut, true)
	return code
}

// verdict decides after a read-back. Any unknown day is exit 8 at once, never retried. With
// every day written, the days are recovered and the import goes on to its DONE line. Else,
// before the retry, the caller retries; after it, exit 9.
func verdict(e *Export, plans []*plan, result map[*plan]string, out, errOut io.Writer, final bool) (int, bool) {
	unknown, notWritten := 0, 0
	for _, s := range result {
		switch s {
		case "unknown":
			unknown++
		case "written":
		default:
			notWritten++
		}
	}
	switch {
	case unknown > 0:
		failLines(e, plans, result, out)
		fmt.Fprintf(errOut, "nova-sprint cost import: outcome unknown for %d day(s); re-run the same import (it is idempotent)\n", unknown)
		return ExitUnknown, true
	case notWritten == 0:
		for p := range result {
			p.recovered = true
		}
		return ExitOK, false
	case final:
		failLines(e, plans, result, out)
		fmt.Fprintf(errOut, "nova-sprint cost import: write failed in part: %d day(s) not written after one retry; re-run the same import\n", notWritten)
		return ExitPartial, true
	}
	return 0, false
}

// dayLine is the per-day line with its state.
func dayLine(e *Export, p *plan, state string) string {
	line := fmt.Sprintf("COST IMPORT provider=%s day=%s rows=%d total=%s fields=%d unrouted_rows=%d source=%s state=%s",
		e.Provider, p.day.Day, p.day.Rows, formatMicro(p.day.Micro), len(p.day.Fields), p.day.Unrouted, e.SHA256[:8], state)
	if p.recovered {
		line += " recovered=1"
	}
	return line
}

// failLines prints each day on exit 8 or 9: a changed day's read-back state (written,
// unchanged, partial or unknown), and `same` for a day that was already complete.
func failLines(e *Export, plans []*plan, result map[*plan]string, out io.Writer) {
	for _, p := range plans {
		state := p.state
		if s, ok := result[p]; ok {
			state = s
		}
		p.recovered = false
		fmt.Fprintln(out, dayLine(e, p, state))
	}
}

// done prints the success lines: exit 0.
func done(e *Export, plans []*plan, out io.Writer) int {
	written, same, repaired, recovered := 0, 0, 0, 0
	for _, p := range plans {
		fmt.Fprintln(out, dayLine(e, p, p.state))
		switch p.state {
		case "same":
			same++
		case "repaired":
			repaired++
			written++
		default:
			written++
		}
		if p.recovered {
			recovered++
		}
	}
	fmt.Fprintf(out, "COST IMPORT DONE provider=%s days=%d written=%d same=%d repaired=%d recovered=%d\n",
		e.Provider, len(plans), written, same, repaired, recovered)
	return ExitOK
}
