package update

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// AdoptHeader is the checks file watch --adopt reads: one adoption check per
// line, the command held to the same argv rule as every other exec site.
const AdoptHeader = "check\tcommand\towner"

// AdoptCheck is one row of the checks file.
type AdoptCheck struct {
	Name, Owner string
	Command     []string
}

// LoadAdopt reads the checks file watch --adopt names. The first line must
// equal the header byte for byte and every other line is one tab-separated
// check; a `#` first character opens a comment.
func LoadAdopt(r io.Reader) ([]AdoptCheck, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != AdoptHeader {
		return nil, fmt.Errorf("line 1: invalid header (put the header back exactly: %s)", tabbed(AdoptHeader))
	}
	var out []AdoptCheck
	seen := map[string]bool{}
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: %d fields, want 3 (use the three-column checks header)", line, len(f))
		}
		for _, v := range f {
			if v == "" {
				return nil, fmt.Errorf("line %d: empty field (supply check, command and owner)", line)
			}
		}
		if seen[f[0]] {
			return nil, fmt.Errorf("line %d: duplicate check %s (give each check a distinct name)", line, f[0])
		}
		seen[f[0]] = true
		args, err := argv(f[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, AdoptCheck{Name: f[0], Owner: f[2], Command: args})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: unreadable checks file (use lines below 1 MiB)", line)
	}
	return out, nil
}

type adoptResult struct {
	check        AdoptCheck
	ok           bool
	detail       string
	reason       string
	remedy       string
	observedLine string
}

func runAdoptChecks(ctx context.Context, checks []AdoptCheck, timeout time.Duration) []adoptResult {
	rs := make([]adoptResult, len(checks))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Go(func() {
			for i := range jobs {
				c := checks[i]
				if ctx.Err() != nil {
					rs[i] = adoptResult{check: c, reason: "budget", remedy: "increase --budget", observedLine: "budget"}
					continue
				}
				child, cancel := context.WithTimeout(ctx, timeout)
				p := process(child, c.Command, nil, ChildCap)
				cancel()
				if p.Reason != "" {
					remedy := "repair the check command"
					if p.Reason == "not_found" {
						remedy = "install " + c.Command[0] + " or supply its executable path"
					}
					if p.Reason == "timeout" {
						remedy = "increase --timeout or repair the check command"
					}
					if ctx.Err() != nil {
						rs[i] = adoptResult{check: c, reason: "budget", remedy: "increase --budget", observedLine: "budget"}
						continue
					}
					rs[i] = adoptResult{check: c, reason: p.Reason, remedy: remedy, observedLine: p.Reason}
					continue
				}
				raw := p.Stdout
				if raw == "" {
					raw = p.Stderr
				}
				rs[i] = adoptResult{check: c, ok: true, detail: firstLine(raw), observedLine: firstLine(raw)}
			}
		})
	}
	for i := range checks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return rs
}

// watchAdopt runs the coordinator's own adoption pass and, when a bus is
// named, posts the receipt as the coordinator's own. Every REFUSED check is
// handed to the duty tier on an ESCALATE line naming its owner.
func watchAdopt(ctx context.Context, checks []AdoptCheck, o options, started time.Time, out, errs io.Writer, env Environment) int {
	rs := runAdoptChecks(ctx, checks, o.timeout)
	var lines []string
	ok, refused := 0, 0
	var okBuf, refuseBuf bytes.Buffer
	for _, r := range rs {
		if r.ok {
			ok++
			l := fmt.Sprintf("ADOPT OK check=%s detail=%s", field(r.check.Name), field(r.detail))
			lines = append(lines, l)
			fmt.Fprintln(&okBuf, l)
		} else {
			refused++
			l := fmt.Sprintf("ADOPT REFUSED check=%s detail=%s (%s)", field(r.check.Name), oneline.Escape(r.reason), oneline.Escape(r.remedy))
			lines = append(lines, l)
			fmt.Fprintln(&refuseBuf, l)
			fmt.Fprintf(&refuseBuf, "ADOPT ESCALATE check=%s to=%s: duty files an issue and a fix card (%s)\n", field(r.check.Name), field(r.check.Owner), oneline.Escape(r.reason))
			lines = append(lines, fmt.Sprintf("ADOPT ESCALATE check=%s to=%s", r.check.Name, r.check.Owner))
		}
	}
	sort.Strings(lines)
	sha := shaText(strings.Join(lines, "\n"))[:12]
	done := fmt.Sprintf("ADOPT DONE sha=%s ok=%d refused=%d", sha, ok, refused)
	if _, err := okBuf.WriteTo(out); err != nil {
		return 1
	}
	if refuseBuf.Len() > 0 {
		if _, err := out.Write([]byte{}); err != nil {
			return 1
		}
		fmt.Fprint(errs, refuseBuf.String())
	}
	w := out
	if refused > 0 {
		w = errs
	}
	fmt.Fprintln(w, done)
	code := 0
	if refused > 0 {
		code = 1
	}
	if o.bus != "" {
		var body bytes.Buffer
		fmt.Fprintf(&body, "From: %s\nTo: %s\nSubject: adoption on %s at %s\n\n", o.as, o.to, dash(o.host), started.UTC().Format("2006-01-02T15:04:05Z"))
		body.Write(okBuf.Bytes())
		body.Write(refuseBuf.Bytes())
		fmt.Fprintln(&body, done)
		line, serr := postAdoptReceipt(ctx, o, body.Bytes(), env)
		if serr != nil {
			fmt.Fprintf(errs, "ADOPT NOTE %s\n", oneline.Err(serr))
			return 1
		}
		fmt.Fprintf(out, "ADOPT SENT to=%s line=%s\n", field(o.to), field(line))
	}
	return code
}

