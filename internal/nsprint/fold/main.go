package fold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// VerbSummary is the verb's line on nova-sprint help.
const VerbSummary = "fold --sprint <S> --redis <host:port> --work <nova-work checkout> [--path <rel>] [--as <actor>] [--calib <set.jsonl> --prompt-sha <sha> --jev-eval <cmd> [--candidate <sha>]] [--tools <dir of nova-* binaries at dev> --repo <nova-tools clone at dev> --receipts <dogfood receipts dir>]: landed, done, useful, $ per useful and per landed per route, Jev calibration per work type, one nova-work commit, then verbs unused and its check"

// Main is `nova-sprint fold --sprint <S> --redis <host:port> --work <dir>`. Exit 0 the
// sprint is folded (now or before), 1 an outcome is unknown (a card with no end
// record or a PR with no state; nothing committed), 2 refused or could not run,
// 3 folded but the --candidate Jev prompt was refused (the current prompt stays),
// 4 folded and the verbs check failed, 5 folded but the verbs step could not
// run (flags missing, or verbs unused refused or fenced). When more than one
// applies every line prints and the exit is 3, else 5, else 4 (#3160).
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("fold")
	sprintFlag := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", seatcred.Addr(), verbflag.HelpRedis)
	work := fs.String("work", "", "the nova-work checkout the fold commits into")
	path := fs.String("path", "", "the path inside the nova-work checkout the fold writes under")
	actor := fs.String("as", "nova-sprint", verbflag.HelpAs)
	calibPath := fs.String("calib", "", "the Jev calibration set, a jsonl file")
	promptSHA := fs.String("prompt-sha", "", "the sha of the current Jev prompt")
	candidate := fs.String("candidate", "", "the sha of a candidate Jev prompt to score against the current one")
	jevEval := fs.String("jev-eval", "", "the command that runs the Jev evaluation")
	tools := fs.String("tools", "", "a directory of nova-* binaries built at dev (the verbs step)")
	repo := fs.String("repo", "", verbflag.HelpRepo)
	receipts := fs.String("receipts", "", "the dogfood receipts directory (the verbs step)")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, err.Error()+"; it wants fold --sprint <S> --redis <host:port> --work <nova-work checkout>")
	}
	sprint := *sprintFlag
	var problems []string
	if sprint == "" {
		problems = append(problems, "name the sprint: fold --sprint <S>")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments: "+strings.Join(fs.Args(), " "))
	}
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is the sprint's Redis")
	}
	if *work == "" {
		problems = append(problems, "--work names the nova-work checkout the fold commits into")
	}
	calibrating := *calibPath != "" || *promptSHA != "" || *candidate != "" || *jevEval != ""
	if calibrating && (*calibPath == "" || *promptSHA == "" || *jevEval == "") {
		problems = append(problems, "the Jev calibration needs --calib <set.jsonl>, --prompt-sha <current> and --jev-eval <cmd> together")
	}
	verbing := *tools != "" || *repo != "" || *receipts != ""
	if verbing && (*tools == "" || *repo == "" || *receipts == "") {
		problems = append(problems, "the verbs step needs --tools <dir>, --repo <nova-tools clone> and --receipts <dir> together")
	}
	if len(problems) > 0 {
		return refuse(stderr, strings.Join(problems, "; "))
	}
	var vc *verbs.Config
	if verbing {
		vc = &verbs.Config{Tools: *tools, Repo: *repo, Receipts: *receipts, Days: verbs.DefaultDays}
	}
	var jc *JevCalib
	if calibrating {
		f, err := os.Open(*calibPath)
		if err != nil {
			return refuse(stderr, err.Error())
		}
		set, err := ReadCalibSet(f)
		f.Close()
		if err != nil {
			return refuse(stderr, *calibPath+": "+err.Error())
		}
		jc = &JevCalib{Set: set, PromptSHA: *promptSHA, Candidate: *candidate, Scorer: ExecScorer{Argv: strings.Fields(*jevEval)}}
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	defer st.Close()
	res, err := Run(ctx, st.Client(), Options{Sprint: sprint, Work: *work, Path: *path, Actor: *actor, Jev: jc, Verbs: vc}, stdout)
	if err != nil {
		var unknown *UnknownError
		if errors.As(err, &unknown) {
			fmt.Fprintf(stderr, "nova-sprint fold: %s\n", oneline.Escape(err.Error()))
			return 1
		}
		return refuse(stderr, err.Error())
	}
	code := 0
	switch res.Verbs {
	case VerbsFailed:
		fmt.Fprintf(stderr, "nova-sprint fold: folded; the verbs check failed (the FOLD VERBS line says why)\n")
		code = 4
	case VerbsNotRun:
		or := func(v, placeholder string) string {
			if v == "" {
				return placeholder
			}
			return v
		}
		fmt.Fprintf(stderr, "nova-sprint fold: folded; the verbs step did not run; run: %s\n", oneline.Escape(fmt.Sprintf(
			"nova-sprint verbs unused --redis %s --tools %s --repo %s --receipts %s",
			*addr, or(*tools, "<dir of nova-* binaries at dev>"), or(*repo, "<nova-tools clone at dev>"), or(*receipts, "<dogfood receipts dir>"))))
		code = 5
	}
	if res.PromptRefused != "" {
		fmt.Fprintf(stderr, "nova-sprint fold: folded; Jev prompt %s refused, %s stays: %s\n", *candidate, *promptSHA, oneline.Escape(res.PromptRefused))
		code = 3
	}
	return code
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-sprint fold: %s; run: nova-sprint help\n", oneline.Escape(what))
	return 2
}
