package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// THE DECIDE READ (docs/SPEC-SPRINT.md section 6; the owner, 2026-10-02: "the nova-decide
// is both sides"). The first read of a flash card carries the sprint row's two bars in its
// frame. Native, once the checkout is staged and before any child, asks nova-decide's read
// decision over the card and the work's diff (start..HEAD) and routes the read by its
// p(defect): at or above the bounce bar the read is broken with the decision as its finding,
// below the review bar it is ok, and no child runs for either; between the two the strings
// read runs as before, and its verdict is attached to the decision as its outcome. Every
// decision is recorded in the machine's record, <root>/decide/read.jsonl, under the read
// card's id at the head it read. A decide read that cannot be made (no key, a backend that
// fails, bars it cannot read) is said on one NATIVE NOTE line and the strings read runs.

// decider is a decide read's backend and the clock its record is stamped by.
type decider struct {
	backend decide.Backend
	now     func() time.Time
}

// deciderOf is the run's decider: the config's (a test's), else Jev over its real
// transport with the key JEV_API_KEY holds, which the member hands native and native never
// hands its child (nativeChildEnv), and the wall clock; nil with no key.
func deciderOf(cfg nativeRunConfig) *decider {
	if cfg.decider != nil {
		return cfg.decider
	}
	key := os.Getenv(decide.JevSecret)
	if key == "" {
		return nil
	}
	return &decider{backend: decide.JevHTTP(key, decide.JevTimeout), now: time.Now}
}

// decideWait bounds the backend's answer, as nova-decide's own --timeout default does.
const decideWait = time.Minute

// decideRecord is the machine's record of decide reads.
func decideRecord(root string) string { return filepath.Join(root, "decide", "read.jsonl") }

// decideOp is a decide read's id in the record: the read card at the head it reads (a card id
// comes back after a clear; a head does not).
func decideOp(card, head string) string { return card + "@" + head[:min(12, len(head))] }

// nativeDecide makes a framed read's decide read, when its frame carries bars: the route its
// p(defect) takes (decide.RouteBounce or RouteLand: RESULT.md is written in the job, the
// read's verdict; RouteStrings: the strings read follows) and the decision's op id; "" when
// the frame names no bars or the decision could not be made (said on errOut).
func nativeDecide(cfg nativeRunConfig, jobDir, start, head string, out, errOut io.Writer) (route, op string) {
	fr := cfg.frame
	if fr == nil || fr.Kind != "read" || fr.DecideBounce == "" && fr.DecideReview == "" {
		return "", ""
	}
	note := func(why string) (string, string) {
		fmt.Fprintf(errOut, "NATIVE NOTE: %s no decide read: %s; the strings read runs\n", oneline.Field(cfg.label), oneline.Escape(why))
		return "", ""
	}
	bars, err := decide.ParseBars(fr.DecideBounce, fr.DecideReview)
	if err != nil {
		return note(err.Error())
	}
	if start == "" || head == "" {
		return note("the work's start or head is unknown, so its diff is")
	}
	dc := deciderOf(cfg)
	if dc == nil {
		return note(decide.JevSecret + " is absent from this environment (the reader loop's nova-secrets keys)")
	}
	diff, err := gitrun.Output(context.Background(), gitrun.Options{C: filepath.Join(jobDir, swarm.JobRepo), OwnRepo: true},
		"diff", "-M", "--no-color", "--end-of-options", start, "HEAD")
	if err != nil {
		return note("the work's diff could not be read: " + err.Error())
	}
	var files []string
	for _, f := range diffcheck.Parse(diff) {
		files = append(files, f.New)
	}
	if err := os.MkdirAll(filepath.Dir(decideRecord(cfg.root)), 0o755); err != nil {
		return note("the record's directory: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), decideWait)
	defer cancel()
	op = decideOp(fr.Card, head)
	d, route, err := decide.FirstRead(ctx, dc.backend, bars, member.BriefOf(string(cfg.card)), diff, decideRecord(cfg.root), op, dc.now())
	if err != nil {
		return note(err.Error())
	}
	finding := decide.Finding(d, bars, files)
	fmt.Fprintf(out, "DECIDE %s op=%s route=%s p_defect=%.2f\n", oneline.Field(cfg.label), oneline.Field(op), oneline.Field(route), decide.PDefect(d))
	if route == decide.RouteStrings {
		return route, op
	}
	verdict := map[string]string{decide.RouteBounce: "broken", decide.RouteLand: "ok"}[route]
	result := fmt.Sprintf("head: %s\nbranch: %s\nverdict: %s\ngate: none (a decide read runs no gate)\noutput: -\nreport: %s\n",
		oneline.Field(head), oneline.Field(orElse(fr.Branch, "-")), oneline.Field(verdict), oneline.Escape(finding))
	if err := atomicfile.Write(swarm.ResultPath(jobDir), []byte(result), 0o644); err != nil {
		return note("RESULT.md could not be written: " + err.Error())
	}
	return route, op
}

// publishDecided puts a decided read's RESULT.md where the member reads it: the run's first
// attempt directory under the results root (nativeResultsAttemptDir), as a launch's is.
func publishDecided(cfg nativeRunConfig, jobDir string, errOut io.Writer) string {
	if cfg.resultsRoot == "" {
		return ""
	}
	id, err := claimNativeResultsRun(cfg)
	if err == nil {
		cfg.runID = id
		dir := nativeResultsAttemptDir(cfg, 1)
		if err = os.MkdirAll(dir, 0o755); err == nil {
			if err = copyRegularFile(swarm.ResultPath(jobDir), filepath.Join(dir, "RESULT.md")); err == nil {
				return dir
			}
		}
	}
	fmt.Fprintf(errOut, "NATIVE NOTE: the decide read's RESULT.md could not be published under %s: %s\n", oneline.Field(cfg.resultsRoot), oneline.Escape(err.Error()))
	return ""
}

// settleDecided attaches the strings read's verdict to the decide read it followed, as the
// decision's outcome (decide.Settle): ok is LAND, broken is BOUNCE, and a run with no verdict
// attaches nothing. The test's decider's clock stamps it, else the wall clock.
func settleDecided(cfg nativeRunConfig, jobDir, op string, errOut io.Writer) {
	now := time.Now
	if cfg.decider != nil {
		now = cfg.decider.now
	}
	path, ok := swarm.FindCardResult(jobDir)
	if !ok {
		return
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		cr := typedrec.ParseCardResult(raw)
		err = decide.Settle(decideRecord(cfg.root), op, cr.Verdict, oneline.Cap("strings read "+cfg.label+": "+strings.TrimSpace(cr.Report), 300), now())
	}
	if err != nil {
		fmt.Fprintf(errOut, "NATIVE NOTE: the strings read's verdict was not attached to the decide read %s: %s\n", oneline.Field(op), oneline.Escape(err.Error()))
	}
}
