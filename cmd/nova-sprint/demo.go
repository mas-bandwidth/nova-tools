package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

func init() {
	// demo is the machine's, like selftest: it opens no store of the caller's,
	// only the throwaway Redis it starts (the class test, coordinator.go).
	verbClasses["demo load"] = classMachine
	verbClasses["demo stop"] = classMachine
	// the Redis it starts and the directory it keeps are this machine's
	notServed = append(notServed, "demo load", "demo stop")
}

// demoStateFile is the state file under the demo's root: the one demo up,
// its address, port, pid and directory.
const demoStateFile = "demo.json"

// demoState is what demo load records and demo stop acts on, and nothing
// else: the pid it signals and the directory it removes are these.
type demoState struct {
	Addr    string   `json:"addr"`
	Port    string   `json:"port"`
	PID     int      `json:"pid"`
	Dir     string   `json:"dir"`
	Parts   []string `json:"parts,omitempty"`
	Started string   `json:"started,omitempty"`
}

// demoServer is a Redis demo load started: its loopback address and its pid.
type demoServer struct {
	Addr string
	PID  int
}

// demoProcs is how the demo reaches processes: start starts the demo's Redis
// (the redis-server bin) in its own directory, alive says whether a pid still
// runs and kill stops the recorded one (demo_proc.go). A test gives its own
// for the root it loads under (demoHooks), so the server pkg/testredis
// starts stands in for one.
type demoProcs struct {
	start func(ctx context.Context, bin, dir string) (demoServer, error)
	alive func(pid int) bool
	kill  func(st demoState) error
}

var (
	demoHooksMu sync.Mutex
	demoHooks   = map[string]demoProcs{}
)

// demoProcsFor is the processes of the demo under root: a test's, for the
// root it registered, else the machine's.
func demoProcsFor(root string) demoProcs {
	demoHooksMu.Lock()
	p := demoHooks[root]
	demoHooksMu.Unlock()
	if p.start == nil {
		p.start = startDemoRedis
	}
	if p.alive == nil {
		p.alive = demoProcessAlive
	}
	if p.kill == nil {
		p.kill = killDemoRedis
	}
	return p
}

// demoReplayBatch is how many RESTORE lines go in one pipeline, and
// demoReplayBytes the most payload bytes one holds.
const (
	demoReplayBatch = 256
	demoReplayBytes = 32 << 20
)

