package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const reserveHelp = `nova-fleet reserve --needs "<spec>" [--holder friend] [--consumer <id>] [--label <lbl>] [--priority <prio>] [--wait <dur>] [--json] [-- <remote-cmd>...]

Request an atomic reservation across eligible fleet machines, locking the hold before SSH.
Filters machines that have the bench role and satisfy all required tags and capacities (R12).
Attempts atomic hold acquisition across candidates so the first machine to lock wins;
if all are currently full, waits across candidates up to --wait.
Prints RESERVED host=<name> address=<addr> user=<user> cores=<C> (or JSON).
If a command is given after --, executes it inside the reservation; otherwise keeps the
hold open until stdin EOF or SIGINT/SIGTERM.
Exit: 0 reserved (and remote command passed); 1 no room or remote command failed; 2 could not run.
`

const resourcesHelp = `nova-fleet resources [--json]

Query the resource status across fleet machines in parallel (R21).
Reads the machines from nova-config and queries nova-reserve status --json over SSH.
Exit: 0 status queried; 2 could not run.
`

type fleetMachine struct {
	Name      string         `json:"name"`
	Address   string         `json:"address"`
	User      string         `json:"user"`
	OS        string         `json:"os"`
	Arch      string         `json:"arch"`
	Cores     *int           `json:"cores"`
	Roles     string         `json:"roles"`
	CPUUnits  *int           `json:"cpu_units"`
	MemoryGB  *int           `json:"memory_gb"`
	Tags      string         `json:"tags"`
	Resources map[string]int `json:"resources"`
}

func (m fleetMachine) EffectiveOS() string {
	if m.OS == "darwin" {
		return "mac"
	}
	return m.OS
}

func (m fleetMachine) EffectiveTags() []string {
	seen := map[string]bool{}
	var tags []string
	add := func(s string) {
		s = strings.TrimSpace(strings.ToLower(s))
		if s != "" && !seen[s] {
			seen[s] = true
			tags = append(tags, s)
		}
	}
	for _, t := range strings.Split(m.Tags, ",") {
		add(t)
	}
	add(m.EffectiveOS())
	add(m.Arch)
	slices.Sort(tags)
	return tags
}

func (m fleetMachine) EffectiveResources() map[string]int {
	res := map[string]int{}
	for k, v := range m.Resources {
		res[k] = v
	}
	if m.CPUUnits != nil && *m.CPUUnits > 0 {
		res["cpu"] = *m.CPUUnits
	} else if m.Cores != nil && *m.Cores > 0 {
		res["cpu"] = *m.Cores
	}
	if m.MemoryGB != nil && *m.MemoryGB > 0 {
		res["memory"] = *m.MemoryGB
	}
	return res
}

type parsedNeeds struct {
	Raw     string
	Amounts map[string]int
	Bare    []string
}

func parseNeeds(spec string) (parsedNeeds, error) {
	out := parsedNeeds{
		Raw:     spec,
		Amounts: map[string]int{"cpu": 1, "memory": 1},
	}
	if strings.TrimSpace(spec) == "" {
		return out, nil
	}
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Fields(p)
		switch len(fields) {
		case 1:
			tok := strings.ToLower(fields[0])
			if _, err := strconv.Atoi(tok); err == nil {
				return out, fmt.Errorf("bare amount %s without resource name", tok)
			}
			if tok == "cpu" || tok == "memory" {
				out.Amounts[tok] = 1
			} else {
				out.Bare = append(out.Bare, tok)
			}
		case 2:
			f0, f1 := strings.ToLower(fields[0]), strings.ToLower(fields[1])
			name, numStr := f0, f1
			if _, err := strconv.Atoi(f0); err == nil {
				name, numStr = f1, f0
			}
			num, err := strconv.Atoi(numStr)
			if err != nil || num < 1 {
				return out, fmt.Errorf("resource %s wants integer amount >= 1, got %q", name, numStr)
			}
			out.Amounts[name] = num
		default:
			return out, fmt.Errorf("malformed token %q", p)
		}
	}
	return out, nil
}

