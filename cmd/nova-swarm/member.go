package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// cmdMember is `nova-swarm member`: this machine as one member of a sprint's
// fleet (or one of its readers). Every few seconds it beats, reads its queue
// from the sprint, reports every card whose child ended, takes up to its width
// and starts each card taken as one child through `nova-swarm native`. The
// fleet table is the dispatcher; the loop is internal/member.
func cmdMember(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("member", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &flags{verb: "member", fs: fs}
	as := fs.String("as", "", "")
	width := fs.Int("width", 0, "")
	reader := fs.Bool("reader", false, "")
	sprintBin := fs.String("sprint", "nova-sprint", "")
	harness := fs.String("harness", "", "")
	model := fs.String("model", "", "")
	root := fs.String("root", "", "")
	slots := fs.String("slots", "", "")
	resultsRoot := fs.String("results-root", "", "")
	deadline := newSecondsFlag(fs, "deadline", 0)
	tokensWord := fs.String("tokens", "", "")
	every := newSecondsFlag(fs, "every", 3*time.Second)
	once := fs.Bool("once", false, "")
	ticks := fs.Int("ticks", 0, "")
	auth := fs.String("auth", "", "")
	config := fs.String("config", "", "")
	workerFile := fs.String("worker", "", "")
	noWall := fs.Bool("no-wall", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*as, "as", "the member's name in the fleet table (a reader's in the readers table with --reader)")
	f.wantCount(*width, "width", "the most cards this machine runs at once")
	f.want(*harness, "harness", "the harness binary path a card runs under (nova-swarm native --harness)")
	f.want(*model, "model", "provider/model")
	if *model != "" {
		if _, ok := providerOf(*model); !ok {
			f.add(fmt.Sprintf("--model %q is not provider/model (one slash, both sides nonempty); every card would be refused by native", *model))
		}
	}
	f.want(*root, "root", "the root directory slots and results sit under")
	f.tokens(*tokensWord) // the word is read here, once, so a typo is one refusal and not one failed card each
	if deadline.d <= 0 {
		f.add("--deadline is required; it wants the wall bound per card; refusing to guess")
	}
	// The store is nova-sprint's to know: the member passes its environment
	// through (NOVA_SPRINT_REDIS, or a seat) and names no address itself.
	if *as != "" && !safepath.NameOK(*as) {
		f.add(fmt.Sprintf("--as %q is not a name (letters, digits, - _ .)", *as))
	}
	if f.refused(stderr) {
		return 2
	}
	if *slots == "" {
		*slots = filepath.Join(*root, "slots")
	}
	if *resultsRoot == "" {
		*resultsRoot = filepath.Join(*root, "results")
	}
	for _, d := range []string{*slots, *resultsRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refuse(stderr, " member", err.Error())
		}
	}
	self, err := os.Executable()
	if err != nil {
		return refuse(stderr, " member", "own executable: "+err.Error())
	}
	sp := &execSprint{bin: *sprintBin, actor: *as}
	rn := &nativeRunner{
		self: self, sprintBin: *sprintBin, harness: *harness, model: *model, root: *root, slots: *slots,
		resultsRoot: *resultsRoot, deadline: deadline.d, tokens: *tokensWord, auth: *auth, config: *config,
		worker: *workerFile, noWall: *noWall, stderr: stderr,
	}
	m := member.New(member.Config{As: *as, Width: *width, Reader: *reader}, sp, rn, stdout)
	kind := "member"
	if *reader {
		kind = "reader"
	}
	fmt.Fprintf(stdout, "MEMBER %s as=%s width=%d every=%s sprint=%s harness=%s model=%s\n", kind, *as, *width, every.d, *sprintBin, *harness, *model)
	n := 0
	for {
		n++
		acted, err := m.Tick(time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm member: tick %d: %s\n", n, oneline.Escape(err.Error()))
		}
		if acted > 0 || err != nil {
			fmt.Fprintf(stdout, "tick %d acted=%d running=%d %s\n", n, acted, m.Running(), time.Now().Format("15:04:05"))
		}
		if *once || (*ticks > 0 && n >= *ticks) {
			break
		}
		time.Sleep(every.d)
	}
	fmt.Fprintf(stdout, "MEMBER OK as=%s ticks=%d running=%d\n", *as, n, m.Running())
	return 0
}

// execSprint runs the sprint's verbs as the nova-sprint binary, with this
// process's environment (the store address is nova-sprint's own flag or
// environment, never the member's).
type execSprint struct {
	bin, actor string
}

