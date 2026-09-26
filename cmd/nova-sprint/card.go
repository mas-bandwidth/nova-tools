package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// runCardPool runs card cut, push, release, show, bench-side stop, and the
// card model's fsck and ls (card_fsck.go); runCard (card_run.go) routes them here.
func runCardPool(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "card", "needs cut, push, release, show or stop")
	}
	switch args[0] {
	case "cut":
		return cmdCardCut(ctx, args[1:], stdout, stderr)
	case "push":
		return cmdCardPush(ctx, args[1:], stdout, stderr)
	case "release":
		return cmdCardRelease(ctx, args[1:], stdout, stderr)
	case "stop":
		return cmdCardStop(ctx, args[1:], os.Stdin, stdout, stderr)
	case "show":
		return cmdCardShow(ctx, args[1:], stdout, stderr)
	case "fsck":
		return cmdCardFsck(ctx, args[1:], stdout, stderr)
	case "ls":
		return cmdCardLs(ctx, args[1:], stdout, stderr)
	default:
		return refuse(stderr, "card", "unknown subcommand "+args[0]+"; it wants cut, push, release, show, stop, fsck or ls")
	}
}

// cardCutSource is card cut's forge seam; tests replace it.
var cardCutSource card.IssueSource = card.GHIssues{}

// cmdCardCut is nova-tools#3623, the retired pulse cutter's job: one GitHub issue
// becomes one card record in Redis (card.Cut: render, store the body at its
// content address, push with the one card writer), with the S2 context block
// inlined when --index names a ctxindex directory. It refuses an issue with no
// STREAM (and no --stream), an unparsable DEPENDS-ON, and a missing PATHS or
// DONE-WHEN before any write. One receipt line; exit 0 cut, 1 refused with
// the remedy named, 2 usage.
func cmdCardCut(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card cut")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	issue := fs.Int("issue", 0, "the GitHub issue number the card is cut from")
	spec := fs.Int("spec", 0, "the spec issue number the card belongs to")
	index := fs.String("index", "", "a ctxindex directory whose S2 context block is inlined")
	stream := fs.String("stream", "", verbflag.HelpStream)
	base := fs.String("base", "", "the base branch when the issue names none")
	from := fs.String("from", "", verbflag.HelpFrom)
	dryRun := fs.Bool("dry-run", false, verbflag.HelpDryRun)
	noGitHub := fs.Bool("no-github", false, "read nothing from GitHub: the issue text is --from's")
	baseSHA := fs.String("base-sha", "", "the base's sha, 40 hex (default the tip of --base in the mirror)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, "nova-sprint card: cut: "+oneline.Escape(err.Error())+"; run: nova-sprint help")
		return 2
	}
	if *from != "" {
		if *issue != 0 || *spec != 0 || *index != "" || fs.NArg() > 0 {
			fmt.Fprintln(stderr, "nova-sprint card: cut --from takes no --issue, --spec, --index or argument; run: nova-sprint help")
			return 2
		}
		return cmdCardCutFrom(ctx, cutFromOpts{From: *from, Repo: *repo, Stream: *stream, Sprint: *sprint, Base: *base,
			BaseSHA: *baseSHA, Actor: seatActor(), DryRun: *dryRun, NoGitHub: *noGitHub}, *addr, stdout, stderr)
	}
	if *sprint == "" || *addr == "" || *repo == "" || *issue <= 0 || *spec < 0 || fs.NArg() > 0 || *dryRun || *noGitHub || *baseSHA != "" {
		fmt.Fprintln(stderr, "nova-sprint card: cut wants --sprint <S> --repo <owner/name> --issue <n> and --redis <addr> (or NOVA_SPRINT_REDIS), optional --spec <n> --index <ctxindex dir> --stream <name> --base <branch>; or many cards: --from <cards.tsv|-> --repo <owner/name> [--stream <s>] [--sprint <S>] [--base dev] [--base-sha <sha40>] [--actor <a>] [--dry-run] [--no-github]; run: nova-sprint help")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	who := fmt.Sprintf("%s/%s#%d", *sprint, *repo, *issue)
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card cut %s redis=down remedy=%s\n", who, oneline.Field("check --redis or NOVA_SPRINT_REDIS"))
		return 1
	}
	defer st.Close()
	c, res, err := card.Cut(ctx, st.Client(), cardCutSource, card.CutInput{
		Sprint: *sprint, Repo: *repo, Issue: *issue, Spec: *spec, Index: *index, Stream: *stream, Base: *base,
	})
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card cut %s why=%s\n", who, oneline.Field(err.Error()))
		return 1
	}
	if res.Code != 0 {
		fmt.Fprintf(stdout, "REFUSED card cut %s label=%s push=%d why=%s\n", who, c.Label, res.Code, oneline.Field(strings.TrimSpace(res.Stderr)))
		return 1
	}
	place := "-"
	if _, p, ok := strings.Cut(strings.TrimSpace(res.Stdout), "place="); ok {
		place = p
	}
	fmt.Fprintf(stdout, "CARD CUT %s label=%s place=%s stream=%s contexts=%d origin=%s\n", who, c.Label, place,
		oneline.Field(c.Stream), c.Contexts, c.Origin)
	return 0
}

