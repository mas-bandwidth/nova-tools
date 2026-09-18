package fleet

// CERTIFICATION: a machine is not what its inventory says, it is what it can DO.
//
// Glenn, 2026-09-18: "certify fleet machines". The hurt behind it, the same morning: the
// first real Go card of the day was launched on hulk and died inside the swarm wall. The
// wall's readable roots did not include `$HOME/sdk/go1.26.5`, so the only Go the card could
// reach was `/usr/bin/go` 1.22, which go.mod refuses by name. hulk had been surveyed, it met
// the provisioning standard, and every check that had ever been run on it passed -- because
// every check that had ever been run on it ran OUTSIDE the wall, over a plain ssh, as the
// person who owns the machine. Nothing had ever made the bench do a card's work the way a
// card does it.
//
// `fleet survey` asks a machine what it HAS. Certification makes the machine DO a
// representative piece of the work its roles imply, under the same containment a card gets,
// and writes down that it did: one certificate row per machine per workload class, tied to
// the build that was installed and to the hash of the standard it was held against. When
// either moves, every certificate written under the old pair stops being current, because a
// certificate for a standard the machine no longer meets is worse than no certificate --
// it is the same lie the survey told about hulk.
//
// THE POINT OF THE WALL. A workload marked `wall: yes` is not run by ssh and a shell; it is
// wrapped in `nova-sandbox` -- the same wall `nova-swarm native` puts a card behind -- with
// a writable job directory, a HOME inside it, and every root the work needs named as a
// `--read`. That is exactly the shape that failed on hulk, so a bench that cannot build and
// test a two-file Go module inside the wall now says so BEFORE a card is spent finding out.
//
// Everything that reaches a machine or the forge is an interface. The production ones are
// one ssh per workload and one `gh api` for the runner list; a test drives fakes and neither
// starts a program nor opens a socket.

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultGo is the toolchain a bench's Go workloads ask for. It tracks go.mod's `go` line;
// when go.mod moves, this moves with it and every certificate written under the old
// workloads expires on its own, because the workload bytes are in the standard hash.
const DefaultGo = "go1.26.5"

// DefaultCertifyTimeout bounds ONE workload on ONE machine. A build inside a cold wall on a
// small bench is minutes, not seconds; the whole run is bounded per workload rather than
// once, so one slow machine cannot eat the fleet's budget.
const DefaultCertifyTimeout = 10 * time.Minute

// DefaultMaxAge is how long a certificate stands before it is stale even though nothing
// moved. Twenty-four hours: a machine drifts by the hand of whoever last logged into it, and
// the whole fleet was found drifted on the morning of 2026-09-18 with nothing in the tools
// having changed at all.
const DefaultMaxAge = 24 * time.Hour

// EvidenceCap is the most of a machine's answer that reaches a line or a row. A workload
// that writes a screenful must not be able to fill the certificates file.
const EvidenceCap = 240

// The verdicts a certificate may carry. They are tokens, not prose.
//
// WARN is a NOTE ON A PASS, not a failure: a workload marked `report: yes` measures
// something worth watching that has never stopped a card -- the runners' `_diag` logs were
// 15.7 GB across the fleet on 2026-09-18 and nothing was broken by it. A WARN is written,
// counted and printed, and it neither fails the run nor withholds the certificate, because
// a check that cries wolf is a check people learn to pass over.
//
// UNREACHABLE is NOT a verdict about a machine: it is this tool saying it could not ask.
// It exists because the first real run of this verb, by somebody who did not write it,
// printed `CERTIFY hulk go-test FAIL evidence="Host key verification failed."` and wrote a
// row whose build column held `Host\x20key\x20verification\x20failed.` -- a lie about a
// bench's toolchain, recorded, and then read by `fill`. A transport failure writes no row,
// is counted apart, and never reaches the repair round.
const (
	VerdictOK          = "OK"
	VerdictFail        = "FAIL"
	VerdictWarn        = "WARN"
	VerdictUnreachable = "UNREACHABLE"
	// TIMEOUT is the second thing that is not a verdict: the work did not FINISH inside the
	// bound this run gave it. `exec sleep 5` under a one-millisecond --timeout was reported
	// FAIL, which is a judgement on work nobody let run. Like UNREACHABLE it writes no row,
	// is never repaired, and is counted apart.
	VerdictTimeout = "TIMEOUT"
)

// Where one workload runs. `gh` lives where the coordinator is and not on a bench, so the
// forge questions -- "are this machine's runners online", "does the registry tell the truth
// about it" -- are asked HERE about the machine. Asking the machine is how the first real
// run of this verb went looking for `gh` on a bench that has never had it.
const (
	WhereMachine     = "machine"
	WhereCoordinator = "coordinator"
)

// The two workloads that are not questions for a machine at all. Whether the forge says a
// runner host's runners are online, and whether the registry's roles match what the forge is
// actually running, are questions for the forge and the registry; asking the machine would
// only tell us what the machine believes. On 2026-09-18 space was serving sixteen
// merge-group runners while machines.tsv said it was `bench,services` -- the machine knew,
// the forge knew, and the file that decides where cards go did not.
const (
	ForgeRunners  = "runners"
	ForgeRegistry = "registry"
)

// ---------------------------------------------------------------------------
// the seams
// ---------------------------------------------------------------------------

// Remote runs one script on one machine and answers with its combined output. The
// production one is `ssh <target> bash -s` with the script on stdin -- never a command line
// pasted together, because the script carries heredocs and quoting nobody could check.
type Remote interface {
	Run(ctx context.Context, target, script string) (string, error)
}

// RunnerStatus is one self-hosted runner as the forge reports it.
type RunnerStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // online, offline
}

// Forge answers the runner list for one repository.
type Forge interface {
	Runners(repo string) ([]RunnerStatus, error)
}

// ---------------------------------------------------------------------------
// workloads
// ---------------------------------------------------------------------------

//go:embed workloads/*.card
var standardWorkloads embed.FS

// Workload is one representative piece of work: what roles it applies to, what a pass looks
// like, whether it runs inside the wall, and the body the machine runs.
type Workload struct {
	Class  string         // the name on every line and every row; the file's name
	Roles  []string       // the roles this workload applies to, sorted
	Expect *regexp.Regexp // a pass is this matching the machine's output
	Wall   bool           // true runs the body inside nova-sandbox
	Reads  []string       // the wall's readable roots; `$HOME` is the machine's own
	Forge  string         // ForgeRunners or ForgeRegistry, or "" for a workload the machine runs
	Where  string         // WhereMachine (the default) or WhereCoordinator
	Report bool           // true makes a failure a WARN: measured and never a refusal
	Body   string         // the command run ON the machine
	Source string         // where this workload was read from, for a refusal that can be found
	raw    []byte         // the file's bytes, which are half of the standard hash
}

// AppliesTo says whether this workload is run on a machine carrying one role.
func (w Workload) AppliesTo(role string) bool {
	for _, r := range w.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// StandardWorkloads is the set that ships with the tool: the one a run takes when
// `--workloads` names no directory.
func StandardWorkloads() ([]Workload, error) {
	entries, err := fs.ReadDir(standardWorkloads, "workloads")
	if err != nil {
		return nil, err
	}
	var out []Workload
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		raw, err := standardWorkloads.ReadFile(path.Join("workloads", e.Name()))
		if err != nil {
			return nil, err
		}
		w, err := ParseWorkload("workloads/"+e.Name(), raw)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	sortWorkloads(out)
	return out, nil
}

// ReadWorkloads reads an override directory: every `<class>.card` in it, whole and
// validated. A directory holding one broken card is refused entire, for the registry's own
// reason -- the half that reads is the half that lets a machine through.
func ReadWorkloads(dir string) ([]Workload, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read the workloads directory %s: %w", dir, err)
	}
	var out []Workload
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		w, err := ParseWorkload(p, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no <class>.card; refusing to guess (a workload is a file: roles, expect, then a blank line, then the body)", dir)
	}
	sortWorkloads(out)
	return out, nil
}

