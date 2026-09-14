// nova-review records the mechanical ground for a review. This first slice
// deliberately contains packets and head-bound policies only; verdict folding,
// findings and cost accounting remain specified work, not implied by this binary.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-review: exact-revision review packets and durable policies (docs/SPEC-REVIEW.md)

usage:
  nova-review packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --out <file>
  nova-review policy --lane <dir> (--pr <n>|--branch <name>) --who <name> --readers <name,...> --reserved <name,...> --deadline <stamp> --reason <text> --head <sha>
  nova-review version
  nova-review help

This production slice writes exact-head packets and immutable policy records.
It does not yet implement verdict, answer, roster, dedupe, or cost.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now)) }

func run(args []string, out, errOut io.Writer, now func() time.Time) int {
	if len(args) == 0 {
		return refused(errOut, "no verb given")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	case "version":
		if len(args) != 1 {
			return refused(errOut, "version takes no arguments")
		}
		fmt.Fprintln(out, "nova-review devel")
		return 0
	case "packet":
		return packet(args[1:], out, errOut, now)
	case "policy":
		return policy(args[1:], out, errOut, now)
	default:
		return refused(errOut, fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

func refused(w io.Writer, why string) int {
	fmt.Fprintf(w, "nova-review REFUSED: %s; run: nova-review help\n", oneline.Escape(why))
	return 2
}

func selection(fs *flag.FlagSet) (*string, *int, *string) {
	lane := fs.String("lane", "", "")
	pr := fs.Int("pr", 0, "")
	branch := fs.String("branch", "", "")
	return lane, pr, branch
}

func entry(pr int, branch string) (string, error) {
	if (pr > 0) == (strings.TrimSpace(branch) != "") {
		return "", fmt.Errorf("give exactly one of --pr or --branch")
	}
	if pr > 0 {
		return fmt.Sprintf("%d", pr), nil
	}
	return branch, nil
}

func exactHead(head string) error {
	if !merge.IsSHA(head) {
		return fmt.Errorf("--head wants the full 40-character sha the actor chose")
	}
	return nil
}

func packet(args []string, out, errOut io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("packet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lane, pr, branch := selection(fs)
	who := fs.String("who", "", "")
	head := fs.String("head", "", "")
	dest := fs.String("out", "", "")
	if fs.Parse(args) != nil {
		return refused(errOut, "bad packet flags")
	}
	id, e := entry(*pr, *branch)
	if e != nil {
		return refused(errOut, e.Error())
	}
	if *lane == "" || *who == "" || *dest == "" {
		return refused(errOut, "--lane, --who and --out are required")
	}
	if e = exactHead(*head); e != nil {
		return refused(errOut, e.Error())
	}
	g := merge.NewGit(filepath.Join(*lane, merge.RepoDir), 120*time.Second, merge.Exec{})
	actual, e := g.Out("rev-parse", *head+"^{commit}")
	if e != nil {
		return refused(errOut, "--head names no commit in this lane")
	}
	actual = strings.TrimSpace(actual)
	if actual != *head {
		return refused(errOut, "--head did not resolve to the exact revision supplied")
	}
	body := fmt.Sprintf("NOVA-REVIEW PACKET v1\nentry=%s\nwho=%s\nhead=%s\nbuilt=%s\n\nThis first packet pins the revision and its entry. Full delta, rule and finding sections remain a later slice.\n", id, *who, actual, now().UTC().Format(time.RFC3339))
	if e = os.WriteFile(*dest, []byte(body), 0o644); e != nil {
		return refused(errOut, fmt.Sprintf("could not write --out: %v", e))
	}
	fmt.Fprintf(out, "PACKET OK entry=%s id=- head=%s base=- range=- files=0 hunks=0 rules=0 prior=0 open=0 bytes=%d cut=0 reused=false out=%s\n", oneline.Field(id), merge.Short(actual), len(body), oneline.Field(*dest))
	return 0
}

type policyRecord struct {
	Version  int      `json:"version"`
	ID       string   `json:"id"`
	Entry    string   `json:"entry"`
	Who      string   `json:"who"`
	Readers  []string `json:"readers"`
	Reserved []string `json:"reserved"`
	Deadline string   `json:"deadline"`
	Reason   string   `json:"reason"`
	Head     string   `json:"head"`
	At       string   `json:"at"`
	File     string   `json:"file"`
}

func names(v string) []string {
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}
func has(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func policy(args []string, out, errOut io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lane, pr, branch := selection(fs)
	who := fs.String("who", "", "")
	readerText := fs.String("readers", "", "")
	reservedText := fs.String("reserved", "", "")
	deadline := fs.String("deadline", "", "")
	reason := fs.String("reason", "", "")
	head := fs.String("head", "", "")
	if fs.Parse(args) != nil {
		return refused(errOut, "bad policy flags")
	}
	id, e := entry(*pr, *branch)
	if e != nil {
		return refused(errOut, e.Error())
	}
	if *lane == "" || *who == "" || *readerText == "" || *reservedText == "" || *deadline == "" || *reason == "" {
		return refused(errOut, "--lane, --who, --readers, --reserved, --deadline and --reason are required")
	}
	if e = exactHead(*head); e != nil {
		return refused(errOut, e.Error())
	}
	readers, reserved := names(*readerText), names(*reservedText)
	if len(readers) == 0 || len(reserved) == 0 || has(readers, *who) || has(reserved, *who) {
		return refused(errOut, "policy actor may not be a named reader or reserved reader")
	}
	st, e := merge.Load(*lane)
	if e != nil {
		return refused(errOut, fmt.Sprintf("could not read lane: %v", e))
	}
	sub, e := merge.NewSubmission(now())
	if e != nil {
		return refused(errOut, e.Error())
	}
	file := merge.PolicyFile(merge.EntryDirName(id), *who, *head, sub)
	rec := policyRecord{Version: 1, ID: sub.ID(), Entry: id, Who: *who, Readers: readers, Reserved: reserved, Deadline: *deadline, Reason: *reason, Head: *head, At: sub.At, File: file}
	body, e := json.MarshalIndent(rec, "", "  ")
	if e != nil {
		return refused(errOut, e.Error())
	}
	body = append(body, '\n')
	r := merge.NewRecords(*lane, st.LaneBranch, "origin", merge.NewGit(*lane, 120*time.Second, merge.Exec{}), 120*time.Second)
	if e = r.Deliver(sub, []merge.Item{{Part: "policy", Path: file, Body: body}}); e != nil {
		fmt.Fprintf(errOut, "POLICY FAIL entry=%s id=%s file=%s pushed=false: %s; re-run the same verb to push it\n", oneline.Field(id), oneline.Field(sub.ID()), oneline.Field(file), oneline.Escape(e.Error()))
		return 1
	}
	fmt.Fprintf(out, "POLICY OK entry=%s id=%s who=%s readers=%d reserved=%d deadline=%s head=%s file=%s pushed=true\n", oneline.Field(id), oneline.Field(sub.ID()), oneline.Field(*who), len(readers), len(reserved), oneline.Field(*deadline), merge.Short(*head), oneline.Field(file))
	return 0
}