func (s *execSprint) Run(args ...string) (int, []byte) {
	cmd := exec.Command(s.bin, args...)
	cmd.Env = append(os.Environ(), "NOVA_SPRINT_ACTOR="+s.actor)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return 2, []byte(err.Error())
	}
	if code != 0 && out.Len() == 0 {
		return code, errb.Bytes()
	}
	return code, out.Bytes()
}

// nativeRunner runs one packet as one `nova-swarm native` child in its own
// slot directory, its results under <results-root>/<card>/.
type nativeRunner struct {
	self, sprintBin, harness, model, root, slots, resultsRoot string
	deadline                                                  time.Duration
	tokens, auth, config, worker                              string
	noWall                                                    bool
	stderr                                                    io.Writer
}

func (r *nativeRunner) Start(p member.Packet) (member.Child, error) {
	if !safepath.NameOK(p.Card) {
		return nil, fmt.Errorf("card %q is not a name", p.Card)
	}
	slot := filepath.Join(r.slots, p.Card)
	if err := safepath.RemoveUnder(r.slots, slot); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return nil, err
	}
	cardPath := filepath.Join(r.slots, p.Card+".card.md")
	if err := os.WriteFile(cardPath, []byte(member.CardText(p, r.sprintBin)), 0o644); err != nil {
		return nil, err
	}
	args := []string{"native", "--harness", r.harness, "--model", r.model, "--card", cardPath, "--slot", slot,
		"--root", r.root, "--deadline", r.deadline.String(), "--tokens", r.tokens, "--label", p.Card, "--results-root", r.resultsRoot}
	if r.auth != "" {
		args = append(args, "--auth", r.auth)
	}
	if r.config != "" {
		args = append(args, "--config", r.config)
	}
	if r.worker != "" {
		args = append(args, "--worker", r.worker)
	}
	if r.noWall {
		args = append(args, "--no-wall")
	}
	cmd := exec.Command(r.self, args...)
	logPath := filepath.Join(r.slots, p.Card+".native.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	c := &nativeChild{card: p.Card, logPath: logPath, results: filepath.Join(r.resultsRoot, p.Card), done: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		logf.Close()
		close(c.done)
	}()
	return c, nil
}

type nativeChild struct {
	card, logPath, results string
	done                   chan struct{}
	err                    error
	once                   sync.Once
	result                 member.Result
}

func (c *nativeChild) Done() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

var nativeRC = regexp.MustCompile(`\bNATIVE (\S+) .*\brc=(-?\d+)\b.*\bharness=(\S+)`)

// Result reads how the child ended: the NATIVE line's rc and harness word,
// and the newest RESULT.md under the card's results (its `rev:` line is the
// head, its "One line" section the report).
func (c *nativeChild) Result() member.Result {
	c.once.Do(func() {
		ok := false
		if b, err := os.ReadFile(c.logPath); err == nil {
			if m := nativeRC.FindSubmatch(b); m != nil {
				ok = string(m[1]) == "OK" && string(m[2]) == "0" && string(m[3]) == "ok"
			}
		}
		head, report := readResult(newestResult(c.results))
		if report == "" {
			if ok {
				report = "finished; the child published no one-line report"
			} else {
				report = "the child ended without a result (see " + c.logPath + ")"
			}
		}
		c.result = member.Result{OK: ok, Head: head, Report: report}
	})
	return c.result
}

// newestResult is the newest RESULT.md under dir, "" when none.
func newestResult(dir string) string {
	var best string
	var bestT time.Time
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "RESULT.md" && (best == "" || info.ModTime().After(bestT)) {
			best, bestT = path, info.ModTime()
		}
		return nil
	})
	return best
}

// readResult reads a RESULT.md: the head from a `rev: <sha>` line, the report
// from the "## One line" section (else the first paragraph of the head).
func readResult(path string) (head, report string) {
	if path == "" {
		return "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	inOne := false
	var first strings.Builder
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "rev:") && head == "" {
			head = strings.TrimSpace(strings.TrimPrefix(l, "rev:"))
		}
		if strings.HasPrefix(l, "## ") {
			inOne = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(l, "## ")), "one line")
			continue
		}
		if inOne && l != "" && report == "" {
			report = l
		}
		if !strings.HasPrefix(l, "#") && l != "" && !strings.Contains(l, ":") && first.Len() == 0 {
			first.WriteString(l)
		}
	}
	if report == "" {
		report = first.String()
	}
	if strings.ContainsAny(head, " \t") || len(head) > 64 {
		head = ""
	}
	return head, report
}

var _ = strconv.Itoa
