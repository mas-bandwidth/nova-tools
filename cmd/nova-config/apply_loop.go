// apply_loop.go is nova-config apply --every, and apply install / uninstall.
// The loop reads the store revision and the Redis applied revision and applies
// each kind that differs, one pass at a time (docs/SPEC-CONFIG.md, "Apply").

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// applyEveryDefault is the period apply install uses when --every is omitted.
const applyEveryDefault = 5 * time.Second

// applyGapJudgment is the age past which a gap is a judgment line for the seat.
const applyGapJudgment = 60 * time.Second

// applyLoopRow is the loop row the dashboard shows for this loop.
const applyLoopRow = "nova-config-apply"

// applyLoopLabel is the launchd label; the plist is applyLoopLabel.plist.
const applyLoopLabel = "nova-config.apply"

// applyLoopService is the systemd user unit's file name.
const applyLoopService = "nova-config-apply.service"

// errApplyStop ends a loop in a test without a signal and without a failure.
var errApplyStop = errors.New("apply loop: stop")

// applyErr is one kind's apply that did not stamp. rev is the store revision
// that Redis does not yet hold.
type applyErr struct {
	rev int64
	err error
}

func (e *applyErr) Error() string {
	why := e.err.Error()
	if i := strings.Index(why, "; run:"); i >= 0 {
		why = why[:i]
	}
	return fmt.Sprintf("UNAPPLIED rev=%d: %s; run: %s apply", e.rev, plain(why), toolName)
}

func (e *applyErr) Unwrap() error { return e.err }

// applyHost is the machine apply install writes a unit onto. Tests hand in
// their own directory and loader; the real host loads with launchctl or
// systemctl (units.Load), which refuses under NOVA_TEST_NO_HOST.
type applyHost struct {
	goos         string
	exe          func() (string, error)
	home         func() (string, error)
	load, unload func(path string) error
}

func realApplyHost() applyHost {
	return applyHost{
		goos:   runtime.GOOS,
		exe:    os.Executable,
		home:   os.UserHomeDir,
		load:   func(path string) error { return units.Load(runtime.GOOS, "load", path) },
		unload: func(path string) error { return units.Load(runtime.GOOS, "unload", path) },
	}
}

// applyEvery runs pass, then waits every, until wait or ctx ends. The same
// error text is reported once; a different text, or a pass that succeeded,
// arms the next report (docs/SPEC-CONFIG.md, "Apply").
func applyEvery(ctx context.Context, every time.Duration, pass func(context.Context) error, wait func(context.Context, time.Duration) error, report func(error)) error {
	var last string
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		err := pass(ctx)
		if err != nil {
			if err.Error() != last {
				report(err)
				last = err.Error()
			}
		} else {
			last = ""
		}
		if err := ctx.Err(); err != nil {
			return nil
		}
		werr := wait(ctx, every)
		if werr == nil {
			continue
		}
		if errors.Is(werr, errApplyStop) || errors.Is(werr, context.Canceled) || errors.Is(werr, context.DeadlineExceeded) {
			return nil
		}
		return werr
	}
}

// applyInterval waits d or returns when ctx is done. It is the loop's real wait.
func applyInterval(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// runApplyLoop is `nova-config apply --every`. One pass at a time, until the
// process is interrupted. wait nil uses applyInterval.
func runApplyLoop(ctx context.Context, every time.Duration, stdout, stderr io.Writer, d deps, c conn, actor, dsn, addr string, kinds []string, moveSeat, asJSON bool, note string, wait func(context.Context, time.Duration) error) int {
	if wait == nil {
		wait = applyInterval
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if note != "" && !asJSON {
		printNotes(stdout, []string{note})
	}
	err := applyEvery(ctx, every, func(ctx context.Context) error {
		return oneLoopPass(ctx, stdout, d, c, actor, dsn, addr, kinds, moveSeat, asJSON, note)
	}, wait, func(err error) {
		fmt.Fprintln(stderr, err.Error())
	})
	if err != nil {
		fmt.Fprintf(stderr, "%s apply REFUSED: %s; run: %s apply -h\n", toolName, plain(err.Error()), toolName)
		return 2
	}
	return 0
}

// oneLoopPass opens the store and Redis and applies the kinds that differ.
func oneLoopPass(ctx context.Context, stdout io.Writer, d deps, c conn, actor, dsn, addr string, kinds []string, moveSeat, asJSON bool, note string) error {
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }() // ignored: the pass has already read every reply it needs
	var schema bytesBuffer
	if _, stale := behindSchema(ctx, st, &schema, "apply", c); stale {
		return errors.New(strings.TrimSpace(schema.String()))
	}
	if containsKind(kinds, config.KindFleet) {
		fleet, _, err := st.Get(ctx, config.KindFleet, config.KindFleet)
		if err != nil {
			return err
		}
		if err := config.ValidateFleetEndpoints(config.View(fleet.Fields)); err != nil {
			return err
		}
	}
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }() // ignored: the pass has already read every reply it needs
	return applyPass(ctx, st, rs, d, actor, kinds, moveSeat, asJSON, note, stdout)
}

