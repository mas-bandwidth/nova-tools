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
)

// VerbSummary is the verb's line on nova-sprint help.
const VerbSummary = "fold <S> --store <host:port> --work <nova-work checkout> [--path <rel>] [--as <actor>] [--calib <set.jsonl> --prompt-sha <sha> --jev-eval <cmd> [--candidate <sha>]] [--tools <dir of nova-* binaries at dev> --repo <nova-tools clone at dev> --receipts <dogfood receipts dir>]: landed, done, useful, $ per useful and per landed per route, Jev calibration per work type, one nova-work commit, then verbs unused and its check"

// Main is `nova-sprint fold <S> --store <host:port> --work <dir>`. Exit 0 the
// sprint is folded (now or before), 1 an outcome is unknown (a card with no end
// record or a PR with no state; nothing committed), 2 refused or could not run,
// 3 folded but the --candidate Jev prompt was refused (the current prompt stays),
// 4 folded and the verbs check failed, 5 folded but the verbs step could not
// run (flags missing, or verbs unused refused or fenced). When more than one
// applies every line prints and the exit is 3, else 5, else 4 (#3160).
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var sprint string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sprint, args = args[0], args[1:]
	}
	fs := verbflag.New("fold")
	addr := fs.String("store", "", "")
	work := fs.String("work", "", "")
	path := fs.String("path", "", "")
	actor := fs.String("as", "nova-sprint", "")
	calibPath := fs.String("calib", "", "")
	promptSHA := fs.String("prompt-sha", "", "")
	candidate := fs.String("candidate", "", "")
	jevEval := fs.String("jev-eval", "", "")
	tools := fs.String("tools", "", "")
	repo := fs.String("repo", "", "")
	receipts := fs.String("receipts", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, err.Error()+"; it wants fold <S> --store <host:port> --work <nova-work checkout>")
	}
	var problems []string
	if sprint == "" {
		problems = append(problems, "name the sprint first: fold <S>")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "one sprint per fold; extra arguments "+strings.Join(fs.Args(), " "))
	}
	if *addr == "" {
		problems = append(problems, "--store <host:port> is the sprint's Redis")
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
			"nova-sprint verbs unused --store %s --tools %s --repo %s --receipts %s",
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