func sortWorkloads(out []Workload) {
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
}

// ParseWorkload reads one card: front matter of `key: value` lines, a blank line, then the
// body. The CLASS is the file's name and never a key, so two cards cannot claim one class
// and a person looking for a class knows which file to open.
func ParseWorkload(source string, raw []byte) (Workload, error) {
	class := strings.TrimSuffix(path.Base(filepath.ToSlash(source)), ".card")
	w := Workload{Class: class, Source: source, raw: append([]byte(nil), raw...)}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	head, body, cut := strings.Cut(text, "\n\n")
	if !cut {
		return Workload{}, fmt.Errorf("%s: front matter and body are separated by ONE blank line; this file has none", source)
	}
	w.Body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(w.Body) == "" {
		return Workload{}, fmt.Errorf("%s: carries no body; the body is the command run ON the machine", source)
	}
	seen := map[string]bool{}
	for n, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Workload{}, fmt.Errorf("%s line %d: front matter is `key: value`, got %q", source, n+1, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if seen[key] {
			return Workload{}, fmt.Errorf("%s line %d: names %s twice", source, n+1, key)
		}
		seen[key] = true
		switch key {
		case "roles":
			roles, err := readRoles(value)
			if err != nil {
				return Workload{}, fmt.Errorf("%s line %d: %w", source, n+1, err)
			}
			w.Roles = roles
		case "expect":
			// (?m) so `^` is the start of a LINE: a machine answers in lines and the
			// evidence worth keeping is the line that matched.
			re, err := regexp.Compile("(?m)" + value)
			if err != nil {
				return Workload{}, fmt.Errorf("%s line %d: expect is not a regexp: %w", source, n+1, err)
			}
			w.Expect = re
		case "wall":
			switch value {
			case "yes", "true":
				w.Wall = true
			case "no", "false", "":
			default:
				return Workload{}, fmt.Errorf("%s line %d: wall is yes or no, got %q", source, n+1, value)
			}
		case "reads":
			for _, part := range strings.Split(value, ",") {
				if t := strings.TrimSpace(part); t != "" {
					w.Reads = append(w.Reads, t)
				}
			}
		case "forge":
			if value != ForgeRunners && value != ForgeRegistry {
				return Workload{}, fmt.Errorf("%s line %d: the forge questions are %s and %s, got %q", source, n+1, ForgeRunners, ForgeRegistry, value)
			}
			w.Forge = value
		case "where":
			if value != WhereMachine && value != WhereCoordinator {
				return Workload{}, fmt.Errorf("%s line %d: where is %s or %s, got %q", source, n+1, WhereMachine, WhereCoordinator, value)
			}
			w.Where = value
		case "report":
			switch value {
			case "yes", "true":
				w.Report = true
			case "no", "false", "":
			default:
				return Workload{}, fmt.Errorf("%s line %d: report is yes or no, got %q", source, n+1, value)
			}
		default:
			return Workload{}, fmt.Errorf("%s line %d: unknown key %q (the keys are roles, expect, wall, reads, forge, report)", source, n+1, key)
		}
	}
	if len(w.Roles) == 0 {
		return Workload{}, fmt.Errorf("%s: names no roles; a workload that applies to nothing is never run (roles: bench|runner|services|coordination)", source)
	}
	if w.Expect == nil {
		return Workload{}, fmt.Errorf("%s: carries no expect; a workload that cannot say what a pass looks like certifies nothing", source)
	}
	if w.Wall && len(w.Reads) == 0 {
		return Workload{}, fmt.Errorf("%s: runs inside the wall and names no reads; a toolchain outside the wall is the failure this exists to catch", source)
	}
	// A forge question is a coordinator question, always: `gh` is where the coordinator is.
	// Saying otherwise out loud is a refusal rather than a quiet correction, because a card
	// that names the wrong `where` means somebody believes something false about the fleet.
	if w.Forge != "" {
		if w.Where == WhereMachine {
			return Workload{}, fmt.Errorf("%s: asks the forge and says where: %s; the forge is asked where the coordinator is, and a bench has no gh", source, WhereMachine)
		}
		w.Where = WhereCoordinator
	}
	if w.Where == "" {
		w.Where = WhereMachine
	}
	if w.Where == WhereCoordinator && w.Wall {
		return Workload{}, fmt.Errorf("%s: runs on the coordinator and asks for the wall; the wall is the containment a CARD gets on a bench", source)
	}
	return w, nil
}

// StandardHash is what makes a certificate expire: the sha256 over the provisioning
// standard file and over every workload's bytes, in class order. Either half moving is a
// new hash and so a fleet with no current certificates, which is the honest state.
func StandardHash(standard string, loads []Workload) (string, error) {
	h := sha256.New()
	raw, err := os.ReadFile(standard)
	if err != nil {
		return "", fmt.Errorf("cannot read the provisioning standard %s: %w", standard, err)
	}
	fmt.Fprintf(h, "standard %d\n", len(raw))
	h.Write(raw)
	ordered := append([]Workload(nil), loads...)
	sortWorkloads(ordered)
	for _, w := range ordered {
		body := w.raw
		if len(body) == 0 {
			body = []byte(w.Body)
		}
		fmt.Fprintf(h, "workload %s %d\n", w.Class, len(body))
		h.Write(body)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16], nil
}

// ---------------------------------------------------------------------------
// certificates
// ---------------------------------------------------------------------------

// Certificate is one row: what machine did what work, under what build and what standard,
// with what it said, and when.
type Certificate struct {
	Machine  string
	Build    string
	Hash     string
	Class    string
	Verdict  string
	Evidence string
	At       time.Time
}

// certificateFields is the shape of one row, and it is FIXED: every field is written every
// time, `-` for one nobody could fill, so a reader sees a column was answered and not
// forgotten (Glenn: fixed tables, no elision).
const certificateFields = 7

// Row renders one certificate as the tab-separated line the file holds. Evidence goes
// through oneline.Escape, so a tab or a newline in what a machine said cannot become a
// column or a row.
func (c Certificate) Row() string {
	return strings.Join([]string{
		dash(c.Machine), dash(c.Build), dash(c.Hash), dash(c.Class), dash(c.Verdict),
		dash(oneline.Escape(oneline.Cap(c.Evidence, EvidenceCap))),
		c.At.UTC().Format(time.RFC3339),
	}, "\t")
}

// AppendCertificate adds one row. Certify appends and never rewrites: the file is the
// record of what was run, and a machine that failed and was repaired is current on its
// newest row while the failure it had stays readable.
func AppendCertificate(path string, c Certificate) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot write the certificates file %s: %w", path, err)
	}
	defer f.Close()
	_, err = io.WriteString(f, c.Row()+"\n")
	return err
}