// bytesBuffer is an io.Writer behindSchema can print a refusal into.
type bytesBuffer struct{ b strings.Builder }

func (w *bytesBuffer) Write(p []byte) (int, error) { return w.b.Write(p) }
func (w *bytesBuffer) String() string              { return w.b.String() }

// applyPass applies each kind whose store revision differs from Redis's
// applied revision, and no other. The stamp is the store revision: the whole
// gap, not each history id between the two (docs/SPEC-CONFIG.md, "Apply").
func applyPass(ctx context.Context, st pgStore, rs redisSide, d deps, actor string, kinds []string, moveSeat, asJSON bool, note string, stdout io.Writer) error {
	o := tool.Done()
	o.Verb = "apply"
	if note != "" {
		o.Note(note)
	}
	applyFn := config.Apply
	if moveSeat {
		applyFn = config.ApplyMovingSeat
	}
	said := false
	var failed error
	for _, kn := range kinds {
		if err := ctx.Err(); err != nil {
			failed = err
			break
		}
		storeRev, err := st.Rev(ctx, kn)
		if err != nil {
			failed = err
			break
		}
		_, applied, err := rs.Read(ctx, kn)
		if err != nil {
			failed = err
			break
		}
		if storeRev == applied {
			continue
		}
		if age, ok, err := kindGapAge(ctx, st, kn, applied, d.now()); err != nil {
			failed = err
			break
		} else if ok {
			said = true
			gap := fmt.Sprintf("CONFIG GAP kind=%s store=%d applied=%d age=%s", kn, storeRev, applied, ageText(age))
			say(stdout, o, asJSON, gap)
			if age > applyGapJudgment {
				say(stdout, o, asJSON, judgmentLine(kn, storeRev, applied, age))
			}
		}
		start := d.now()
		res, err := applyFn(ctx, st, rs, kn, actor, false, func(op config.Op) {
			if asJSON {
				o.Item("op", "kind", kn, "op", op.Op, "name", op.Name, "changed", op.Changed)
				return
			}
			fmt.Fprintln(stdout, config.SaidLine("APPLY", kn, op))
		})
		if err != nil {
			failed = &applyErr{rev: storeRev, err: err}
			break
		}
		said = true
		if asJSON {
			o.Item("kind", "kind", kn, "add", res.Add, "set", res.Set, "remove", res.Remove, "rev", res.Rev, "applied", res.RedisRev)
			continue
		}
		fmt.Fprintf(stdout, "CONFIG APPLY kind=%s add=%d set=%d remove=%d rev=%d ms=%d\n", kn, res.Add, res.Set, res.Remove, res.Rev, d.now().Sub(start).Milliseconds())
	}
	if asJSON && (said || failed != nil) {
		if failed != nil {
			o.Status, o.Exit = tool.Failed, 1
			o.Why = []string{failed.Error()}
		}
		emit(stdout, o)
	}
	return failed
}

// say prints one line, or keeps it as a note on the JSON object.
func say(stdout io.Writer, o *tool.Out, asJSON bool, line string) {
	if asJSON {
		o.Note(line)
		return
	}
	fmt.Fprintln(stdout, line)
}

