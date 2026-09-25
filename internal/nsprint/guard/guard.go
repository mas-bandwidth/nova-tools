package guard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

type Config struct {
	MaxAge time.Duration
	Names  []string
	Spare  []string
}

func Default() Config {
	return Config{
		MaxAge: 300 * time.Second,
		Names:  []string{"rg", "find", "grep", "ugrep"},
		Spare:  []string{"harvest", "HARVEST", "SECRETS", "runner-nova-tools", "Runner.Worker"},
	}
}

type Proc struct {
	PID  int
	Age  time.Duration
	Comm string
	Args string
}

type ProcessTable interface {
	List() ([]Proc, error)
	Alive(pid int) bool
	Kill(pid int) error
	Term(pid int) error
}

type OSProcs struct {
	Timeout time.Duration
}

func (o OSProcs) List() ([]Proc, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid,etime,comm,args").Output()
	if err != nil {
		return nil, fmt.Errorf("the process table could not be listed: %w", err)
	}
	var procs []Proc
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		age, ok := pulse.ParseETime(fields[1])
		if !ok {
			continue
		}
		comm := fields[2]
		args := strings.Join(fields[3:], " ")
		procs = append(procs, Proc{PID: pid, Age: age, Comm: comm, Args: args})
	}
	return procs, nil
}

func (o OSProcs) Alive(pid int) bool {
	return pulse.OSProcs{Timeout: o.Timeout}.Alive(pid)
}

func (o OSProcs) Kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func (o OSProcs) Term(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

type KillRecord struct {
	At   string
	PID  int
	AgeS int
	Name string
	Args string
	Dry  int
}

type LastRecord struct {
	At      string
	Scanned int
	Killed  int
}

type RedisClient interface {
	GetConfig(bench string) (Config, error)
	SetConfig(bench string, cfg Config) error
	ExecutePass(bench string, kills []KillRecord, last LastRecord) error
}

type GuardInput struct {
	Bench     string
	DryRun    bool
	RedisAddr string
	Procs     ProcessTable
	Redis     RedisClient
	Now       func() time.Time
	Stdout    io.Writer
	Stderr    io.Writer
}

func resolveBench(bench string) string {
	if strings.TrimSpace(bench) != "" {
		return bench
	}
	if b := os.Getenv("NOVA_NOVA_BENCH"); b != "" {
		return b
	}
	if b := os.Getenv("NOVA_BENCH"); b != "" {
		return b
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		if idx := strings.Index(h, "."); idx > 0 {
			return h[:idx]
		}
		return h
	}
	return "localhost"
}

func Pass(in GuardInput) int {
	stdout := in.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := in.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	procs := in.Procs
	if procs == nil {
		procs = OSProcs{}
	}
	bench := resolveBench(in.Bench)

	redisCli := in.Redis
	if redisCli == nil {
		redisCli = NewRedisClient(in.RedisAddr)
	}

	// Round trip 1: HGETALL config
	cfg, err := redisCli.GetConfig(bench)
	if err != nil || len(cfg.Names) == 0 {
		cfg = Default()
	}

	rawProcs, err := procs.List()
	if err != nil {
		fmt.Fprintf(stderr, "GUARD NOTE process table could not be read: %s\n", oneline.Err(err))
		return 1
	}

	self, parent := os.Getpid(), os.Getppid()
	scanned := 0
	var kills []KillRecord
	killed := 0

	timestamp := now().Format(time.RFC3339)

	for _, p := range rawProcs {
		scanned++
		if p.PID <= 0 || p.PID == self || p.PID == parent {
			continue
		}
		if p.Age <= cfg.MaxAge {
			continue
		}
		// Check comm basename and argv[0] basename match one of Names
		commBase := filepath.Base(p.Comm)
		argv0Base := ""
		if fields := strings.Fields(p.Args); len(fields) > 0 {
			argv0Base = filepath.Base(fields[0])
		}
		matchedName := ""
		for _, name := range cfg.Names {
			if commBase == name && argv0Base == name {
				matchedName = name
				break
			}
		}
		if matchedName == "" {
			continue
		}

		// Check spare
		spared := false
		for _, s := range cfg.Spare {
			if strings.Contains(p.Args, s) {
				spared = true
				break
			}
		}
		if spared {
			continue
		}

		killed++
		dryVal := 0
		if in.DryRun {
			dryVal = 1
		} else {
			if err := procs.Term(p.PID); err != nil {
				fmt.Fprintf(stderr, "GUARD NOTE pid=%d could not be terminated: %s\n", p.PID, oneline.Err(err))
			}
		}

		truncatedArgs := oneline.Cap(p.Args, 120)
		kills = append(kills, KillRecord{
			At:   timestamp,
			PID:  p.PID,
			AgeS: int(p.Age.Seconds()),
			Name: matchedName,
			Args: truncatedArgs,
			Dry:  dryVal,
		})
	}

	last := LastRecord{
		At:      timestamp,
		Scanned: scanned,
		Killed:  killed,
	}

	// Round trip 2: pipeline of XADDs + HSET of last
	if err := redisCli.ExecutePass(bench, kills, last); err != nil {
		fmt.Fprintf(stderr, "GUARD NOTE redis pipeline failed: %s\n", oneline.Err(err))
		return 1
	}

	dryStr := "false"
	if in.DryRun {
		dryStr = "true"
	}
	fmt.Fprintf(stdout, "GUARD bench=%s scanned=%d killed=%d dry-run=%s\n", oneline.Field(bench), scanned, killed, dryStr)
	return 0
}

func SetConfig(addr, bench string, cfg Config) error {
	cli := NewRedisClient(addr)
	return cli.SetConfig(bench, cfg)
}

type tcpRedisClient struct {
	Addr string
}

func NewRedisClient(addr string) RedisClient {
	if strings.TrimSpace(addr) == "" {
		if env := os.Getenv("NOVA_REDIS"); env != "" {
			addr = env
		} else {
			addr = "127.0.0.1:6379"
		}
	}
	return &tcpRedisClient{Addr: addr}
}

func (c *tcpRedisClient) connect() (net.Conn, error) {
	return net.DialTimeout("tcp", c.Addr, 3*time.Second)
}

func (c *tcpRedisClient) GetConfig(bench string) (Config, error) {
	conn, err := c.connect()
	if err != nil {
		return Default(), err
	}
	defer conn.Close()

	key := fmt.Sprintf("bench:%s:guard", bench)
	cmd := fmt.Sprintf("*2\r\n$6\r\nHGETALL\r\n$%d\r\n%s\r\n", len(key), key)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return Default(), err
	}

	reader := bufio.NewReader(conn)
	cfg := Default()
	resp, err := readRESP(reader)
	if err != nil {
		return Default(), err
	}
	if arr, ok := resp.([]interface{}); ok {
		for i := 0; i < len(arr)-1; i += 2 {
			k, _ := arr[i].(string)
			v, _ := arr[i+1].(string)
			switch k {
			case "max_age":
				if secs, err := strconv.Atoi(v); err == nil {
					cfg.MaxAge = time.Duration(secs) * time.Second
				}
			case "names":
				if v != "" {
					cfg.Names = strings.Split(v, ",")
				}
			case "spare":
				if v != "" {
					cfg.Spare = strings.Split(v, ",")
				}
			}
		}
	}
	return cfg, nil
}