// ReadCertificates reads the file whole. A file nobody has written yet is no certificates,
// which is not an error: the first run of certify creates it.
func ReadCertificates(path string) ([]Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read the certificates file %s: %w", path, err)
	}
	var out []Certificate
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) != certificateFields {
			return nil, fmt.Errorf("%s line %d: wants %d tab-separated fields machine, build, standard-hash, class, verdict, evidence, at; got %d",
				path, i+1, certificateFields, len(f))
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(f[6]))
		if err != nil {
			return nil, fmt.Errorf("%s line %d: `at` wants RFC3339, got %q", path, i+1, strings.TrimSpace(f[6]))
		}
		out = append(out, Certificate{
			Machine: strings.TrimSpace(f[0]), Build: strings.TrimSpace(f[1]),
			Hash: strings.TrimSpace(f[2]), Class: strings.TrimSpace(f[3]),
			Verdict: strings.TrimSpace(f[4]), Evidence: undash(f[5]), At: at,
		})
	}
	return out, nil
}

// Certified is the whole currency rule, and it is the one pulse.Fill asks before a card
// reaches a machine: the named machine has a row for this class whose verdict is OK, whose
// build is the build installed there NOW, and whose standard hash is the standard NOW. The
// newest matching row wins, so a bench that failed and was repaired is certified and a
// bench that passed and then drifted is not.
func Certified(certs []Certificate, machine, class, build, hash string) bool {
	var newest *Certificate
	for i := range certs {
		c := certs[i]
		if c.Machine != machine || c.Class != class {
			continue
		}
		if c.Build != build || c.Hash != hash {
			continue
		}
		if newest == nil || !c.At.Before(newest.At) {
			newest = &certs[i]
		}
	}
	return newest != nil && (newest.Verdict == VerdictOK || newest.Verdict == VerdictWarn)
}

// ---------------------------------------------------------------------------
// the run
// ---------------------------------------------------------------------------

// CertifyInput is the verb apart from flag parsing, so a test drives a whole run against
// fakes and the release verb drives the same engine through its own ssh seam.
type CertifyInput struct {
	Machines  string     // the machines registry
	Only      string     // one machine name; empty with All false is a refusal
	All       bool       // every machine in the registry, under its own roles
	Workloads []Workload // the set to run; nil takes StandardWorkloads
	Certs     string     // the certificates file appended to
	Attempts  string     // the per-attempt trail; "" is attempts.log beside Certs
	Hash      string     // the standard hash every row carries
	Build     string     // the build every row carries; empty asks each machine its own
	Bin       string     // the install directory the release puts the tools in; "" is $HOME/.local/bin
	Repo      string     // the repository the forge is asked about
	IfStale   bool       // true skips a machine whose every class is current
	MaxAge    time.Duration
	Log       io.Writer // the structured event stream; nil writes none
	Timeout   time.Duration
	DryRun    bool
	// Fix is the repair round: a FAIL whose class maps to an item of the provisioning
	// standard is applied and certified ONCE more. The CLI has it on by default and
	// `--no-fix` waives it out loud; the zero value here is off, so a caller that wants a
	// verb that only reads gets one by saying nothing.
	Fix          bool
	MaxFixRounds int       // how many repair-and-recertify rounds; 0 is DefaultFixRounds
	Fixer        Fixer     // the apply seam; nil with Fix on is an escalation, never a pass
	Bus          BusPoster // where an escalation's note goes; nil sends none and says so
	Lane         string    // the lane that note goes to; "" is DefaultLane
	Remote       Remote
	// Local runs a workload HERE, with no ssh, when the machine being certified is the
	// machine running this. hulk certifying hulk went through `ssh hulk` on its first real
	// run and died on its own host key: a machine cannot be asked to prove itself through a
	// transport it does not need.
	Local     Remote
	LocalHost string // this machine's hostname; "" never matches anything
	Forge     Forge
	Now       func() time.Time
	Stdout    io.Writer
	Stderr    io.Writer
}