// kindGapAge is how long the first kind-wide history revision after applied
// has waited (docs/SPEC-CONFIG.md, Apply). Removed names still have history.
func kindGapAge(ctx context.Context, st pgStore, kind string, applied int64, now time.Time) (time.Duration, bool, error) {
	reader, ok := st.(interface {
		FirstAfter(context.Context, string, int64) (config.Change, bool, error)
	})
	if !ok {
		return 0, false, fmt.Errorf("store cannot read kind-wide history for %s", kind)
	}
	first, found, err := reader.FirstAfter(ctx, kind, applied)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}
	at, err := time.Parse(time.RFC3339, first.At)
	if err != nil {
		return 0, false, nil
	}
	age := now.Sub(at)
	if age < 0 {
		age = 0
	}
	return age, true, nil
}

func ageText(age time.Duration) string {
	secs := int(age.Round(time.Second) / time.Second)
	if secs < 0 {
		secs = 0
	}
	return fmt.Sprintf("%ds", secs)
}

func judgmentLine(kind string, store, applied int64, age time.Duration) string {
	return fmt.Sprintf("JUDGMENT kind=%s store=%d applied=%d age=%s: the Redis copy is behind the store; run: %s apply", kind, store, applied, ageText(age), toolName)
}

func containsKind(kinds []string, name string) bool {
	for _, k := range kinds {
		if k == name {
			return true
		}
	}
	return false
}

