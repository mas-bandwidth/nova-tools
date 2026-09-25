package main

import (
	"context"
	"flag"
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
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// runCardPool runs card push, release, show, bench-side stop, and the card
// model's fsck and ls (card_fsck.go); runCard (card_run.go) routes them here.
func runCardPool(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "card", "needs push, release, show or stop")
	}
	switch args[0] {
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
		return refuse(stderr, "card", "unknown subcommand "+args[0]+"; it wants push, release, show, stop, fsck or ls")
	}
}

// cmdCardPush pushes every card named (files, every regular file of --dir in
// name order, or --stdin) in one batch: one pipeline of FCALL ns_card_push
// after one pipeline of dependency reads, however many cards (#3266). It
// never loads the function library (nova-sprint fn load is the owner's).
func cmdCardPush(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	stdin := fs.Bool("stdin", false, "")
	dir := fs.String("dir", "", "")
	mapKind := fs.Bool("map-kind", false, "")
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
	fs := flag.NewFlagSet("card release", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
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
	fs := flag.NewFlagSet("card stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromStdin := fs.Bool("stdin", false, "")
	grace := fs.Duration("grace", launch.DefaultStopGrace, "")
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
	fs := flag.NewFlagSet("card show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	label := fs.String("label", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *label == "" || *addr == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "nova-sprint card: show wants --sprint <S> --label <label> and --redis <addr> (or NOVA_SPRINT_REDIS); run: nova-sprint help")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s redis=down remedy=%s\n", *sprint, *label, oneline.Field("check --redis or NOVA_SPRINT_REDIS"))
		return 6
	}
	defer st.Close()
	cardFields, resultFields, err := card.ShowRecord(ctx, st.Client(), *sprint, *label)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s why=%s\n", *sprint, *label, oneline.Field(err.Error()))
		return 6
	}
	if len(cardFields) == 0 {
		fmt.Fprintf(stdout, "REFUSED card show %s/%s why=%s\n", *sprint, *label, oneline.Field("no such card: s:"+*sprint+":card:"+*label+" is empty"))
		return 1
	}
	for _, line := range card.ShowLines(cardFields, resultFields) {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "SHOWN card %s/%s state=%s attempt=%s outcome=%s valid=%s fields=%d\n", *sprint, *label,
		orDash(cardFields["state"]), orDash(cardFields["attempt"]), orDash(cardFields["outcome"]), orDash(resultFields["valid"]), len(cardFields)+len(resultFields))
	return 0
}