// Certify runs every workload of every named machine's roles, writes one certificate row
// each, and answers 0 when all passed, 1 when any failed, 2 when it refused to start.
func Certify(in CertifyInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Timeout <= 0 {
		in.Timeout = DefaultCertifyTimeout
	}
	if in.MaxAge <= 0 {
		in.MaxAge = DefaultMaxAge
	}
	if strings.TrimSpace(in.Only) == "" && !in.All {
		return certifyRefusal(in.Stderr, fmt.Errorf(
			"neither --machine nor --all; refusing to guess which machines to certify (certification puts real load on a machine, so it is never the whole fleet by accident)"))
	}
	if strings.TrimSpace(in.Only) != "" && in.All {
		return certifyRefusal(in.Stderr, fmt.Errorf("--machine and --all together; pass one"))
	}
	if strings.TrimSpace(in.Certs) == "" && !in.DryRun {
		return certifyRefusal(in.Stderr, fmt.Errorf("missing --certs; refusing to guess (the certificates file is the record a fill reads before it launches a card)"))
	}
	if in.Remote == nil && !in.DryRun {
		return certifyRefusal(in.Stderr, fmt.Errorf("missing a remote; refusing to guess (inject a fleet.Remote)"))
	}
	reg, err := ReadRegistry(in.Machines)
	if err != nil {
		return certifyRefusal(in.Stderr, err)
	}
	if in.Workloads == nil {
		loads, err := StandardWorkloads()
		if err != nil {
			return certifyRefusal(in.Stderr, err)
		}
		in.Workloads = loads
	}

	var machines []Machine
	if in.All {
		machines = reg.Machines()
	} else {
		m, ok := reg.Lookup(in.Only)
		if !ok {
			// The registry's own refusal shape, under this verb's token: named, with the
			// reason as a token and the remedy as it would be typed, and BEFORE any ssh.
			r := &Refusal{
				Name:   strings.TrimSpace(in.Only),
				Reason: ReasonUnknown,
				Remedy: fmt.Sprintf("%s does not carry %s; add it, or name a machine it does",
					reg.Path(), dash(strings.TrimSpace(in.Only))),
			}
			fmt.Fprintln(in.Stderr, r.Line("CERTIFY"))
			return 2
		}
		machines = []Machine{m}
	}

	// --if-stale reads the record once. THE LOOP RUNS THIS EVERY SIX HOURS, so the common
	// case is a fleet with nothing to do, and the common case must cost one file read.
	var held []Certificate
	if in.IfStale {
		held, err = ReadCertificates(in.Certs)
		if err != nil {
			return certifyRefusal(in.Stderr, err)
		}
	}

	ok, fail, warn, skipped, fixed, would, unreachable, timedOut := 0, 0, 0, 0, 0, 0, 0, 0
	for _, m := range machines {
		loads := workloadsFor(in.Workloads, m)
		if len(loads) == 0 {
			fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s roles=%s: no workload applies\n",
				oneline.Field(m.Name), oneline.Field(m.RoleList()))
			continue
		}
		if _, local := in.remoteFor(m); local && !in.DryRun {
			// Said once per machine, because it changes what every line below it means.
			fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s transport=local reason=this-is-the-machine\n",
				oneline.Field(m.Name))
		}
		build, unreached, firstAnswer := in.Build, "", ""
		if build == "" && !in.DryRun {
			build, unreached, firstAnswer = machineBuild(in, m)
		}
		if build == "" {
			build = "-"
		}
		if in.IfStale {
			if stale := staleClasses(held, m, loads, build, in.Hash, in.Now().UTC(), in.MaxAge); len(stale) == 0 {
				skipped++
				fmt.Fprintf(in.Stdout, "CERTIFY %s CURRENT classes=%d build=%s\n",
					oneline.Field(m.Name), len(loads), oneline.Field(build))
				continue
			}
		}
		// The verdict of every class this machine ran, and what it said. They are a map and
		// not a counter because the repair round runs a class AGAIN: what the run reports is
		// each class's FINAL verdict, so a class that failed, was repaired and passed is one
		// pass -- and a certificate -- rather than a failure the summary can never lose.
		verdicts, evidence := map[string]string{}, map[string]string{}
		rows := map[string]Certificate{}
		for _, w := range loads {
			// A forge question with no forge wired is SKIPPED, not failed: `release adopt`
			// certifies over ssh and has no runner list to read, and a FAIL there would
			// mean "this tool could not ask" rather than "this machine is wrong". No row is
			// written, so the class stays uncertified and the loop's own certify -- which
			// does carry a forge -- is what answers it.
			if w.Forge != "" && (in.Forge == nil || strings.TrimSpace(in.Repo) == "") {
				fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s class=%s skipped=no-forge\n",
					oneline.Field(m.Name), oneline.Field(w.Class))
				continue
			}
			if in.DryRun {
				fmt.Fprintf(in.Stdout, "CERTIFY %s %s WOULD wall=%t where=%s build=%s hash=%s\n",
					oneline.Field(m.Name), oneline.Field(w.Class), w.Wall, w.Where,
					oneline.Field(build), oneline.Field(in.Hash))
				would++
				continue
			}
			// The machine did not answer the first question at all. Its own classes carry
			// that same answer -- UNREACHABLE or TIMEOUT -- with the reason the first
			// attempt gave, and no second attempt is made; a coordinator class is still
			// answered, because the forge and this machine know what they know whether or
			// not anybody can ssh to the bench.
			if unreached != "" && w.Where != WhereCoordinator {
				verdicts[w.Class], evidence[w.Class] = firstAnswer, unreached
				in.appendAttempt(m, w.Class, firstAnswer, unreached)
				fmt.Fprintf(in.Stderr, "CERTIFY %s %s %s reason=%s\n",
					oneline.Field(m.Name), oneline.Field(w.Class), firstAnswer,
					oneline.Quote(oneline.Cap(unreached, EvidenceCap)))
				in.unansweredEvent(m, w.Class, firstAnswer, unreached)
				continue
			}
			in.certifyOne(reg, m, w, build, verdicts, evidence, rows)
		}
		// Fix, prove, escalate. Nothing here mutates a machine unless Fix is on, and even
		// then only through the Fixer seam.
		before := len(failedClasses(verdicts))
		esc, err := in.repair(reg, m, build, loads, verdicts, evidence, rows)
		if err != nil {
			return certifyRefusal(in.Stderr, err)
		}
		fixed += before - len(failedClasses(verdicts))
		if err := in.writeRows(rows); err != nil {
			return certifyRefusal(in.Stderr, err)
		}
		if esc != nil {
			fmt.Fprintln(in.Stderr, esc.Line())
			in.escalationEvent(*esc, build)
			in.postEscalation(*esc, build)
		}
		for _, v := range verdicts {
			switch v {
			case VerdictOK:
				ok++
			case VerdictWarn:
				warn++
			case VerdictUnreachable:
				unreachable++
			case VerdictTimeout:
				timedOut++
			default:
				fail++
			}
		}
	}
	// A dry run has its own closing line. `CERTIFY OK machines=1 ok=14` for a run that
	// reached nothing was this tool reporting fourteen passes it never made.
	if in.DryRun {
		fmt.Fprintf(in.Stdout, "CERTIFY DRY-RUN machines=%d would=%d\n", len(machines), would)
		return 0
	}
	w, result, code := in.Stdout, VerdictOK, 0
	switch {
	case fail > 0:
		w, result, code = in.Stderr, VerdictFail, 1
	case unreachable > 0:
		// Exit 3 is what every other fleet verb answers for a machine it could not reach,
		// and it is not exit 1: nothing failed, and a caller that treats them alike will act
		// on a machine it has no evidence about.
		w, result, code = in.Stderr, VerdictUnreachable, 3
	case timedOut > 0:
		w, result, code = in.Stderr, VerdictTimeout, 3
	}
	fmt.Fprintf(w, "CERTIFY %s machines=%d ok=%d fail=%d warn=%d skipped=%d unreachable=%d timeout=%d fixed=%d\n",
		result, len(machines), ok, fail, warn, skipped, unreachable, timedOut, fixed)
	return code
}

// certifyOne runs ONE workload against ONE machine: the row, the event, the line, and the
// verdict recorded under its class. It is one function because the repair round runs it a
// second time, and two spellings of "certify this class" is two records of what happened.
func (in CertifyInput) certifyOne(reg *Registry, m Machine, w Workload, build string, verdicts, evidence map[string]string, rows map[string]Certificate) {
	verdict, said := runWorkload(in, reg, m, w)
	verdicts[w.Class], evidence[w.Class] = verdict, said
	// Two things that are not verdicts about the machine, and neither is written down: a
	// machine nobody reached proved nothing, and work nobody let finish proved nothing. The
	// row that said `FAIL ... build=Host\x20key\x20verification` was a record of this
	// tool's own failure filed as the bench's, and a FAIL for `exec sleep` under a
	// one-millisecond bound is a judgement on work that never ran.
	if verdict == VerdictUnreachable || verdict == VerdictTimeout {
		// A row from an earlier attempt in this run would now be a lie by omission: the run
		// ends knowing less than it did, and the record must say so rather than keep the
		// older verdict as though it were this run's.
		delete(rows, w.Class)
		in.appendAttempt(m, w.Class, verdict, said)
		fmt.Fprintf(in.Stderr, "CERTIFY %s %s %s reason=%s\n",
			oneline.Field(m.Name), oneline.Field(w.Class), verdict,
			oneline.Quote(oneline.Cap(said, EvidenceCap)))
		in.unansweredEvent(m, w.Class, verdict, said)
		return
	}
	// ONE ROW PER (machine, class) PER RUN. The repair round certifies a class a second
	// time, and appending both left the record holding two verdicts for one pass -- the
	// first of them a failure that was no longer true when the run ended. The rows are held
	// and written once the machine's pass is over, so the record carries the FINAL verdict.
	rows[w.Class] = Certificate{
		Machine: m.Name, Build: build, Hash: in.Hash, Class: w.Class,
		Verdict: verdict, Evidence: said, At: in.Now().UTC(),
	}
	in.appendAttempt(m, w.Class, verdict, said)
	line := fmt.Sprintf("CERTIFY %s %s %s evidence=%s",
		oneline.Field(m.Name), oneline.Field(w.Class), verdict,
		oneline.Quote(oneline.Cap(said, EvidenceCap)))
	if verdict == VerdictOK {
		fmt.Fprintln(in.Stdout, line)
		return
	}
	// A WARN and a FAIL both go to stderr, where a person looking for what to do next looks.
	// A WARN changes neither the count that gates nor the exit.
	fmt.Fprintln(in.Stderr, line)
}

// appendAttempt writes ONE line per attempt, AS IT HAPPENS, beside the certificates.
//
// Holding the rows to one per (machine, class) is right for the record and wrong for the
// trail: a run killed halfway through a machine would leave nothing of what it had already
// learned, and the run that matters most is the one somebody interrupts because it is taking
// too long. The trail is append-only, one line per attempt including the ones that write no
// certificate at all (UNREACHABLE, TIMEOUT), and it is never read by the tool -- it is for
// the person who comes back to a machine and asks what happened.
//
// A trail that cannot be written is a NOTE and not a refusal: the run has real work to do
// and the record is the certificates file.
func (in CertifyInput) appendAttempt(m Machine, class, verdict, evidence string) {
	path := in.attemptsPath()
	if path == "" {
		return
	}
	line := strings.Join([]string{
		in.Now().UTC().Format(time.RFC3339), dash(m.Name), dash(class), dash(verdict),
		dash(oneline.Escape(oneline.Cap(evidence, EvidenceCap))),
	}, "\t")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = io.WriteString(f, line+"\n")
		_ = f.Close()
	}
	if err != nil {
		fmt.Fprintf(in.Stderr, "CERTIFY NOTE attempts=unwritten path=%s err=%s\n",
			oneline.Field(path), oneline.Quote(oneline.Cap(err.Error(), EvidenceCap)))
	}
}