// runApplyInstall writes the apply loop's unit and its loop row.
func runApplyInstall(ctx context.Context, args []string, stdout, stderr io.Writer, d deps, host applyHost) int {
	const verb = "apply install"
	fs := applyFlagSet(verb)
	c := seatStoreFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` the loop writes (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	every := fs.Duration("every", applyEveryDefault, "the time between the loop's passes, above zero (default 5s)")
	machine := fs.String("machine", "", "the `name` of the machine row this loop runs on")
	dirFlag := fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logFlag := fs.String("log", "", "the file the loop's lines go to on macOS (default: ~/Library/Logs/nova-config-apply.log); on Linux they are in the journal")
	dry := fs.Bool("dry-run", false, "print the unit and the loop row and write nothing; it still reads the store")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "apply install takes no arguments; flags only")
	}
	return finishApplyInstall(ctx, stdout, stderr, d, host, c, verb, *redisFlag, *as, *every, *machine, *dirFlag, *logFlag, *dry, *asJSON)
}

func finishApplyInstall(ctx context.Context, stdout, stderr io.Writer, d deps, host applyHost, c conn, verb, redisFlag, actorFlag string, every time.Duration, machine, dirFlag, logFlag string, dry, asJSON bool) int {
	var problems []string
	if every <= 0 {
		problems = append(problems, "--every wants a duration above zero (default 5s)")
	}
	if machine == "" {
		problems = append(problems, "--machine is required: the name of the machine row this loop runs on")
	}
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	actor, err := actorName(actorFlag, d.getenv, seatVal)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	addr, err := redisAddress(redisFlag, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	} else if strings.HasPrefix(addr, "mem:") {
		problems = append(problems, "the loop applies the fleet's Redis, and "+addr+" is this process's alone")
	}
	storeArgs, err := applyLoopStoreArgs(c, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if host.goos == "" {
		host.goos = runtime.GOOS
	}
	if applyUnitFile(host.goos) == "" {
		problems = append(problems, "apply install writes a launchd agent (macOS) or a systemd user unit (Linux), and "+host.goos+" has neither; run: nova-config apply --every 5s")
	}
	exe := ""
	if host.exe == nil {
		problems = append(problems, "the path of this nova-config cannot be read")
	} else if exe, err = host.exe(); err != nil {
		problems = append(problems, "the path of this nova-config cannot be read: "+err.Error())
	} else if !filepath.IsAbs(exe) {
		problems = append(problems, "the unit runs nova-config by its absolute path, not "+exe)
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	unitDir, logPath, err := applyUnitPaths(host, d.getenv, dirFlag, logFlag)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	env, err := applyUnitEnv(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	argv := applyLoopArgs(every, addr, storeArgs, actor, d.getenv)
	text, err := applyUnitText(host.goos, exe, logPath, append([]string{exe}, argv[1:]...), env)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	path := filepath.Join(unitDir, applyUnitFile(host.goos))
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: the reply the verb needs is already read
	if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
		return code
	}
	if _, found, err := st.Get(ctx, config.KindMachine, machine); err != nil {
		return storeErr(stderr, verb, err, toolName+" machine list")
	} else if !found {
		return refused(stderr, verb, "--machine "+machine+" names no machine row", toolName+" machine add "+machine+" --user <user> --seat <seat> --slots <n> --actor "+actor)
	}
	row, err := applyLoopRowOf(machine, argv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if d.probe != nil {
		if err := config.CheckLoopVerb(ctx, row, d.probe); err != nil {
			return refuse(stderr, verb, err.Error())
		}
	}
	if dry {
		return printApplyInstallDry(stdout, asJSON, path, text, argv)
	}
	if err := upsertApplyLoop(ctx, st, row, actor); err != nil {
		return storeErr(stderr, verb, err, toolName+" loop show "+applyLoopRow)
	}
	rev, err := st.Rev(ctx, config.KindLoop)
	if err != nil {
		return storeErr(stderr, verb, err, toolName+" apply")
	}
	applyOutput := stdout
	if asJSON {
		applyOutput = io.Discard
	}
	applyFailed := applyLoopKind(ctx, applyOutput, d, addr, actor, st)
	if host.load == nil {
		return refuse(stderr, verb, "apply install has no loader for the unit")
	}
	written, err := units.Installer{Load: host.load, Unload: host.unload}.Write(path, text)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", toolName, verb, oneline.Escape(err.Error()))
		return 1
	}
	if asJSON {
		o := tool.Done().Fact("path", path).Fact("written", written.Changed).Fact("loaded", true).Fact("loop", applyLoopRow).Fact("every", every.String()).Fact("rev", rev)
		o.Verb = verb
		if applyFailed != nil {
			o.Status, o.Exit = tool.Failed, 1
			o.Why = []string{(&applyErr{rev: rev, err: applyFailed}).Error()}
			emit(stdout, o)
			return 1
		}
		o.Fact("applied", true)
		emit(stdout, o)
		return 0
	}
	fmt.Fprintf(stdout, "APPLY INSTALL OK unit=%s written=%t loaded=true loop=%s every=%s\n", oneline.Field(path), written.Changed, applyLoopRow, every.String())
	if applyFailed != nil {
		fmt.Fprintln(stdout, (&applyErr{rev: rev, err: applyFailed}).Error())
		return 1
	}
	fmt.Fprintf(stdout, "APPLIED rev=%d\n", rev)
	return 0
}

func printApplyInstallDry(stdout io.Writer, asJSON bool, path, text string, argv []string) int {
	if asJSON {
		o := tool.Done().Fact("dry_run", true).Fact("path", path).Fact("unit", text).Fact("loop", applyLoopRow).Fact("argv", argv)
		o.Verb = "apply install"
		return emit(stdout, o)
	}
	fmt.Fprintf(stdout, "APPLY INSTALL DRY-RUN unit=%s loop=%s; nothing was written or loaded\n%s", oneline.Field(path), applyLoopRow, text)
	return 0
}

// applyLoopKind applies the loop kind once so the dashboard's copy shows the
// row. A failure leaves the applied revision unchanged; the caller says so.
func applyLoopKind(ctx context.Context, stdout io.Writer, d deps, addr, actor string, st pgStore) error {
	rs, err := d.openRedis(ctx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }() // ignored: the reply the verb needs is already read
	start := d.now()
	res, err := config.Apply(ctx, st, rs, config.KindLoop, actor, false, func(op config.Op) {
		fmt.Fprintln(stdout, config.SaidLine("APPLY", config.KindLoop, op))
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "CONFIG APPLY kind=%s add=%d set=%d remove=%d rev=%d ms=%d\n", config.KindLoop, res.Add, res.Set, res.Remove, res.Rev, d.now().Sub(start).Milliseconds())
	return nil
}

// runApplyUninstall removes the apply loop's unit and its loop row.
func runApplyUninstall(ctx context.Context, args []string, stdout, stderr io.Writer, d deps, host applyHost) int {
	const verb = "apply uninstall"
	fs := applyFlagSet(verb)
	c := seatStoreFlags(fs)
	redisFlag := fs.String("redis", "", "the Redis `host:port` the loop wrote (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)")
	as := actorFlag(fs)
	dirFlag := fs.String("dir", "", "the directory the unit was written into (default: as apply install's)")
	dry := fs.Bool("dry-run", false, "name the unit and the loop row and remove nothing")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "apply uninstall takes no arguments; flags only")
	}
	if host.goos == "" {
		host.goos = runtime.GOOS
	}
	file := applyUnitFile(host.goos)
	if file == "" {
		return refuse(stderr, verb, "apply uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and "+host.goos+" has neither")
	}
	var problems []string
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	actor, err := actorName(*as, d.getenv, seatVal)
	if err != nil {
		problems = append(problems, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	addr, err := redisAddress(*redisFlag, d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	unitDir, _, err := applyUnitPaths(host, d.getenv, *dirFlag, "")
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	path := filepath.Join(unitDir, file)
	if *dry {
		_, statErr := os.Stat(path)
		there := statErr == nil
		if *asJSON {
			o := tool.Done().Fact("dry_run", true).Fact("path", path).Fact("present", there).Fact("loop", applyLoopRow)
			o.Verb = verb
			return emit(stdout, o)
		}
		fmt.Fprintf(stdout, "APPLY UNINSTALL DRY-RUN unit=%s present=%t loop=%s; nothing was unloaded or removed\n", oneline.Field(path), there, applyLoopRow)
		return 0
	}
	if host.unload == nil {
		return refuse(stderr, verb, "apply uninstall has no loader for the unit")
	}
	removed, err := units.Installer{Load: host.load, Unload: host.unload}.Remove(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", toolName, verb, oneline.Escape(err.Error()))
		return 1
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: the reply the verb needs is already read
	if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
		return code
	}
	if _, found, err := st.Get(ctx, config.KindLoop, applyLoopRow); err != nil {
		return storeErr(stderr, verb, err, toolName+" loop show "+applyLoopRow)
	} else if found {
		if _, err := st.Delete(ctx, config.KindLoop, applyLoopRow, actor); err != nil {
			return storeErr(stderr, verb, err, toolName+" loop remove "+applyLoopRow)
		}
	}
	applyOutput := stdout
	if *asJSON {
		applyOutput = io.Discard
	}
	applyFailed := applyLoopKind(ctx, applyOutput, d, addr, actor, st)
	rev, revErr := st.Rev(ctx, config.KindLoop)
	if revErr != nil {
		return storeErr(stderr, verb, revErr, toolName+" apply")
	}
	if *asJSON {
		o := tool.Done().Fact("path", path).Fact("removed", removed.Changed).Fact("loop", applyLoopRow).Fact("rev", rev)
		o.Verb = verb
		if applyFailed != nil {
			o.Status, o.Exit = tool.Failed, 1
			o.Why = []string{(&applyErr{rev: rev, err: applyFailed}).Error()}
			emit(stdout, o)
			return 1
		}
		emit(stdout, o)
		return 0
	}
	fmt.Fprintf(stdout, "APPLY UNINSTALL OK unit=%s removed=%t loop=%s\n", oneline.Field(removed.Path), removed.Changed, applyLoopRow)
	if applyFailed != nil {
		fmt.Fprintln(stdout, (&applyErr{rev: rev, err: applyFailed}).Error())
		return 1
	}
	fmt.Fprintf(stdout, "APPLIED rev=%d\n", rev)
	return 0
}

func applyUnitFile(goos string) string {
	switch goos {
	case "darwin":
		return applyLoopLabel + ".plist"
	case "linux":
		return applyLoopService
	default:
		return ""
	}
}

// applyLoopArgs is the loop's command after the binary: apply --every, the
// Redis it writes, and the store it reads. --actor is set only when the
// environment will not name one, so a service whose NOVA_FRIEND moves keeps
// the name it was given.
func applyLoopArgs(every time.Duration, redis string, storeArgs []string, actor string, getenv func(string) string) []string {
	args := []string{"nova-config", "apply", "--every", every.String(), "--redis", redis}
	args = append(args, storeArgs...)
	friend := ""
	if getenv != nil {
		friend = getenv("NOVA_FRIEND")
	}
	if friend == "" && actor != "" {
		args = append(args, "--actor", actor)
	}
	return args
}

func applyLoopStoreArgs(c conn, getenv func(string) string) ([]string, error) {
	switch {
	case c.file != nil && *c.file != "":
		return nil, errors.New("the loop follows the fleet's store, and --file is this process's alone; run: nova-config apply install --pg <dsn>")
	case c.pg != nil && *c.pg != "":
		if _, err := config.ResolveDSN(*c.pg, func(string) string { return "" }); err != nil {
			return nil, err
		}
		return []string{"--pg", *c.pg}, nil
	case c.seat != nil && *c.seat != "":
		return []string{"--seat", *c.seat}, nil
	}
	raw := ""
	if getenv != nil {
		raw = getenv(config.EnvPG)
	}
	if raw == "" {
		return nil, fmt.Errorf("--pg is required: postgres://user@host:5432/nova (or %s), with no password", config.EnvPG)
	}
	if _, err := config.ResolveDSN(raw, func(string) string { return "" }); err != nil {
		return nil, err
	}
	return []string{"--pg", raw}, nil
}

func applyUnitEnv(getenv func(string) string) ([][2]string, error) {
	if getenv == nil {
		return nil, nil
	}
	var env [][2]string
	for _, key := range []string{config.EnvPGPassEnv, "NOVA_FRIEND", "NOVA_SEAT"} {
		val := getenv(key)
		if val == "" {
			continue
		}
		if key == config.EnvPGPassEnv && !envNameOK(val) {
			return nil, fmt.Errorf("%s does not name a variable (letters, digits and underscores only), and the unit carries no secret", key)
		}
		env = append(env, [2]string{key, val})
	}
	return env, nil
}

func envNameOK(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
		default:
			return false
		}
	}
	return true
}

func applyUnitPaths(host applyHost, getenv func(string) string, dir, log string) (string, string, error) {
	if dir == "" {
		home, err := host.home()
		if err != nil {
			return "", "", fmt.Errorf("--dir names no directory and the home cannot be read: %w", err)
		}
		dir = units.Dir(host.goos, home, getenv)
		if log == "" && host.goos == "darwin" {
			log = filepath.Join(home, "Library", "Logs", "nova-config-apply.log")
		}
	} else if log == "" && host.goos == "darwin" {
		home, err := host.home()
		if err != nil {
			return "", "", fmt.Errorf("--log names no file and the home cannot be read: %w", err)
		}
		log = filepath.Join(home, "Library", "Logs", "nova-config-apply.log")
	}
	return dir, log, nil
}

// applyUnitText is the unit file: a launchd agent or a systemd user unit that
// runs args (the binary first, absolute) and is restarted no sooner than 10s
// after it exits. The process itself waits --every.
func applyUnitText(goos, exe, log string, args []string, env [][2]string) (string, error) {
	if applyUnitFile(goos) == "" {
		return "", fmt.Errorf("apply install writes a launchd agent (macOS) or a systemd user unit (Linux), not a service on %s", goos)
	}
	if exe == "" || !filepath.IsAbs(exe) {
		return "", fmt.Errorf("the unit runs nova-config by its absolute path, not %s", exe)
	}
	if goos == "linux" {
		return units.SystemdUnit("nova-config apply loop (apply --every)", args, env, 10), nil
	}
	return units.LaunchdPlist(applyLoopLabel, args, env, log, 10), nil
}

func applyLoopRowOf(machine string, argv []string) (config.Row, error) {
	body, err := json.Marshal(argv)
	if err != nil {
		return config.Row{}, err
	}
	k, ok := config.Lookup(config.KindLoop)
	if !ok {
		return config.Row{}, errors.New("loop is not a kind")
	}
	return k.NewRow(applyLoopRow, map[string]string{
		"machine":   machine,
		"argv":      string(body),
		"keepalive": "true",
		"every":     "0",
		"enabled":   "true",
	})
}

// upsertApplyLoop inserts the loop row, or updates it when its fields differ.
// The same fields are left as they are.
func upsertApplyLoop(ctx context.Context, st pgStore, row config.Row, actor string) error {
	cur, found, err := st.Get(ctx, config.KindLoop, row.Name)
	if err != nil {
		return err
	}
	if !found {
		_, err = st.Insert(ctx, config.KindLoop, row, actor)
		return err
	}
	if maps.Equal(cur.Fields, row.Fields) {
		return nil
	}
	changes := map[string]string{}
	for key, val := range row.Fields {
		if cur.Fields[key] != val {
			changes[key] = val
		}
	}
	if len(changes) == 0 {
		return nil
	}
	_, _, err = st.Update(ctx, config.KindLoop, row.Name, changes, actor)
	return err
}