func (c *tcpRedisClient) SetConfig(bench string, cfg Config) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	key := fmt.Sprintf("bench:%s:guard", bench)
	maxAgeStr := strconv.Itoa(int(cfg.MaxAge.Seconds()))
	namesStr := strings.Join(cfg.Names, ",")
	spareStr := strings.Join(cfg.Spare, ",")

	cmd := fmt.Sprintf("*8\r\n$4\r\nHSET\r\n$%d\r\n%s\r\n$7\r\nmax_age\r\n$%d\r\n%s\r\n$5\r\nnames\r\n$%d\r\n%s\r\n$5\r\nspare\r\n$%d\r\n%s\r\n",
		len(key), key, len(maxAgeStr), maxAgeStr, len(namesStr), namesStr, len(spareStr), spareStr)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	_, err = readRESP(reader)
	return err
}

func (c *tcpRedisClient) ExecutePass(bench string, kills []KillRecord, last LastRecord) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	var sb strings.Builder
	streamKey := fmt.Sprintf("bench:%s:guard:kills", bench)
	lastHashKey := fmt.Sprintf("bench:%s:guard:last", bench)

	totalCmds := len(kills) + 1
	sb.WriteString(fmt.Sprintf("*%d\r\n", totalCmds))

	for _, k := range kills {
		pidStr := strconv.Itoa(k.PID)
		ageStr := strconv.Itoa(k.AgeS)
		dryStr := strconv.Itoa(k.Dry)
		args := []string{"XADD", streamKey, "*", "at", k.At, "pid", pidStr, "age_s", ageStr, "name", k.Name, "args", k.Args, "dry", dryStr}
		sb.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
		for _, arg := range args {
			sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
		}
	}

	scannedStr := strconv.Itoa(last.Scanned)
	killedStr := strconv.Itoa(last.Killed)
	lastArgs := []string{"HSET", lastHashKey, "at", last.At, "scanned", scannedStr, "killed", killedStr}
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(lastArgs)))
	for _, arg := range lastArgs {
		sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}

	if _, err := conn.Write([]byte(sb.String())); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	for i := 0; i < totalCmds; i++ {
		if _, err := readRESP(reader); err != nil {
			return err
		}
	}
	return nil
}

func readRESP(r *bufio.Reader) (interface{}, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 {
		return nil, fmt.Errorf("invalid resp line")
	}
	line = strings.TrimSuffix(line, "\r\n")
	prefix := line[0]
	val := line[1:]

	switch prefix {
	case '+':
		return val, nil
	case '-':
		return nil, fmt.Errorf("redis error: %s", val)
	case ':':
		return strconv.Atoi(val)
	case '$':
		n, err := strconv.Atoi(val)
		if err != nil || n < -1 {
			return nil, fmt.Errorf("invalid bulk string length")
		}
		if n == -1 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(val)
		if err != nil || n < -1 {
			return nil, fmt.Errorf("invalid array length")
		}
		if n == -1 {
			return nil, nil
		}
		arr := make([]interface{}, n)
		for i := 0; i < n; i++ {
			elem, err := readRESP(r)
			if err != nil {
				return nil, err
			}
			arr[i] = elem
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unknown resp prefix %c", prefix)
	}
}