// cmdDemoLoad loads a backup of the store (the RESTORE text dump, xz, split
// into parts) into a throwaway Redis on a free loopback port, with this
// build's function library, and prints where against it (SPEC-SPRINT,
// demo-load-verb). The live store is never opened.
func (a *app) cmdDemoLoad(args []string, stdout, stderr io.Writer) int {
	// the store flags are parsed and never used: the demo opens no store but
	// the one it starts
	fs, _ := a.verbSetup("demo load")
	rootFlag := fs.String("dir", "", "the directory the demo's Redis directory and state file are kept under (default: the user cache directory's nova-sprint/demo)")
	sumFlag := fs.String("sha256", "", "the sha256 of the joined .xz, when no <joined>.sha256 or SHA256SUMS sits beside the parts")
	xzFlag := fs.String("xz", "xz", "the xz program the backup is decompressed with")
	serverFlag := fs.String("redis-server", "redis-server", "the redis-server the demo runs, on a free 127.0.0.1 port")
	pos, err := parse(fs, args)
	if err != nil || len(pos) == 0 {
		return refuse(stderr, "demo load", argErr("wants the backup's parts ", err)+"; run: nova-sprint demo load sprint-store-2026-10-04-2336.redis.txt.xz.part-*")
	}
	if demoSet(fs, "redis") {
		return refuse(stderr, "demo load", "takes no --redis: the demo never opens a store but the throwaway one it starts; run: nova-sprint demo load <part>...")
	}
	root, err := demoRoot(*rootFlag)
	if err != nil {
		return refuse(stderr, "demo load", err.Error())
	}
	if st, err := readDemoState(root); err == nil {
		return demoFailed(stderr, "load", fmt.Sprintf("a demo is up at %s (pid %d, directory %s); run: nova-sprint demo stop%s", st.Addr, st.PID, st.Dir, demoDirArg(*rootFlag)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return demoFailed(stderr, "load", err.Error()+"; run: nova-sprint demo stop"+demoDirArg(*rootFlag))
	}
	parts, joined, err := demoParts(pos)
	if err != nil {
		return refuse(stderr, "demo load", err.Error())
	}
	if err := demoReadable(parts); err != nil {
		return demoFailed(stderr, "load", err.Error()+"; run: nova-sprint demo load <the backup's parts>")
	}
	checked, err := demoCheckSum(parts, joined, *sumFlag)
	if err != nil {
		return demoFailed(stderr, "load", err.Error())
	}
	if _, err := exec.LookPath(*xzFlag); err != nil {
		return demoFailed(stderr, "load", "no "+*xzFlag+" to decompress the backup ("+err.Error()+"); run: install xz, or nova-sprint demo load --xz <the path of xz>")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return demoFailed(stderr, "load", err.Error())
	}
	// the state file is claimed before the server starts: a second load
	// finds it and is refused, and a stop finds what this one started
	claim, err := os.OpenFile(filepath.Join(root, demoStateFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return demoFailed(stderr, "load", "a demo is being loaded or is up under "+root+" ("+err.Error()+"); run: nova-sprint demo stop"+demoDirArg(*rootFlag))
	}
	// ignored: the claim's handle; the state is written whole by writeDemoState
	_ = claim.Close()
	dir, err := os.MkdirTemp(root, "redis-")
	if err != nil {
		_ = os.Remove(filepath.Join(root, demoStateFile)) // ignored: the claim of a load that started nothing
		return demoFailed(stderr, "load", err.Error())
	}
	st := demoState{Dir: dir, Parts: parts, Started: a.now().UTC().Format(time.RFC3339)}
	if err := writeDemoState(root, st); err != nil {
		_ = safepath.RemoveUnder(root, dir)               // ignored: the directory this load made, empty
		_ = os.Remove(filepath.Join(root, demoStateFile)) // ignored: the claim of a load that started nothing
		return demoFailed(stderr, "load", err.Error())
	}
	ctx := context.Background()
	srv, err := demoProcsFor(root).start(ctx, *serverFlag, dir)
	if err != nil {
		return demoFailed(stderr, "load", err.Error()+demoUndo(root, st))
	}
	st.Addr, st.PID = srv.Addr, srv.PID
	host, port, err := net.SplitHostPort(srv.Addr)
	if err != nil || host != "127.0.0.1" {
		return demoFailed(stderr, "load", fmt.Sprintf("the demo's Redis listens on %s, not the IPv4 loopback", srv.Addr)+demoUndo(root, st))
	}
	st.Port = port
	if err := writeDemoState(root, st); err != nil {
		return demoFailed(stderr, "load", err.Error()+demoUndo(root, st))
	}
	c := redis.NewClient(&redis.Options{Addr: srv.Addr})
	keys, lib, err := demoFill(ctx, c, *xzFlag, parts)
	// ignored: a close of the demo's client; the fill's error is the one reported
	_ = c.Close()
	if err != nil {
		return demoFailed(stderr, "load", err.Error()+demoUndo(root, st))
	}
	fmt.Fprintf(stdout, "DEMO LOADED parts=%d sha256=%s keys=%d library=%s dir=%s pid=%d; the live store was not opened\n", len(parts), checked, keys, lib, dir, srv.PID)
	// where runs as a fresh process would, with no environment: no login,
	// no store but the demo's
	sub := newApp(func(string) string { return "" })
	sub.now, sub.loc = a.now, a.loc
	code := sub.run([]string{"where", "--redis", srv.Addr}, stdout, stderr)
	sub.close()
	fmt.Fprintf(stdout, "DEMO UP --addr %s: point a read verb at it with --redis %s (nova-redis takes --addr %s); stop it with: nova-sprint demo stop%s\n", srv.Addr, srv.Addr, srv.Addr, demoDirArg(*rootFlag))
	if code != 0 {
		return 1
	}
	return 0
}

// cmdDemoStop stops the Redis demo load started, by the pid in its state
// file and only when the Redis at the recorded address says it is that pid,
// and removes the recorded directory and the state file; nothing else.
func (a *app) cmdDemoStop(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("demo stop")
	rootFlag := fs.String("dir", "", "the directory demo load kept its state under (default: the user cache directory's nova-sprint/demo)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "demo stop", argErr("takes no words ", err, pos...))
	}
	if demoSet(fs, "redis") {
		return refuse(stderr, "demo stop", "takes no --redis: it stops the Redis its state file records; run: nova-sprint demo stop")
	}
	root, err := demoRoot(*rootFlag)
	if err != nil {
		return refuse(stderr, "demo stop", err.Error())
	}
	st, err := readDemoState(root)
	if errors.Is(err, os.ErrNotExist) {
		return demoFailed(stderr, "stop", "no demo is up under "+root+"; run: nova-sprint demo load <part>...")
	}
	if err != nil {
		return demoFailed(stderr, "stop", err.Error()+"; nothing was stopped or removed")
	}
	if err := stopDemo(root, st); err != nil {
		return demoFailed(stderr, "stop", err.Error())
	}
	fmt.Fprintf(stdout, "DEMO STOPPED addr=%s pid=%d removed=%s\n", st.Addr, st.PID, st.Dir)
	return 0
}

// stopDemo is demo stop's law: the recorded directory must be one demo load
// makes (directly under the root, named redis-*), the pid is signalled only
// when it is alive and the Redis at the recorded address answers with that
// process_id, and then the directory and the state file are removed by their
// literal paths.
func stopDemo(root string, st demoState) error {
	if !filepath.IsAbs(st.Dir) || filepath.Clean(st.Dir) != st.Dir || filepath.Dir(st.Dir) != root || !strings.HasPrefix(filepath.Base(st.Dir), "redis-") {
		return fmt.Errorf("the state file names the directory %q, which is not one demo load makes under %s; nothing was stopped or removed", st.Dir, root)
	}
	procs := demoProcsFor(root)
	if st.PID > 0 && procs.alive(st.PID) {
		if err := demoOwns(st); err != nil {
			return fmt.Errorf("pid %d is running and is not the demo's Redis (%v); nothing was stopped or removed", st.PID, err)
		}
		if err := procs.kill(st); err != nil {
			return fmt.Errorf("pid %d did not stop: %v; nothing was removed", st.PID, err)
		}
	}
	if err := safepath.RemoveUnder(root, st.Dir); err != nil {
		return fmt.Errorf("the demo's directory %s cannot be removed: %v", st.Dir, err)
	}
	if err := os.Remove(filepath.Join(root, demoStateFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// demoOwns asks the Redis at the recorded address for its process_id: the
// pid is the demo's only when it answers with that pid.
func demoOwns(st demoState) error {
	if st.Addr == "" {
		return errors.New("the state file records no address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// a client that never waits long on an address that does not answer
	c := redis.NewClient(&redis.Options{Addr: st.Addr, DialTimeout: 2 * time.Second, MaxRetries: -1})
	defer c.Close() // ignored: closing a probe client that only read the server's INFO, nothing is left to report it to
	info, err := c.Info(ctx, "server").Result()
	if err != nil {
		return fmt.Errorf("%s does not answer: %v", st.Addr, err)
	}
	for _, l := range strings.Split(info, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "process_id:"); ok {
			if v == strconv.Itoa(st.PID) {
				return nil
			}
			return fmt.Errorf("%s is pid %s", st.Addr, v)
		}
	}
	return fmt.Errorf("%s names no process_id", st.Addr)
}

// demoUndo stops what a failed load started and removes its directory and
// state, and says so for the failure's line.
func demoUndo(root string, st demoState) string {
	if err := stopDemo(root, st); err != nil {
		return "; the demo was not cleaned up (" + err.Error() + "); run: nova-sprint demo stop"
	}
	return "; the demo's Redis was stopped and its directory removed"
}

// demoSet is whether the flag was given on the command line (an environment
// default is not given).
func demoSet(fs flagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func demoFailed(stderr io.Writer, verb, what string) int {
	fmt.Fprintf(stderr, "%s demo %s FAILED: %s\n", prog, verb, oneline.Escape(what))
	return 1
}

func demoDirArg(flag string) string {
	if flag == "" {
		return ""
	}
	return " --dir " + flag
}

// demoRoot is the absolute directory the demo keeps its state under.
func demoRoot(flag string) (string, error) {
	if flag == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("no user cache directory (%v); run: nova-sprint demo load --dir <dir> <part>", err)
		}
		flag = filepath.Join(cache, "nova-sprint", "demo")
	}
	return filepath.Abs(flag)
}

func readDemoState(root string) (demoState, error) {
	var st demoState
	b, err := os.ReadFile(filepath.Join(root, demoStateFile))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("the state file %s cannot be read: %v", filepath.Join(root, demoStateFile), err)
	}
	return st, nil
}

func writeDemoState(root string, st demoState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, demoStateFile), append(b, '\n'))
}

// demoParts is the parts in name order and the name of the file they join
// into: <joined>.part-<suffix> each, one joined name; or one whole file.
func demoParts(args []string) ([]string, string, error) {
	parts := append([]string(nil), args...)
	sort.SliceStable(parts, func(i, j int) bool {
		bi, bj := filepath.Base(parts[i]), filepath.Base(parts[j])
		if bi != bj {
			return bi < bj
		}
		return parts[i] < parts[j]
	})
	joined := ""
	for i, p := range parts {
		if i > 0 && filepath.Base(p) == filepath.Base(parts[i-1]) {
			return nil, "", fmt.Errorf("the part %s is given twice", filepath.Base(p))
		}
		at := strings.LastIndex(p, ".part-")
		stem := p
		if at >= 0 {
			stem = p[:at]
		} else if len(parts) > 1 {
			return nil, "", fmt.Errorf("%s is not a part (<file>.part-<suffix>) and more than one file was given", p)
		}
		if i > 0 && stem != joined {
			return nil, "", fmt.Errorf("the parts are of two files, %s and %s", joined, stem)
		}
		joined = stem
	}
	return parts, joined, nil
}

// demoReadable is nil when every part is a file that can be read.
func demoReadable(parts []string) error {
	for _, p := range parts {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a file", p)
		}
	}
	return nil
}

// demoCheckSum checks the parts, joined, against --sha256 or the sum beside
// them: the file <joined>.sha256 (the hand backup's), else the line of
// SHA256SUMS naming the joined file (backup --out's); with none it says
// unchecked.
func demoCheckSum(parts []string, joined, want string) (string, error) {
	from := "--sha256"
	if want == "" {
		var err error
		if want, from, err = demoSumBeside(joined); err != nil {
			return "", err
		}
		if want == "" {
			return "unchecked", nil
		}
	}
	want = strings.ToLower(strings.TrimSpace(want))
	h := sha256.New()
	r, closeAll, err := openParts(parts)
	if err != nil {
		return "", err
	}
	defer closeAll()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return "", fmt.Errorf("the parts joined are sha256 %s and %s says %s; the backup is damaged or a part is missing, and nothing was started", got, from, want)
	}
	return "checked", nil
}

// demoSumBeside is the sha256 a sum file beside the parts gives the joined
// file, and the file it was read from; none is "".
func demoSumBeside(joined string) (string, string, error) {
	side := joined + ".sha256"
	b, err := os.ReadFile(side)
	if err == nil {
		want, _, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
		return want, side, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	sums := filepath.Join(filepath.Dir(joined), "SHA256SUMS")
	b, err = os.ReadFile(sums)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == filepath.Base(joined) {
			return f[0], sums, nil
		}
	}
	return "", "", nil
}

// openParts is the parts read as one file, in the order given.
func openParts(parts []string) (io.Reader, func(), error) {
	var rs []io.Reader
	var fs []*os.File
	closeAll := func() {
		for _, f := range fs {
			// ignored: a close of a file only read
			_ = f.Close()
		}
	}
	for _, p := range parts {
		f, err := os.Open(p)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		fs = append(fs, f)
		rs = append(rs, bufio.NewReaderSize(f, 1<<20))
	}
	return io.MultiReader(rs...), closeAll, nil
}

// demoFill loads this build's function library into the demo's Redis (a
// DUMP copies keys, never functions) and replays the dump; it returns the
// keys restored and the library's digest, checked to be this build's.
func demoFill(ctx context.Context, c *redis.Client, xz string, parts []string) (int, string, error) {
	if err := fn.Load(ctx, c); err != nil {
		return 0, "", err
	}
	source, err := fn.Source()
	if err != nil {
		return 0, "", err
	}
	code, found, err := fn.Loaded(ctx, c)
	if err != nil {
		return 0, "", err
	}
	if !found || fn.Sum(code) != fn.Sum(source) {
		return 0, "", fmt.Errorf("the demo's Redis does not hold this build's %s library %s after loading it", fn.Library, fn.Sum(source))
	}
	n, err := demoReplay(ctx, c, xz, parts)
	if err != nil {
		return 0, "", err
	}
	size, err := c.DBSize(ctx).Result()
	if err != nil {
		return 0, "", err
	}
	if size != int64(n) {
		return 0, "", fmt.Errorf("the dump restored %d keys and the demo's Redis holds %d", n, size)
	}
	return n, fn.Sum(source), nil
}

// demoReplay decompresses the joined parts through the system xz and sends
// each RESTORE line, pipelined; a line of any other command is refused.
func demoReplay(ctx context.Context, c *redis.Client, xzBin string, parts []string) (int, error) {
	in, closeAll, err := openParts(parts)
	if err != nil {
		return 0, err
	}
	defer closeAll()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	xz := subproc.Context(ctx, xzBin, "-dc")
	xz.Stdin = in
	var errb bytes.Buffer
	xz.Stderr = &errb
	out, err := xz.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := xz.Start(); err != nil {
		return 0, err
	}
	n, err := demoReplayLines(ctx, c, out)
	if err != nil {
		cancel()
		_ = xz.Wait() // ignored: xz is ended by the cancel; the replay's error is the one returned
		return n, err
	}
	if err := xz.Wait(); err != nil {
		return n, fmt.Errorf("%s -dc failed: %v %s", xzBin, err, strings.TrimSpace(errb.String()))
	}
	return n, nil
}

// demoReplayLines sends the RESTORE lines of a dump; it returns how many.
func demoReplayLines(ctx context.Context, c *redis.Client, r io.Reader) (int, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	pipe := c.Pipeline()
	var cmds []*redis.StatusCmd
	var keys []string
	held, n, line := 0, 0, 0
	flush := func() error {
		if len(cmds) == 0 {
			return nil
		}
		// ignored: each command's own error is read below, naming its key
		_, _ = pipe.Exec(ctx)
		for i, cmd := range cmds {
			if err := cmd.Err(); err != nil {
				return fmt.Errorf("RESTORE %s: %v", keys[i], err)
			}
		}
		n += len(cmds)
		cmds, keys, held = cmds[:0], keys[:0], 0
		return nil
	}
	for {
		b, err := br.ReadBytes('\n')
		if len(b) > 0 {
			line++
			s := strings.TrimRight(string(b), "\r\n")
			if t := strings.TrimSpace(s); t != "" && !strings.HasPrefix(t, "#") {
				args, perr := demoSplitArgs(s)
				switch {
				case perr != nil:
					return n, fmt.Errorf("line %d of the dump cannot be read: %v", line, perr)
				case len(args) == 0:
				case !strings.EqualFold(args[0], "RESTORE") || len(args) < 4:
					return n, fmt.Errorf("line %d of the dump is not a RESTORE <key> <ttl> <payload> line; the demo replays only RESTORE lines", line)
				default:
					cmdArgs := make([]any, len(args))
					for i, a := range args {
						cmdArgs[i] = a
					}
					cmd := redis.NewStatusCmd(ctx, cmdArgs...)
					// ignored: the queued command's error is read after Exec
					_ = pipe.Process(ctx, cmd)
					cmds, keys = append(cmds, cmd), append(keys, args[1])
					held += len(s)
					if len(cmds) >= demoReplayBatch || held >= demoReplayBytes {
						if err := flush(); err != nil {
							return n, err
						}
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, err
		}
	}
	if err := flush(); err != nil {
		return n, err
	}
	return n, nil
}

// demoSplitArgs splits a line as redis-cli reads one from its input
// (sdssplitargs): words split on blanks; a "double-quoted" word takes
// \xNN, \n, \r, \t, \b, \a and \<c> escapes; a 'single-quoted' word takes \'.
// A closing quote must be followed by blanks or the end.
func demoSplitArgs(line string) ([]string, error) {
	var out []string
	p, end := 0, len(line)
	blank := func(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\v' || c == '\f' }
	isHex := func(c byte) bool { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
	for {
		for p < end && blank(line[p]) {
			p++
		}
		if p >= end {
			return out, nil
		}
		var cur []byte
		inq, insq := false, false
		for done := false; !done; {
			switch {
			case inq:
				switch {
				case p >= end:
					return nil, errors.New("unbalanced double quotes")
				case line[p] == '\\' && p+3 < end && line[p+1] == 'x' && isHex(line[p+2]) && isHex(line[p+3]):
					v, _ := strconv.ParseUint(line[p+2:p+4], 16, 8)
					cur = append(cur, byte(v))
					p += 3
				case line[p] == '\\' && p+1 < end:
					p++
					c := line[p]
					switch c {
					case 'n':
						c = '\n'
					case 'r':
						c = '\r'
					case 't':
						c = '\t'
					case 'b':
						c = '\b'
					case 'a':
						c = '\a'
					}
					cur = append(cur, c)
				case line[p] == '"':
					if p+1 < end && !blank(line[p+1]) {
						return nil, errors.New("a closing quote not followed by a blank")
					}
					done = true
				default:
					cur = append(cur, line[p])
				}
			case insq:
				switch {
				case p >= end:
					return nil, errors.New("unbalanced single quotes")
				case line[p] == '\\' && p+1 < end && line[p+1] == '\'':
					p++
					cur = append(cur, '\'')
				case line[p] == '\'':
					if p+1 < end && !blank(line[p+1]) {
						return nil, errors.New("a closing quote not followed by a blank")
					}
					done = true
				default:
					cur = append(cur, line[p])
				}
			default:
				switch {
				case p >= end || blank(line[p]) || line[p] == 0:
					done = true
				case line[p] == '"':
					inq = true
				case line[p] == '\'':
					insq = true
				default:
					cur = append(cur, line[p])
				}
			}
			if p < end {
				p++
			}
		}
		out = append(out, string(cur))
	}
}