// attemptsPath is --attempts, or attempts.log beside the certificates file. A dry run writes
// no trail, because it made no attempt.
func (in CertifyInput) attemptsPath() string {
	if in.DryRun {
		return ""
	}
	if p := strings.TrimSpace(in.Attempts); p != "" {
		return p
	}
	if strings.TrimSpace(in.Certs) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(in.Certs), "attempts.log")
}

// writeRows puts one machine's pass on the record: one row per class, in class order, each
// with the verdict the run ENDED on. It is called once per machine rather than once at the
// end, so a fleet run interrupted halfway keeps what it proved about the machines it
// finished.
func (in CertifyInput) writeRows(rows map[string]Certificate) error {
	classes := make([]string, 0, len(rows))
	for class := range rows {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		cert := rows[class]
		if err := AppendCertificate(in.Certs, cert); err != nil {
			return err
		}
		in.event(cert)
	}
	return nil
}

// repair is the fix-then-prove round: apply the standard items the failed classes map to,
// certify those classes once more, and answer the escalation for whatever still fails.
//
// It NEVER credits a repair. The classes it re-runs are run by the same workloads through
// the same wall, and the certificate that comes out is the certificate `fill` reads.
func (in CertifyInput) repair(reg *Registry, m Machine, build string, loads []Workload, verdicts, evidence map[string]string, rows map[string]Certificate) (*escalation, error) {
	if in.DryRun || len(failedClasses(verdicts)) == 0 {
		return nil, nil
	}
	if !in.Fix {
		// --no-fix: the failures are lines and rows, and the waiver is the person saying out
		// loud that they will look. Nothing is applied and nobody is paged.
		return nil, nil
	}
	e := &escalation{machine: m.Name, evidence: evidence}
	rounds := in.MaxFixRounds
	if rounds <= 0 {
		rounds = DefaultFixRounds
	}
	for round := 1; round <= rounds; round++ {
		failed := failedClasses(verdicts)
		if len(failed) == 0 {
			return nil, nil
		}
		items, byHand := ItemsForClasses(failed)
		if len(items) == 0 {
			// No item of the standard repairs any of these. Going to the machine anyway is a
			// round of load and a lie in the record.
			break
		}
		run := runnableItems(items)
		e.applied, e.byHand = run, byHand
		fmt.Fprintf(in.Stdout, "CERTIFY FIX machine=%s round=%d classes=%s items=%s by-hand=%s\n",
			oneline.Field(m.Name), round, oneline.Field(strings.Join(failed, ",")),
			oneline.Field(dash(strings.Join(run, ","))), oneline.Field(dash(strings.Join(byHand, ","))))
		changed, err := in.apply(m.Name, run)
		if err != nil {
			e.failed = oneline.Err(err)
			fmt.Fprintf(in.Stderr, "CERTIFY FIX machine=%s FAILED err=%s\n",
				oneline.Field(m.Name), oneline.Quote(oneline.Cap(err.Error(), EvidenceCap)))
			break
		}
		fmt.Fprintf(in.Stdout, "CERTIFY FIX machine=%s changed=%s\n",
			oneline.Field(m.Name), oneline.Field(dash(strings.Join(changed, ","))))
		for _, w := range loads {
			if verdicts[w.Class] != VerdictFail || len(ItemsForClass(w.Class)) == 0 {
				continue
			}
			in.certifyOne(reg, m, w, build, verdicts, evidence, rows)
		}
	}
	failed := failedClasses(verdicts)
	if len(failed) == 0 {
		return nil, nil
	}
	e.classes = failed
	return e, nil
}

// apply is the one call that mutates a machine, and it is behind the seam. A Fix with no
// Fixer is an error and never a silent pass: "on by default" cannot mean a guessed path.
func (in CertifyInput) apply(machine string, items []string) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if in.Fixer == nil {
		return nil, fmt.Errorf("no apply is wired; run: nova-pulse fleet standard --apply --machine %s", machine)
	}
	return in.Fixer.Apply(machine, items)
}

// runnableItems is the items apply actually runs: everything but the ones that are named and
// left to a person (`nova-update release adopt`).
func runnableItems(items []string) []string {
	var out []string
	for _, item := range items {
		if _, byHand := ByHandItems[item]; byHand {
			continue
		}
		out = append(out, item)
	}
	return out
}

// postEscalation carries the escalation to the lane through the bus seam. A run with no bus
// wired says so on a line rather than dropping the escalation quietly.
func (in CertifyInput) postEscalation(e escalation, build string) {
	if in.Bus == nil {
		fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s escalation=unsent reason=no-bus\n", oneline.Field(e.machine))
		return
	}
	lane := strings.TrimSpace(in.Lane)
	if lane == "" {
		lane = DefaultLane
	}
	if err := in.Bus.Post(lane, e.subject(), e.note(in.Hash, build)); err != nil {
		fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s escalation=unsent err=%s\n",
			oneline.Field(e.machine), oneline.Quote(oneline.Cap(err.Error(), EvidenceCap)))
		return
	}
	fmt.Fprintf(in.Stderr, "CERTIFY NOTE machine=%s escalation=sent lane=%s\n",
		oneline.Field(e.machine), oneline.Field(lane))
}

// Stale says whether the newest certificate for one machine and class is missing, failed,
// written under another build or standard, or simply older than maxAge. It is the one place
// "current" is decided for the trigger, and it agrees with Certified by construction: a
// certificate Certified accepts is stale only when it has aged out.
func Stale(certs []Certificate, machine, class, build, hash string, now time.Time, maxAge time.Duration) bool {
	if !Certified(certs, machine, class, build, hash) {
		return true
	}
	for i := range certs {
		c := certs[i]
		if c.Machine == machine && c.Class == class && c.Build == build && c.Hash == hash {
			if now.Sub(c.At) <= maxAge {
				return false
			}
		}
	}
	return true
}

// staleClasses is every class of this machine that is not current, in class order.
func staleClasses(certs []Certificate, m Machine, loads []Workload, build, hash string, now time.Time, maxAge time.Duration) []string {
	var out []string
	for _, w := range loads {
		if Stale(certs, m.Name, w.Class, build, hash, now, maxAge) {
			out = append(out, w.Class)
		}
	}
	return out
}

// StatusInput is `fleet certify --status`: the reading verb. It touches no machine and takes
// no forge -- it is the record, read back, one line per machine and class.
type StatusInput struct {
	Machines  string
	Certs     string
	Workloads []Workload
	Hash      string
	MaxAge    time.Duration
	Now       func() time.Time
	Stdout    io.Writer
	Stderr    io.Writer
}