func (m fleetMachine) FitsMax(n parsedNeeds) bool {
	tagSet := map[string]bool{}
	for _, t := range m.EffectiveTags() {
		tagSet[t] = true
	}
	effRes := m.EffectiveResources()
	for _, b := range n.Bare {
		if cap, ok := effRes[b]; ok {
			if cap < 1 {
				return false
			}
		} else {
			if !tagSet[b] {
				return false
			}
		}
	}
	for r, need := range n.Amounts {
		cap, ok := effRes[r]
		if !ok || cap < need {
			return false
		}
	}
	return true
}

type holdSession interface {
	StdoutLine() (string, error)
	Close() error
	Wait() (int, error)
}

type realHoldSession struct {
	cmd    *exec.Cmd
	reader *bufio.Reader
	stdin  io.WriteCloser
	cancel context.CancelFunc
}

func (s *realHoldSession) StdoutLine() (string, error) {
	line, err := s.reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func (s *realHoldSession) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.stdin != nil {
		_ = s.stdin.Close() // ignored: process being closed
	}
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

func (s *realHoldSession) Wait() (int, error) {
	err := s.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return max(ee.ExitCode(), 1), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

type reserveSeam struct {
	configMachines func() ([]fleetMachine, error)
	probeHold      func(ctx context.Context, m fleetMachine, holdArgs []string) (holdSession, error)
	runRemote      func(m fleetMachine, cmd []string, stdin io.Reader, stdout, stderr io.Writer) int
	queryStatus    func(ctx context.Context, m fleetMachine) (string, error)
}

func defaultProbeHold(ctx context.Context, m fleetMachine, holdArgs []string) (holdSession, error) {
	cCtx, cancel := context.WithCancel(ctx)
	target := m.Address
	if m.User != "" {
		target = m.User + "@" + m.Address
	}
	remoteCmd := fmt.Sprintf("PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH nova-reserve hold %s", strings.Join(holdArgs, " "))
	cmd := exec.CommandContext(cCtx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target, remoteCmd)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	return &realHoldSession{
		cmd:    cmd,
		reader: bufio.NewReader(stdoutPipe),
		stdin:  stdinPipe,
		cancel: cancel,
	}, nil
}

func defaultRunRemote(m fleetMachine, cmd []string, stdin io.Reader, stdout, stderr io.Writer) int {
	target := m.Address
	if m.User != "" {
		target = m.User + "@" + m.Address
	}
	c := exec.Command("ssh", append([]string{"-o", "BatchMode=yes", target, "--"}, cmd...)...)
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return max(ee.ExitCode(), 1)
		}
		return 1
	}
	return 0
}

