package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// AdoptHeader is the checks file watch --adopt reads: one adoption check per
// line, the command held to the same argv rule as every other exec site.
const AdoptHeader = "check\tcommand\towner"

// AdoptCheck is one row of the checks file.
type AdoptCheck struct {
	Name, Owner string
	Command     []string
}

// LoadAdopt reads the checks file watch --adopt names. The file is read
// alike from the generated AdoptChecks table wire, its padded standard
// base64, its schema JSON text and today's legacy TSV, through pkg/schemaread
// (S1, S2): a malformed or clamped value is refused by name, never read as
// absent, empty or zero, and every read is counted per form (C13, S4).
func LoadAdopt(r io.Reader) ([]AdoptCheck, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	doc, _, err := adoptRead.Read(raw)
	if err != nil {
		return nil, err
	}
	return adoptOf(doc), nil
}

// adoptDoc holds one AdoptChecks read through pkg/schemaread and the region
// its text references point into, so the value outlives the read that filled it.
type adoptDoc struct {
	v      *wire.AdoptChecks
	region []byte
}

// adoptRead is the one strict reader of the adopt checks file (S2, UNION.md
// section 6): the generated AdoptChecks table's binary wire, its padded
// standard base64, its schema JSON text and the legacy TSV people write by
// hand. It refuses damage by name and counts reads per form.
var adoptRead = schemaread.NewReader(adoptCodec())