// Status prints the current certificate per machine and class and answers 1 when any machine
// carries a stale or failed class, 0 when the fleet is certified, 2 on a refusal.
//
// The build is NOT read from the machines here: --status is the record, and reading twenty
// machines to print a file is how a status verb becomes something nobody runs. A row whose
// build no longer matches the fleet shows as stale on the next real run.
func Status(in StatusInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.MaxAge <= 0 {
		in.MaxAge = DefaultMaxAge
	}
	certs, err := ReadCertificates(in.Certs)
	if err != nil {
		return certifyRefusal(in.Stderr, err)
	}
	// --status reads ONE FILE. It used to refuse without a registry and without the
	// provisioning standard above the working directory -- for a verb that touches no
	// machine and answers from a record. With a registry it reports every machine and class
	// the fleet is MEANT to hold, including the ones nobody has certified at all; without
	// one it reports what the record carries, which is what a person asking "what do we
	// know" wants at the moment they cannot find the registry.
	pairs, err := statusPairs(in, certs)
	if err != nil {
		return certifyRefusal(in.Stderr, err)
	}
	now := in.Now().UTC()
	current, stale := 0, 0
	for _, pair := range pairs {
		machine, class := pair[0], pair[1]
		c, found := newestCertificate(certs, machine, class)
		switch {
		case !found:
			stale++
			fmt.Fprintf(in.Stderr, "CERTIFY STATUS %s %s NONE\n", oneline.Field(machine), oneline.Field(class))
		case c.Verdict == VerdictFail:
			stale++
			fmt.Fprintf(in.Stderr, "CERTIFY STATUS %s %s FAIL build=%s at=%s evidence=%s\n",
				oneline.Field(machine), oneline.Field(class), oneline.Field(c.Build),
				c.At.UTC().Format(time.RFC3339), oneline.Quote(c.Evidence))
		case c.Hash != in.Hash && in.Hash != "":
			stale++
			fmt.Fprintf(in.Stderr, "CERTIFY STATUS %s %s STALE reason=standard-hash was=%s now=%s\n",
				oneline.Field(machine), oneline.Field(class), oneline.Field(c.Hash), oneline.Field(in.Hash))
		case now.Sub(c.At) > in.MaxAge:
			stale++
			fmt.Fprintf(in.Stderr, "CERTIFY STATUS %s %s STALE reason=age at=%s max-age=%s\n",
				oneline.Field(machine), oneline.Field(class), c.At.UTC().Format(time.RFC3339), in.MaxAge)
		default:
			current++
			fmt.Fprintf(in.Stdout, "CERTIFY STATUS %s %s %s build=%s at=%s\n",
				oneline.Field(machine), oneline.Field(class), c.Verdict,
				oneline.Field(c.Build), c.At.UTC().Format(time.RFC3339))
		}
	}
	w, result, code := in.Stdout, VerdictOK, 0
	if stale > 0 {
		w, result, code = in.Stderr, VerdictFail, 1
	}
	fmt.Fprintf(w, "CERTIFY STATUS %s current=%d stale=%d\n", result, current, stale)
	return code
}

// statusPairs is every (machine, class) --status reports on. With a registry it is what the
// fleet is MEANT to hold -- every machine's roles crossed with the workloads, so a class
// nobody has ever certified shows as NONE. With no registry it is what the record carries,
// sorted, so the verb still answers when the registry is not at hand.
func statusPairs(in StatusInput, certs []Certificate) ([][2]string, error) {
	var out [][2]string
	if strings.TrimSpace(in.Machines) == "" {
		seen := map[[2]string]bool{}
		for _, c := range certs {
			key := [2]string{c.Machine, c.Class}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, key)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i][0] != out[j][0] {
				return out[i][0] < out[j][0]
			}
			return out[i][1] < out[j][1]
		})
		return out, nil
	}
	reg, err := ReadRegistry(in.Machines)
	if err != nil {
		return nil, err
	}
	loads := in.Workloads
	if loads == nil {
		loads, err = StandardWorkloads()
		if err != nil {
			return nil, err
		}
	}
	for _, m := range reg.Machines() {
		for _, w := range workloadsFor(loads, m) {
			out = append(out, [2]string{m.Name, w.Class})
		}
	}
	return out, nil
}

// newestCertificate is the latest row for one machine and class, whatever build or hash it
// was written under: --status reports what IS recorded, and says why it no longer counts.
func newestCertificate(certs []Certificate, machine, class string) (Certificate, bool) {
	var newest *Certificate
	for i := range certs {
		c := certs[i]
		if c.Machine != machine || c.Class != class {
			continue
		}
		if newest == nil || !c.At.Before(newest.At) {
			newest = &certs[i]
		}
	}
	if newest == nil {
		return Certificate{}, false
	}
	return *newest, true
}

// event writes the structured JSON line BESIDE the CERTIFY line, through internal/log --
// the same Emitter `nova-pulse launch` uses, not a second one. It writes nothing when no Log
// is configured, which is how every test that predates the stream keeps its exact output.
// A certificate is exactly the shape a dashboard wants: machine, class, verdict, build.
func (in CertifyInput) event(c Certificate) {
	if in.Log == nil {
		return
	}
	clock := in.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	l := log.New(Clock(clock), log.ProcessGUID, "nova-pulse")
	l.Verb = "certify"
	l.Event = "certify"
	l.Bench = c.Machine
	l.Msg = fmt.Sprintf("certify: %s %s %s build=%s evidence=%s", c.Machine, c.Class, c.Verdict, c.Build, c.Evidence)
	switch c.Verdict {
	case VerdictFail:
		l.Level = "ERROR"
		l.Err = c.Evidence
	case VerdictWarn:
		l.Level = "WARN"
	}
	_ = l.Write(in.Log)
}

// escalationEvent is the same stream, for the event a dashboard must never miss: a machine
// the loop could not certify and could not repair. It is ERROR, it carries the machine and
// the classes, and it is one event per machine, like the line.
func (in CertifyInput) escalationEvent(e escalation, build string) {
	if in.Log == nil {
		return
	}
	clock := in.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	l := log.New(Clock(clock), log.ProcessGUID, "nova-pulse")
	l.Verb = "certify"
	l.Event = "certify"
	l.Level = "ERROR"
	l.Bench = e.machine
	l.Msg = fmt.Sprintf("certify escalate: %s classes=%s applied=%s build=%s remedy=%s",
		e.machine, strings.Join(e.classes, ","), dash(strings.Join(e.applied, ",")), dash(build), e.remedy())
	l.Err = e.remedy()
	_ = l.Write(in.Log)
}

// unansweredEvent is the third thing the stream carries: not a certificate and not an
// escalation, but this tool saying it could not ask, or could not wait long enough. WARN and not ERROR -- nothing about the
// machine is known to be wrong -- and it names the transport so a dashboard can tell a fleet
// with a broken bench from a coordinator with a broken ssh.
func (in CertifyInput) unansweredEvent(m Machine, class, verdict, reason string) {
	if in.Log == nil {
		return
	}
	clock := in.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	l := log.New(Clock(clock), log.ProcessGUID, "nova-pulse")
	l.Verb = "certify"
	l.Event = "certify"
	l.Level = "WARN"
	l.Bench = m.Name
	l.Msg = fmt.Sprintf("certify %s: %s %s transport=%s reason=%s", strings.ToLower(verdict), m.Name, class, m.SSH, reason)
	l.Err = reason
	_ = l.Write(in.Log)
}

// Clock is internal/log's clock, re-exported here only so a caller need not import both.
type Clock = log.Clock

func certifyRefusal(w io.Writer, err error) int {
	fmt.Fprintf(w, "CERTIFY REFUSED: %s\n", oneline.Err(err))
	return 2
}