// postAdoptReceipt publishes the adoption receipt through the prepared
// artifact protocol, the same validation-before-send the reporter uses.
func postAdoptReceipt(ctx context.Context, o options, body []byte, env Environment) (string, error) {
	allowance := deliveryAllowance(ctx, env.Now())
	if allowance <= 0 {
		return "", fmt.Errorf("delivery budget exhausted (retry watch with the same --adopt)")
	}
	child, cancel := context.WithTimeout(ctx, allowance)
	prepared := captureRun(child, []string{"nova-bus", "prepare", "--bus", o.bus, "--as", o.as, "--stdin"}, body, ChildCap)
	cancel()
	if prepared.Reason != "" {
		return "", fmt.Errorf("prepare refused: %s; the bus said: %s (check nova-bus and the named bus; retry watch)", prepared.Reason, busSaid(prepared))
	}
	id, err := validatePrepared([]byte(prepared.Stdout))
	if err != nil {
		return "", fmt.Errorf("%s (use a compatible nova-bus)", err)
	}
	var artifact map[string]string
	_ = artifact
	allowance = deliveryAllowance(ctx, env.Now())
	if allowance <= 0 {
		return "", fmt.Errorf("pending %s not sent: the budget is spent (retry watch with the same --adopt)", id)
	}
	attempts, gitSeconds := busBounds(allowance)
	args := []string{"nova-bus", "send", "--prepared-stdin", "--bus", o.bus, "--remote", o.remote, "--branch", o.branch, "--as", o.as,
		"--attempts", strconv.Itoa(attempts), "--git-timeout", strconv.Itoa(gitSeconds)}
	child2, cancel2 := context.WithTimeout(ctx, allowance)
	r := captureRun(child2, args, []byte(prepared.Stdout), ChildCap)
	cancel2()
	for _, l := range strings.Split(r.Stdout, "\n") {
		if confirmed(l, id) {
			return l, nil
		}
	}
	return "", fmt.Errorf("pending %s not confirmed: %s; the bus said: %s (retry watch with the same --adopt)", id, dash(r.Reason), busSaid(r))
}

func loadAdoptFile(path string) ([]AdoptCheck, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadAdopt(f)
}

// watchMain parses watch flags and runs the adoption pass. The pass is the
// mechanical step of the upgrade cycle: after every rebuild the coordinator
// runs its own checks, posts the receipt as its own, and escalates refusals.
func watchMain(name string, args []string, out, errs io.Writer, env Environment) int {
	if env.Now == nil {
		env.Now = time.Now
	}
	o := options{timeout: 5 * time.Second, budget: 60 * time.Second}
	f := flag.NewFlagSet("watch", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.adopt, "adopt", "", "the checks file (required): a header line check<TAB>command<TAB>owner, then one check per line, its command run as written")
	f.StringVar(&o.bus, "bus", "", "the bus checkout that delivers the receipt; with it, --remote, --branch, --as and --to are required")
	f.StringVar(&o.remote, "remote", "", "the bus remote")
	f.StringVar(&o.branch, "branch", "", "the bus branch")
	f.StringVar(&o.as, "as", "", "the sender the receipt is from")
	f.StringVar(&o.to, "to", "", "the receipt's recipients, comma-separated (those who answer a refused check)")
	f.StringVar(&o.host, "host", "", "a label for the machine the pass ran on, carried in the receipt's subject")
	f.DurationVar(&o.timeout, "timeout", o.timeout, "one check's deadline, such as 5s")
	f.DurationVar(&o.budget, "budget", o.budget, "the whole pass's deadline, such as 60s")
	help := name + " watch -h"
	if err := verbflag.Parse(f, interspersed(f, args)); err != nil {
		return refusal(errs, "ADOPT", help, flagProblem(f, err))
	}
	// Every problem of the invocation in one refusal (STANDARD §2).
	var problems, missing []string
	if o.adopt == "" {
		missing = append(missing, "--adopt")
	}
	// The bus flags go together: one names a delivery, and a delivery needs all five.
	if o.bus != "" || o.remote != "" || o.branch != "" || o.as != "" || o.to != "" {
		for _, x := range []struct{ n, v string }{{"bus", o.bus}, {"remote", o.remote}, {"branch", o.branch}, {"as", o.as}, {"to", o.to}} {
			if x.v == "" {
				missing = append(missing, "--"+x.n)
			}
		}
		if strings.ContainsAny(o.as+o.to+o.host, "\r\n") {
			problems = append(problems, "note header contains a newline (use a single-line --as, --to and --host)")
		}
	}
	if len(missing) > 0 {
		problems = append([]string{"missing " + strings.Join(missing, ", ") + "; refusing to guess"}, problems...)
	}
	if len(f.Args()) != 0 {
		problems = append(problems, fmt.Sprintf("watch takes no positional arguments, got %q", f.Arg(0)))
	}
	if o.timeout <= 0 || o.budget <= 0 {
		problems = append(problems, "--timeout and --budget want positive durations")
	}
	if len(problems) > 0 {
		return refusal(errs, "ADOPT", help, errors.New(strings.Join(problems, "; ")))
	}
	checks, err := loadAdoptFile(o.adopt)
	if err != nil {
		return refusal(errs, "ADOPT", help, fmt.Errorf("cannot adopt %s (supply a readable --adopt checks file: %v)", o.adopt, err))
	}
	started := env.Now()
	ctx, cancel := context.WithTimeout(context.Background(), o.budget)
	defer cancel()
	return watchAdopt(ctx, checks, o, started, out, errs, env)
}