// adoptCodec is the checks file's Codec for pkg/schemaread: the generated
// AdoptChecks table's wire, padded base64 and JSON text, the legacy TSV, and
// the constraints the read enforces.
func adoptCodec() *schemaread.Codec[adoptDoc] {
	return &schemaread.Codec[adoptDoc]{
		Name:  "AdoptChecks",
		Help:  "fix the checks file: the three-column header, then one check per line, check<TAB>command<TAB>owner",
		Reset: func(d *adoptDoc) { *d = adoptDoc{} },
		Measure: func(w []byte) error {
			_, err := wire.AdoptChecksLoadMeasureReason(w)
			return err
		},
		LoadWire:    loadAdoptWire,
		LoadJSON:    loadAdoptJSON,
		Legacy:      adoptLegacyForm,
		LoadLegacy:  loadAdoptLegacy,
		Constraints: adoptConstraints,
		SaveWire: func(d *adoptDoc) []byte {
			n, err := wire.AdoptChecksMeasureReason(d.v)
			if err != nil || n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.AdoptChecksSave(d.v, buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(d *adoptDoc) []byte {
			n := wire.AdoptChecksToJsonMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if wire.AdoptChecksToJson(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

// adoptReport folds a generated load report into the strict read's.
func adoptReport(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

// loadAdoptWire loads the table wire and keeps the region the value's text
// points into, as the generated load returns a value that borrows it.
func loadAdoptWire(d *adoptDoc, w []byte) schemaread.Report {
	n, err := wire.AdoptChecksLoadMeasureReason(w)
	if err != nil || n < 0 {
		rep := schemaread.Report{Malformed: true}
		if err != nil {
			rep.Reason = err.Error()
		}
		return rep
	}
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	d.region = raw[off : off+uintptr(n)]
	var rep wire.TableReport
	if d.v = wire.AdoptChecksLoad(d.region, w, &rep); d.v == nil {
		return schemaread.Report{Malformed: true, Reason: rep.Reason}
	}
	return adoptReport(&rep)
}

// loadAdoptJSON builds the table from its schema JSON text form and locks it;
// the builder's region backs every string the value references.
func loadAdoptJSON(d *adoptDoc, text []byte) schemaread.Report {
	var b wire.AdoptChecksBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	var rep wire.TableReport
	if !wire.AdoptChecksFromJson(&b, text, &rep) {
		b.Shutdown()
		return schemaread.Report{Malformed: true, Reason: rep.Reason}
	}
	if !b.Lock() {
		b.Shutdown()
		return schemaread.Report{Malformed: true}
	}
	d.region = b.Region()
	d.v = b.AsConst()
	return adoptReport(&rep)
}

// adoptLegacyForm recognises today's hand-written checks file, with its
// header and # comment lines, so every file the TSV reader read stays
// readable (RECIPE step 2: people write these files by hand). Input that
// opens with the header line is legacy, with or without a newline. Other
// input is legacy when it is UTF-8 text with no control byte but tab, CR and
// LF, and is neither the JSON text form (it opens with `{`) nor the padded
// base64 form (only base64 characters and line breaks): the binary wire
// leads with its form byte, a control byte, so it is never legacy.
func adoptLegacyForm(in []byte) bool {
	if bytes.HasPrefix(in, []byte(AdoptHeader)) {
		return true
	}
	if !utf8.Valid(in) {
		return false
	}
	for _, b := range in {
		if b < 0x20 && b != '\t' && b != '\r' && b != '\n' || b == 0x7f {
			return false
		}
	}
	trimmed := bytes.TrimSpace(in)
	if len(trimmed) == 0 || trimmed[0] == '{' {
		return false
	}
	for _, b := range trimmed {
		base64ish := b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' ||
			b == '+' || b == '/' || b == '=' || b == '\r' || b == '\n'
		if !base64ish {
			return true
		}
	}
	return false
}

// adoptLegacyRow is one TSV row as the legacy reader parses it, in the
// schema JSON text form: the command is a list of Word records.
type adoptLegacyRow struct {
	Check   string          `json:"check"`
	Command []adoptWordJSON `json:"command"`
	Owner   string          `json:"owner"`
}

// adoptWordJSON is one command word in the schema JSON text form.
type adoptWordJSON struct {
	Text string `json:"text"`
}

// loadAdoptLegacy reads the legacy TSV into the AdoptChecks value. The
// refusals are the ones LoadAdopt made today, each by name: a header that is
// not exactly the three-column one, a line with not three fields, an empty
// field, a duplicate check and a command argv refuses.
func loadAdoptLegacy(d *adoptDoc, in []byte) schemaread.Report {
	rows, reason := parseAdoptLines(in)
	if reason != "" {
		return schemaread.Report{Reason: reason}
	}
	js, err := json.Marshal(struct {
		Checks []adoptLegacyRow `json:"checks"`
	}{rows})
	if err != nil {
		return schemaread.Report{Malformed: true}
	}
	var b wire.AdoptChecksBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	var rep wire.TableReport
	if !wire.AdoptChecksFromJson(&b, js, &rep) || !b.Lock() {
		b.Shutdown()
		return schemaread.Report{Malformed: true, Reason: rep.Reason}
	}
	d.region = b.Region()
	d.v = b.AsConst()
	return adoptReport(&rep)
}

// parseAdoptLines splits the legacy TSV into rows, line by line as LoadAdopt
// scanned it, refusing each line exactly as LoadAdopt refused it. The first
// line must equal the header byte for byte; a `#` first character opens a
// comment. The row list starts empty, not nil, so a header-only file is the
// empty checks list and not an absent one. The table's text is UTF-8, so a
// line that is not UTF-8 is refused by name rather than rewritten.
func parseAdoptLines(in []byte) ([]adoptLegacyRow, string) {
	sc := bufio.NewScanner(bytes.NewReader(in))
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != AdoptHeader {
		return nil, fmt.Sprintf("line 1: invalid header (put the header back exactly: %s)", tabbed(AdoptHeader))
	}
	seen := map[string]bool{}
	rows := []adoptLegacyRow{}
	line := 1
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.HasPrefix(text, "#") {
			continue
		}
		if !utf8.ValidString(text) {
			return nil, fmt.Sprintf("line %d: not UTF-8 text (save the checks file as UTF-8)", line)
		}
		f := strings.Split(text, "\t")
		if len(f) != 3 {
			return nil, fmt.Sprintf("line %d: %d fields, want 3 (use the three-column checks header)", line, len(f))
		}
		for _, v := range f {
			if v == "" {
				return nil, fmt.Sprintf("line %d: empty field (supply check, command and owner)", line)
			}
		}
		if seen[f[0]] {
			return nil, fmt.Sprintf("line %d: duplicate check %s (give each check a distinct name)", line, f[0])
		}
		seen[f[0]] = true
		args, err := argv(f[1])
		if err != nil {
			return nil, fmt.Sprintf("line %d: %v", line, err)
		}
		cmd := make([]adoptWordJSON, 0, len(args))
		for _, a := range args {
			cmd = append(cmd, adoptWordJSON{Text: a})
		}
		rows = append(rows, adoptLegacyRow{Check: f[0], Command: cmd, Owner: f[2]})
	}
	if sc.Err() != nil {
		return nil, fmt.Sprintf("line %d: unreadable checks file (use lines below 1 MiB)", line)
	}
	return rows, ""
}

// adoptConstraints holds the record to the checks reader's rules, the same
// three non-empty fields and distinct names LoadAdopt enforced today: a check
// name, an argv command and an owner, all present, and no name twice
// (adopt.go, the three-field and duplicate refusals). The table declares no
// bound: every text is uncapped and every list is open, so nothing here can
// clamp (schemas/release/release.schema, the area's variability rule).
func adoptConstraints(d *adoptDoc, c *schemaread.Checker) {
	c.Required("record", d.v != nil)
	if d.v == nil {
		return
	}
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < d.v.Checks.Count; i++ {
		e := d.v.Checks.At(i)
		c.Required("check", adoptStr(&e.CheckName) != "")
		adoptArgvWords(c, &e.Command)
		c.Required("owner", adoptStr(&e.Owner) != "")
		n := adoptStr(&e.CheckName)
		ok = ok && !seen[n]
		seen[n] = true
	}
	c.Required("checks.check distinct", ok)
}

// adoptArgvWords holds the command to the argv rule LoadAdopt applied (adopt.go
// via argv, manifest.go): at least one word, each with no blank, quote or line
// break.
func adoptArgvWords(c *schemaread.Checker, l *wire.TableList[wire.Word]) {
	c.Required("command", l.Count > 0)
	for i := int32(0); i < l.Count; i++ {
		w := adoptStr(&l.At(i).Text)
		c.Required("command word", w != "" && !strings.ContainsAny(w, " \r\n\t\"'"))
	}
}

// adoptStr is a *string field's text; a zero slot is absent, not a value.
func adoptStr(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// adoptOf is the checks the read produced, as LoadAdopt always returned them.
func adoptOf(d adoptDoc) []AdoptCheck {
	out := make([]AdoptCheck, 0, d.v.Checks.Count)
	for i := int32(0); i < d.v.Checks.Count; i++ {
		e := d.v.Checks.At(i)
		cmd := make([]string, 0, e.Command.Count)
		for j := int32(0); j < e.Command.Count; j++ {
			cmd = append(cmd, adoptStr(&e.Command.At(j).Text))
		}
		out = append(out, AdoptCheck{Name: adoptStr(&e.CheckName), Owner: adoptStr(&e.Owner), Command: cmd})
	}
	return out
}

type adoptResult struct {
	check        AdoptCheck
	ok           bool
	detail       string
	reason       string
	remedy       string
	observedLine string
}

func runAdoptChecks(ctx context.Context, env Environment, checks []AdoptCheck, timeout time.Duration) []adoptResult {
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
				p := process(child, env.Env, c.Command, nil, ChildCap)
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
	rs := runAdoptChecks(ctx, env, checks, o.timeout)
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
		if _, err := fmt.Fprint(errs, refuseBuf.String()); err != nil {
			return 1
		}
	}
	w := out
	if refused > 0 {
		w = errs
	}
	if _, err := fmt.Fprintln(w, done); err != nil {
		return 1
	}
	code := 0
	if refused > 0 {
		code = 1
	}
	if o.to != "" {
		var body bytes.Buffer
		fmt.Fprintf(&body, "From: %s\nTo: %s\nSubject: adoption on %s at %s\n\n", o.as, o.to, dash(o.host), started.UTC().Format("2006-01-02T15:04:05Z"))
		body.Write(okBuf.Bytes())
		body.Write(refuseBuf.Bytes())
		fmt.Fprintln(&body, done)
		line, serr := postAdoptReceipt(ctx, o, body.Bytes(), env)
		if serr != nil {
			_, _ = fmt.Fprintf(errs, "ADOPT NOTE %s\n", oneline.Err(serr)) // ignored: the failed receipt already returns 1
			return 1
		}
		if _, err := fmt.Fprintf(out, "ADOPT SENT to=%s line=%s\n", field(o.to), field(line)); err != nil {
			return 1
		}
	}
	return code
}

// postAdoptReceipt publishes the adoption receipt on the Redis bus.
// SPEC-UPDATE.md rule 27: with --as and --to, the pass posts the receipt as its own.
// nova-bus reads the store from NOVA_BUS_REDIS; the confirmation is SEND OK id=.
func postAdoptReceipt(ctx context.Context, o options, body []byte, env Environment) (string, error) {
	_, line, _, err := redisSend(ctx, env, o.as, o.to, noteSubject(body), body, "retry watch with the same --adopt")
	return line, err
}

func loadAdoptFile(path string) ([]AdoptCheck, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // ignored: the checks file is opened only to be read
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
	f.StringVar(&o.as, "as", "", "the sender the receipt is from; with --to, the pass posts the receipt through nova-bus send (nova-bus reads its store from NOVA_BUS_REDIS)")
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
	// A delivery needs the sender and the recipients; nova-bus reads the store itself.
	if o.as != "" || o.to != "" {
		for _, x := range []struct{ n, v string }{{"as", o.as}, {"to", o.to}} {
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