func defaultQueryStatus(ctx context.Context, m fleetMachine) (string, error) {
	target := m.Address
	if m.User != "" {
		target = m.User + "@" + m.Address
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target,
		"PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH nova-reserve status --json")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func defaultLoadMachines() ([]fleetMachine, error) {
	cfg, err := lookPath("nova-config")
	if err != nil {
		return nil, errors.New("nova-config not on PATH; run: nova-fleet check")
	}
	out, why, code := outCmd(cfg, []string{"machine", "list", "--json"}, os.Environ())
	if code != 0 {
		return nil, fmt.Errorf("nova-config machine list --json exit %d: %s", code, strings.TrimSpace(why))
	}
	var raw struct {
		Items []struct {
			Kind   string          `json:"kind"`
			Fields json.RawMessage `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		var direct []fleetMachine
		if err2 := json.Unmarshal([]byte(out), &direct); err2 == nil {
			return direct, nil
		}
		return nil, fmt.Errorf("unmarshal machine list: %w", err)
	}
	var res []fleetMachine
	for _, it := range raw.Items {
		var m fleetMachine
		if len(it.Fields) > 0 {
			if err := json.Unmarshal(it.Fields, &m); err == nil {
				res = append(res, m)
			}
		}
	}
	return res, nil
}

var (
	reserveSeamMu     sync.RWMutex
	activeReserveSeam = reserveSeam{
		configMachines: defaultLoadMachines,
		probeHold:      defaultProbeHold,
		runRemote:      defaultRunRemote,
		queryStatus:    defaultQueryStatus,
	}
)

func getReserveSeam() reserveSeam {
	reserveSeamMu.RLock()
	defer reserveSeamMu.RUnlock()
	s := activeReserveSeam
	if s.configMachines == nil {
		s.configMachines = defaultLoadMachines
	}
	if s.probeHold == nil {
		s.probeHold = defaultProbeHold
	}
	if s.runRemote == nil {
		s.runRemote = defaultRunRemote
	}
	if s.queryStatus == nil {
		s.queryStatus = defaultQueryStatus
	}
	return s
}

var heldCorePattern = regexp.MustCompile(`cores=(\d+)`)

func extractCores(line string, fallback int) int {
	res := max(fallback, 1)
	if m := heldCorePattern.FindStringSubmatch(line); m != nil {
		if c, err := strconv.Atoi(m[1]); err == nil && c > 0 {
			res = c
		}
	}
	return res
}

func runReserve(args []string, stdout, stderr io.Writer) int {
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "nova-fleet reserve: %s; run: nova-fleet reserve -h\n", fmt.Sprintf(format, a...))
		return 2
	}
	var needsSpec, holder, consumer, label, priority, waitStr string
	asJSON := false
	var remoteCmd []string

	holder = "friend"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, reserveHelp)
			return 0
		case a == "--":
			remoteCmd = args[i+1:]
			i = len(args)
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "--needs="):
			needsSpec = strings.TrimPrefix(a, "--needs=")
		case a == "--needs":
			if i+1 >= len(args) {
				return refuse("--needs wants a resource spec")
			}
			i++
			needsSpec = args[i]
		case strings.HasPrefix(a, "--holder="):
			holder = strings.TrimPrefix(a, "--holder=")
		case a == "--holder":
			if i+1 >= len(args) {
				return refuse("--holder wants a name")
			}
			i++
			holder = args[i]
		case strings.HasPrefix(a, "--consumer="):
			consumer = strings.TrimPrefix(a, "--consumer=")
		case a == "--consumer":
			if i+1 >= len(args) {
				return refuse("--consumer wants an ID")
			}
			i++
			consumer = args[i]
		case strings.HasPrefix(a, "--label="):
			label = strings.TrimPrefix(a, "--label=")
		case a == "--label":
			if i+1 >= len(args) {
				return refuse("--label wants a label")
			}
			i++
			label = args[i]
		case strings.HasPrefix(a, "--priority="):
			priority = strings.TrimPrefix(a, "--priority=")
		case a == "--priority":
			if i+1 >= len(args) {
				return refuse("--priority wants a priority")
			}
			i++
			priority = args[i]
		case strings.HasPrefix(a, "--wait="):
			waitStr = strings.TrimPrefix(a, "--wait=")
		case a == "--wait":
			if i+1 >= len(args) {
				return refuse("--wait wants a duration")
			}
			i++
			waitStr = args[i]
		case strings.HasPrefix(a, "-"):
			return refuse("unknown flag %s", a)
		default:
			return refuse("unexpected argument %s (remote commands go after --)", a)
		}
	}

	if needsSpec == "" {
		return refuse("--needs wants a resource spec (e.g. --needs \"cpu 4, memory 8\")")
	}

	parsed, err := parseNeeds(needsSpec)
	if err != nil {
		return refuse("invalid --needs spec: %v", err)
	}

	var waitDur time.Duration
	if waitStr != "" {
		d, err := time.ParseDuration(waitStr)
		if err != nil || d < 0 {
			return refuse("invalid --wait %q: %v", waitStr, err)
		}
		waitDur = d
	}

	seam := getReserveSeam()
	machines, err := seam.configMachines()
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet reserve: %v\n", err)
		return 2
	}

	var benches []fleetMachine
	for _, m := range machines {
		roles := strings.Split(m.Roles, ",")
		if slices.Contains(roles, "bench") {
			benches = append(benches, m)
		}
	}
	if len(benches) == 0 {
		fmt.Fprintf(stderr, "nova-fleet reserve: no bench machines in fleet; run: nova-config machine list\n")
		return 1
	}

	var eligible []fleetMachine
	for _, m := range benches {
		if m.FitsMax(parsed) {
			eligible = append(eligible, m)
		}
	}
	if len(eligible) == 0 {
		fmt.Fprintf(stderr, "nova-fleet reserve: no machine in the fleet can fit needs %q (R12); run: nova-config machine list\n", needsSpec)
		return 1
	}

	makeHoldArgs := func(nowait bool, wait string) []string {
		var a []string
		if nowait {
			a = append(a, "--nowait")
		} else if wait != "" {
			a = append(a, "--wait", wait)
		}
		a = append(a, "--needs", fmt.Sprintf("%q", needsSpec))
		if holder != "" {
			a = append(a, "--holder", holder)
		}
		if consumer != "" {
			a = append(a, "--consumer", consumer)
		}
		if label != "" {
			a = append(a, "--label", label)
		}
		if priority != "" {
			a = append(a, "--priority", priority)
		}
		return a
	}

	type probeResult struct {
		machine fleetMachine
		session holdSession
		line    string
		err     error
	}

	attemptProbe := func(ctx context.Context, cands []fleetMachine, nowait bool, wait string) (fleetMachine, holdSession, string, error) {
		pCtx, pCancel := context.WithCancel(ctx)
		defer pCancel()

		resCh := make(chan probeResult, len(cands))
		args := makeHoldArgs(nowait, wait)

		var (
			sessMu   sync.Mutex
			sessions []holdSession
		)

		for _, m := range cands {
			go func(mach fleetMachine) {
				sess, err := seam.probeHold(pCtx, mach, args)
				if err != nil {
					resCh <- probeResult{machine: mach, err: err}
					return
				}
				sessMu.Lock()
				sessions = append(sessions, sess)
				sessMu.Unlock()
				if pCtx.Err() != nil {
					_ = sess.Close() // ignored: context cancelled
					resCh <- probeResult{machine: mach, err: pCtx.Err()}
					return
				}
				line, err := sess.StdoutLine()
				if err == nil && strings.HasPrefix(line, "HELD") {
					resCh <- probeResult{machine: mach, session: sess, line: line}
					return
				}
				_ = sess.Close() // ignored: hold not granted
				resCh <- probeResult{machine: mach, err: fmt.Errorf("not held: %s (err: %v)", line, err)}
			}(m)
		}

		var failed int
		var firstErr error
		for failed < len(cands) {
			select {
			case <-pCtx.Done():
				return fleetMachine{}, nil, "", pCtx.Err()
			case r := <-resCh:
				if r.session != nil {
					pCancel()
					sessMu.Lock()
					for _, s := range sessions {
						if s != r.session {
							_ = s.Close() // ignored: closing losing probe session
						}
					}
					sessMu.Unlock()
					return r.machine, r.session, r.line, nil
				}
				failed++
				if firstErr == nil {
					firstErr = r.err
				}
			}
		}
		return fleetMachine{}, nil, "", cmpError(firstErr, errors.New("no room right now"))
	}

	ctx := context.Background()
	winMachine, winSession, heldLine, err := attemptProbe(ctx, eligible, true, "")
	if err != nil && waitDur > 0 {
		wCtx, wCancel := context.WithTimeout(ctx, waitDur)
		defer wCancel()
		winMachine, winSession, heldLine, err = attemptProbe(wCtx, eligible, false, waitStr)
	}

	if err != nil {
		if waitDur > 0 {
			fmt.Fprintf(stderr, "nova-fleet reserve: no room on any eligible machine within %s; run: nova-fleet resources\n", waitStr)
		} else {
			fmt.Fprintf(stderr, "nova-fleet reserve: no room on any eligible machine right now; run: nova-fleet resources\n")
		}
		return 1
	}
	defer func() {
		if winSession != nil {
			_ = winSession.Close() // ignored: releasing winning session
		}
	}()

	cores := 1
	if winMachine.Cores != nil {
		cores = *winMachine.Cores
	}
	cores = extractCores(heldLine, cores)

	if asJSON {
		b, err := json.Marshal(map[string]any{
			"host":    winMachine.Name,
			"address": winMachine.Address,
			"user":    winMachine.User,
			"cores":   cores,
		})
		if err != nil {
			fmt.Fprintf(stderr, "nova-fleet reserve: json marshal: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintf(stdout, "RESERVED host=%s address=%s user=%s cores=%d\n", winMachine.Name, winMachine.Address, winMachine.User, cores)
	}

	if len(remoteCmd) > 0 {
		return seam.runRemote(winMachine, remoteCmd, os.Stdin, stdout, stderr)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			n, rErr := os.Stdin.Read(buf)
			if n > 0 {
				continue
			}
			if rErr != nil {
				break
			}
		}
		close(done)
	}()

	select {
	case <-sigCh:
		return 0
	case <-done:
		return 0
	}
}

func cmpError(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func runResources(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, resourcesHelp)
			return 0
		case a == "--json":
			asJSON = true
		default:
			fmt.Fprintf(stderr, "nova-fleet resources: unknown flag %s; run: nova-fleet resources -h\n", a)
			return 2
		}
	}

	seam := getReserveSeam()
	machines, err := seam.configMachines()
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet resources: %v\n", err)
		return 2
	}

	var benches []fleetMachine
	for _, m := range machines {
		roles := strings.Split(m.Roles, ",")
		if slices.Contains(roles, "bench") {
			benches = append(benches, m)
		}
	}

	type statusRow struct {
		Machine string `json:"machine"`
		Address string `json:"address"`
		Status  string `json:"status"`
		Data    any    `json:"data,omitempty"`
		Error   string `json:"error,omitempty"`
	}

	var mu sync.Mutex
	rows := make([]statusRow, len(benches))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i, m := range benches {
		wg.Add(1)
		go func(idx int, mach fleetMachine) {
			defer wg.Done()
			raw, err := seam.queryStatus(ctx, mach)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				rows[idx] = statusRow{
					Machine: mach.Name,
					Address: mach.Address,
					Status:  "unreachable",
					Error:   err.Error(),
				}
				return
			}
			var parsed any
			if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr == nil {
				rows[idx] = statusRow{
					Machine: mach.Name,
					Address: mach.Address,
					Status:  "ok",
					Data:    parsed,
				}
			} else {
				rows[idx] = statusRow{
					Machine: mach.Name,
					Address: mach.Address,
					Status:  "ok",
					Data:    raw,
				}
			}
		}(i, m)
	}
	wg.Wait()

	if asJSON {
		b, err := json.Marshal(rows)
		if err != nil {
			fmt.Fprintf(stderr, "nova-fleet resources: json marshal: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}

	for _, r := range rows {
		if r.Status == "ok" {
			summary := ""
			if mData, ok := r.Data.(map[string]any); ok {
				var cpuTotal, memTotal int
				if res, ok := mData["resources"].(map[string]any); ok {
					if c, ok := res["cpu"].(float64); ok {
						cpuTotal = int(c)
					}
					if m, ok := res["memory"].(float64); ok {
						memTotal = int(m)
					}
				}
				var cpuHeld, memHeld int
				if labels, ok := mData["held_by_label"].(map[string]any); ok {
					for _, v := range labels {
						if vMap, ok := v.(map[string]any); ok {
							if c, ok := vMap["cpu"].(float64); ok {
								cpuHeld += int(c)
							}
							if m, ok := vMap["memory"].(float64); ok {
								memHeld += int(m)
							}
						}
					}
				}
				var tags []string
				if tArr, ok := mData["tags"].([]any); ok {
					for _, t := range tArr {
						tags = append(tags, fmt.Sprint(t))
					}
				}
				summary = fmt.Sprintf(" cpu=%d/%d memory=%d/%d tags=%s", cpuHeld, cpuTotal, memHeld, memTotal, strings.Join(tags, ","))
			}
			fmt.Fprintf(stdout, "RESOURCE %s status=ok%s\n", r.Machine, summary)
		} else {
			fmt.Fprintf(stdout, "RESOURCE %s status=unreachable (%s)\n", r.Machine, r.Error)
		}
	}
	return 0
}