// workloadsFor is every workload that applies to at least one of the machine's roles, in
// class order, each once. A machine that is both bench and runner runs both sets, which is
// the point: hulk is both, and it was the bench half that was never certified.
func workloadsFor(loads []Workload, m Machine) []Workload {
	var out []Workload
	for _, w := range loads {
		for _, role := range m.Roles {
			if w.AppliesTo(role) {
				out = append(out, w)
				break
			}
		}
	}
	sortWorkloads(out)
	return out
}

// machineBuild asks the machine what nova-merge it is running. The build is half of what
// makes a certificate current, so it is read FROM the machine and never assumed: `release
// adopt` changes it, and a certificate written before an adopt must not survive it.
//
// It is also the run's FIRST contact with the machine, so it is where "unreachable" is
// found: if this answer is the transport failing, nothing else is asked of that machine, and
// the reason is carried to every one of its classes. Eleven doomed ssh attempts teach a
// person nothing the first one did not.
func machineBuild(in CertifyInput, m Machine) (build, reason, token string) {
	out, err := runScript(in, m, "build", BuildScript)
	if errors.Is(err, ErrTimeout) {
		return "", oneline.Err(err), VerdictTimeout
	}
	if said, bad := TransportFailure(out, err); bad {
		return "", said, VerdictUnreachable
	}
	return BuildVersion(out), "", ""
}

// BuildScript is the one question every certification asks first: what build is installed
// here? It is exported because the fill asks the same question, of the same machines, and
// two spellings of it would be two answers.
const BuildScript = "# nova-certify workload build\nnova-merge version 2>&1 || true\n"

// BuildVersion reads the version token out of a `nova-merge version` line. The token, not
// the line: `nova-merge v0.17.0` and `v0.17.0` are the same build, and a certificate keyed
// on the whole line would expire when the banner changed.
//
// A line with NO version token is no build at all, and it used to be kept whole: the first
// real run of this verb wrote `build=Host\x20key\x20verification\x20failed.` into a
// certificate row, where the build column is half of what makes a certificate current. The
// column holds a version or it holds `-`.
func BuildVersion(out string) string {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, f := range strings.Fields(line) {
			if len(f) > 1 && f[0] == 'v' && f[1] >= '0' && f[1] <= '9' {
				return f
			}
		}
	}
	return ""
}

// runWorkload runs one workload against one machine and answers the verdict and the one
// line of evidence that goes on the line and into the row.
//
// THE VERDICT COMES FROM WHAT THE MACHINE SAID, never from the exit code alone. The hulk
// card exited non-zero for a reason no exit code could name, and a tool that reported
// `exit status 1` would have sent a person back to the machine to find out what this run
// already knows.
func runWorkload(in CertifyInput, reg *Registry, m Machine, w Workload) (string, string) {
	verdict, evidence := answer(in, reg, m, w)
	// `report: yes` is the whole difference between a measurement and a gate. It softens a
	// FAIL and never an UNREACHABLE: "we could not ask" is not a measurement.
	if verdict == VerdictFail && w.Report {
		return VerdictWarn, evidence
	}
	return verdict, evidence
}

func answer(in CertifyInput, reg *Registry, m Machine, w Workload) (string, string) {
	switch w.Forge {
	case ForgeRunners:
		return runnersOnline(in, m)
	case ForgeRegistry:
		return registryTruth(in, reg, m)
	}
	script := certifyScript(in, w)
	// The branch comes BEFORE the run, not after it. Written the other way round -- run,
	// then decide -- the ssh had already happened, which is exactly the fault `where` exists
	// to prevent, and no test of the embedded set could see it because the only coordinator
	// workloads there ask the forge and run no script at all.
	out, err := "", error(nil)
	if w.Where == WhereCoordinator {
		out, err = runHere(in, w.Class, script)
	} else {
		out, err = runScript(in, m, w.Class, script)
	}
	// FINISHING COMES FIRST, before the matched line. `echo PROOF OK` followed by `exec
	// sleep 3` under a 100ms bound printed the expected line, ran out of time, and was
	// recorded OK with a certificate behind it: the marker said the work had STARTED, and a
	// certificate is a claim that it FINISHED. A workload that printed its line and then ran
	// out of time is TIMEOUT, never OK.
	if errors.Is(err, ErrTimeout) {
		return VerdictTimeout, oneline.Err(err)
	}
	// The evidence is the WHOLE LINE the expect matched, not the matched text: `^GO OK` is
	// four characters and `GO OK go version go1.26.5 linux/amd64 ok 0.4s` is the answer a
	// person reading the certificate in a month actually needs.
	if line, ok := matchedLine(w.Expect, out); ok {
		return VerdictOK, line
	}
	// Reaching the machine comes before judging it: a machine that printed the marker WAS
	// reached, whatever else came back.
	if reason, bad := TransportFailure(out, err); bad {
		return VerdictUnreachable, reason
	}
	if reason := firstAnswerLine(out); reason != "" {
		return VerdictFail, reason
	}
	if err != nil {
		return VerdictFail, err.Error()
	}
	return VerdictFail, fmt.Sprintf("the machine said nothing matching %s", w.Expect.String())
}

// registryTruth holds the registry against what the forge is ACTUALLY running. A machine
// serving merge-group shards that the registry does not call a runner is the lock broken in
// the only direction the lock cannot see: on 2026-09-18 space had sixteen online
// `space-nova-*` runners and roles `bench,services`, so every guard in the tools was happy
// to put cards on a machine that was serving the merge group.
//
// It is also where a registration with no machine behind it is caught: `vision-nova-\u25cf`,
// a literal bullet, sat offline on the forge with no runner directory anywhere.
func registryTruth(in CertifyInput, reg *Registry, m Machine) (string, string) {
	runners, err := in.Forge.Runners(in.Repo)
	if err != nil {
		return VerdictFail, oneline.Err(err)
	}
	prefix := m.Name + "-nova-"
	online, stale := 0, []string{}
	for _, r := range runners {
		if !strings.HasPrefix(r.Name, prefix) {
			continue
		}
		if r.Status == "online" {
			online++
			continue
		}
		stale = append(stale, r.Name)
	}
	if online > 0 && !m.HasRole(RoleRunner) {
		return VerdictFail, fmt.Sprintf(
			"%s serves %d online %s* runner(s) and %s says roles=%s; add the %s role, or deregister them",
			m.Name, online, prefix, reg.Path(), m.RoleList(), RoleRunner)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return VerdictFail, fmt.Sprintf("the forge carries %d registration(s) with nothing behind them: %s",
			len(stale), strings.Join(stale, ", "))
	}
	if m.HasRole(RoleRunner) && online == 0 {
		return VerdictFail, fmt.Sprintf("%s carries the %s role and the forge names no online %s*", m.Name, RoleRunner, prefix)
	}
	return VerdictOK, fmt.Sprintf("roles=%s online=%d prefix=%s", m.RoleList(), online, prefix)
}

// ErrTimeout is the work not finishing inside this run's bound. It is a sentinel because
// what a Remote returns for a killed child is its own business -- `signal: killed` from
// exec, a context error from another -- and the DISTINCTION must not depend on the wording.
var ErrTimeout = errors.New("the work did not finish inside the timeout")

func runScript(in CertifyInput, m Machine, class, script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()
	remote, _ := in.remoteFor(m)
	out, err := remote.Run(ctx, m.SSH, script)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded) {
		return out, fmt.Errorf("%w (%s)", ErrTimeout, in.Timeout)
	}
	return out, err
}