// cmdCardPush pushes every card named (files, every regular file of --dir in
// name order, or --stdin) in one batch: one pipeline of FCALL ns_card_push
// after one pipeline of dependency reads, however many cards (#3266). It
// never loads the function library (nova-sprint fn load is the owner's).
func cmdCardPush(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card push")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	stdin := fs.Bool("stdin", false, "read the card file(s) from stdin")
	dir := fs.String("dir", "", "a directory of card files, pushed in name order")
	mapKind := fs.Bool("map-kind", false, "push a classification KIND as the card's RESULT kind")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" {
		return refuse(stderr, "card", "push needs --sprint <name>, --redis <addr>, and card files (or --dir <cards/>, or --stdin); --map-kind pushes a classification KIND as its RESULT kind")
	}
	sources := 0
	for _, on := range []bool{*stdin, *dir != "", fs.NArg() > 0} {
		if on {
			sources++
		}
	}
	if sources > 1 {
		return refuse(stderr, "card", "push reads card files, --dir or --stdin, one of them")
	}
	if sources == 0 {
		return refuse(stderr, "card", "push needs card files, --dir <cards/>, or --stdin")
	}
	files, err := readCardFiles(*stdin, *dir, fs.Args())
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	first := 0
	for _, res := range card.PushBatch(ctx, client, *sprint, files, card.PushOptions{MapKind: *mapKind}) {
		if wrote := writeCardResult(stdout, stderr, res); wrote != 0 && first == 0 {
			first = wrote
		}
	}
	return first
}

// readCardFiles reads the batch: stdin, every regular non-dot file of dir in
// name order, or the named files.
func readCardFiles(stdin bool, dir string, paths []string) ([]card.CardFile, error) {
	if stdin {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("cannot read stdin: %v", err)
		}
		return []card.CardFile{{Name: "stdin", Body: body}}, nil
	}
	if dir != "" {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("cannot read --dir %s: %v", dir, err)
		}
		for _, e := range ents {
			if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("--dir %s holds no card file", dir)
		}
		sort.Strings(paths)
	}
	files := make([]card.CardFile, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %v", path, err)
		}
		files = append(files, card.CardFile{Name: path, Body: body})
	}
	return files, nil
}

func cmdCardRelease(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card release")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "card", "release needs --sprint <name> and --redis <addr>, and no card file")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	return writeCardResult(stdout, stderr, card.Release(ctx, client, *sprint))
}

func openCardRedis(ctx context.Context, addr string, stderr io.Writer) (*redis.Client, int) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, refuse(stderr, "card", err.Error())
	}
	return st.Client(), 0
}

func writeCardResult(stdout, stderr io.Writer, res card.VerbResult) int {
	if res.Code != 0 {
		fmt.Fprintf(stderr, "nova-sprint card: %s; run: nova-sprint help\n", oneline.Escape(strings.TrimSpace(res.Stderr)))
		return res.Code
	}
	if _, err := io.WriteString(stdout, res.Stdout); err != nil {
		return refuse(stderr, "card", err.Error())
	}
	return 0
}

func cmdCardStop(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := verbflag.New("card stop")
	fromStdin := fs.Bool("stdin", false, "read the card file(s) from stdin")
	grace := fs.Duration("grace", launch.DefaultStopGrace, "how long a stopped attempt gets to exit before it is killed")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "card stop", err.Error())
	}
	if !*fromStdin || fs.NArg() != 0 {
		return refuse(stderr, "card stop", "wants --stdin [--grace <duration>]")
	}
	if *grace <= 0 {
		return refuse(stderr, "card stop", "--grace must be positive")
	}
	// Exit 0 whenever the protocol completed, ALIVE included: ALIVE is data the
	// reset holds on, and a non-zero exit tells the reset the session failed.
	if err := launch.StopCommand(ctx, stdin, stdout, stderr, *grace, launch.OSGroupManager{}, nil); err != nil {
		return refuse(stderr, "card stop", err.Error())
	}
	return 0
}

// cmdCardShow prints one card's Redis record (#3689, Glenn 2026-09-24 11:55
// PM: "it should be in REDIS, not on files"): the card hash (never its token)
// and its current attempt's result hash -- the wrapper-written RESULT fields,
// the model's two lines and note, the check run, outcome, wall, commit and the
// provider facts -- one `card.<field> <value>` or `result.<field> <value>` line
// each, sorted, values on one line, then one receipt line. One FCALL
// (ns_card_show). Exit 0 shown, 1 no such card, 2 usage, 6 Redis.
func cmdCardShow(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card show")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	err := fs.Parse(args)
	label := oneID(*ids)
	if err != nil || *sprint == "" || label == "" || *addr == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "nova-sprint card: show wants --sprint <S> --ids <label> and --redis <addr> (or NOVA_SPRINT_REDIS); run: nova-sprint help")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s redis=down remedy=%s\n", *sprint, label, oneline.Field("check --redis or NOVA_SPRINT_REDIS"))
		return 6
	}
	defer st.Close()
	cardFields, resultFields, err := card.ShowRecord(ctx, st.Client(), *sprint, label)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s why=%s\n", *sprint, label, oneline.Field(err.Error()))
		return 6
	}
	if len(cardFields) == 0 {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s why=%s\n", *sprint, label, oneline.Field("no such card: s:"+*sprint+":card:"+label+" is empty"))
		return 1
	}
	for _, line := range card.ShowLines(cardFields, resultFields) {
		fmt.Fprintln(stdout, line)
	}
	// The receipt names attempt, retries, reason and why (#3700), so a card
	// the dealer keeps redealing shows why in one line; why is last and
	// verbatim (escaped onto the line), the refusal as the bench printed it.
	fmt.Fprintf(stdout, "SHOWN card %s/%s state=%s attempt=%s outcome=%s valid=%s retries=%s reason=%s fields=%d why=%s\n", *sprint, label,
		orDash(cardFields["state"]), orDash(cardFields["attempt"]), orDash(cardFields["outcome"]), orDash(resultFields["valid"]),
		orDash(cardFields["retries"]), orDash(cardFields["reason"]), len(cardFields)+len(resultFields), oneline.Escape(orDash(cardFields["why"])))
	return 0
}