// runHere runs a coordinator workload's body ON THE COORDINATOR. `where: coordinator` is not
// only about the forge: a workload that asks whether `gh` is authenticated, or whether the
// queue is where it should be, is a question about THIS machine, and the first draft sent
// its body down the ssh pipe to the bench -- which is the one thing `where` exists to stop.
func runHere(in CertifyInput, class, script string) (string, error) {
	if in.Local == nil {
		return "", fmt.Errorf("no coordinator runner is wired, and a %s workload never opens an ssh; inject a fleet.Remote as Local", WhereCoordinator)
	}
	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()
	out, err := in.Local.Run(ctx, "", script)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded) {
		return out, fmt.Errorf("%w (%s)", ErrTimeout, in.Timeout)
	}
	return out, err
}

// remoteFor is the transport for one machine: the local runner when the machine IS this
// machine, ssh otherwise. It answers whether it chose the local one, so the run can say so
// on a line -- "it ran here" is the kind of thing a person needs told once, not guessed.
func (in CertifyInput) remoteFor(m Machine) (Remote, bool) {
	if in.Local != nil && IsLocalMachine(m, in.LocalHost) {
		return in.Local, true
	}
	return in.Remote, false
}

// IsLocalMachine says whether a registry machine is the machine this process runs on. The
// registry's name, its ssh target and the host's own name are all compared as SHORT host
// names, because the same machine is `space`, `nova@space` and `space.tail1234.ts.net`
// depending on who is writing it down.
func IsLocalMachine(m Machine, localHost string) bool {
	local := shortHost(localHost)
	if local == "" {
		return false
	}
	for _, candidate := range []string{m.Name, m.SSH} {
		if c := shortHost(candidate); c != "" && strings.EqualFold(c, local) {
			return true
		}
	}
	return false
}

// shortHost is a host name with any user, port and domain taken off.
func shortHost(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndex(v, "@"); i >= 0 {
		v = v[i+1:]
	}
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "."); i >= 0 {
		v = v[:i]
	}
	return v
}

// transportMarkers are ssh's OWN words for "I never reached the machine". They are matched
// against what came back rather than against an exit code, because ssh exits 255 for its own
// failures and the remote command's status for everything else, and a fake or another
// transport has neither.
var transportMarkers = []string{
	"host key verification failed",
	"ssh: connect to host",
	"ssh: could not resolve hostname",
	"permission denied (publickey",
	"connection refused",
	"connection timed out",
	"connection closed by remote host",
	"connection reset by peer",
	"no route to host",
	"kex_exchange_identification",
	"operation timed out",
	"broken pipe",
	"remote host identification has changed",
}

// TransportFailure says whether this answer is the transport failing rather than the machine
// answering, and names the line that said so. THE DISTINCTION IS THE WHOLE POINT: what a
// machine said about its own work is a verdict, and what ssh said about reaching it is not.
func TransportFailure(out string, err error) (string, bool) {
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		for _, marker := range transportMarkers {
			if strings.Contains(lower, marker) {
				return line, true
			}
		}
	}
	return "", false
}

// runnersOnline is the runner host's own workload: the forge says every runner named
// `<machine>-nova-*` is online. It names the runner that is NOT, because "one of six is
// down" sends a person to look at six.
func runnersOnline(in CertifyInput, m Machine) (string, string) {
	runners, err := in.Forge.Runners(in.Repo)
	if err != nil {
		return VerdictFail, oneline.Err(err)
	}
	prefix := m.Name + "-nova-"
	seen, offline := 0, []string{}
	for _, r := range runners {
		if !strings.HasPrefix(r.Name, prefix) {
			continue
		}
		seen++
		if r.Status != "online" {
			offline = append(offline, r.Name)
		}
	}
	if seen == 0 {
		return VerdictFail, fmt.Sprintf("the forge names no runner %s* on %s", prefix, in.Repo)
	}
	if len(offline) > 0 {
		sort.Strings(offline)
		return VerdictFail, fmt.Sprintf("offline: %s (of %d)", strings.Join(offline, ", "), seen)
	}
	return VerdictOK, fmt.Sprintf("%d runner(s) %s* online", seen, prefix)
}

// certifyScript composes what goes down the ssh pipe. A plain workload is its body with the
// marker; a wall workload is its body inside `nova-sandbox`, with a job directory of its
// own that is the only writable path, a HOME inside that directory (without it the wall
// denies the first config write), a `--cwd` inside it (without it getcwd is denied and
// every git command dies before it reads anything), and each declared root as a `--read`.
//
// The job directory is made for the run and taken away after it, on every exit path: Glenn,
// 2026-09-17, hygiene -- a job lives in working/tmp, is read, and is deleted.
func certifyScript(in CertifyInput, w Workload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# nova-certify workload %s\n", w.Class)
	b.WriteString("set -eu\n")
	bin := in.Bin
	if strings.TrimSpace(bin) == "" {
		bin = "$HOME/.local/bin"
	}
	fmt.Fprintf(&b, "NOVA_HOME=\"$HOME\"\nNOVA_GO=%q\nNOVA_BIN=%q\nexport NOVA_GO NOVA_BIN\n", DefaultGo, bin)
	if !w.Wall {
		b.WriteString("export NOVA_HOME\n")
		b.WriteString(w.Body)
		b.WriteString("\n")
		return b.String()
	}
	fmt.Fprintf(&b, "JOB=\"$NOVA_HOME/working/tmp/nova-certify-%s-$$\"\n", w.Class)
	b.WriteString("mkdir -p \"$JOB/home\"\n")
	b.WriteString("trap 'rm -rf \"$JOB\"' EXIT INT TERM\n")
	// The preamble is written unquoted so the machine's own HOME and wanted Go reach the
	// body; the body itself goes through a QUOTED heredoc, so nothing in it is expanded
	// out here where its $ and ` would mean something else.
	b.WriteString("printf 'NOVA_HOME=%s\\nNOVA_GO=%s\\nNOVA_BIN=%s\\nexport NOVA_HOME NOVA_GO NOVA_BIN\\n' \"$NOVA_HOME\" \"$NOVA_GO\" \"$NOVA_BIN\" > \"$JOB/body.sh\"\n")
	b.WriteString("cat >> \"$JOB/body.sh\" <<'NOVA_CERTIFY_BODY'\n")
	b.WriteString(w.Body)
	b.WriteString("\nNOVA_CERTIFY_BODY\n")
	// A read root that is not on this machine is not a guess and not a failure: the wall
	// refuses a `--read` that does not exist, so each root is named only when it is there,
	// and a bench whose toolchain is missing fails on the WORK rather than on the flags.
	b.WriteString("NOVA_READS=\"\"\nfor r in")
	for _, r := range w.Reads {
		fmt.Fprintf(&b, " %q", strings.ReplaceAll(r, "$HOME", "$NOVA_HOME"))
	}
	b.WriteString("; do\n  if [ -e \"$r\" ]; then NOVA_READS=\"$NOVA_READS --read $r\"; fi\ndone\n")
	b.WriteString("HOME=\"$JOB/home\" nova-sandbox $NOVA_READS --write \"$JOB\" --cwd \"$JOB\" -- /bin/sh \"$JOB/body.sh\"\n")
	return b.String()
}

// matchedLine is the first line of the output the expect matches, whole.
func matchedLine(re *regexp.Regexp, out string) (string, bool) {
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line != "" && re.MatchString(line) {
			return line, true
		}
	}
	return "", false
}

// firstAnswerLine is the one line of a machine's answer that a person needs: the first
// non-empty one, which is what the tool that failed said about itself.
func firstAnswerLine(out string) string {
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			return line
		}
	}
	return ""
}
