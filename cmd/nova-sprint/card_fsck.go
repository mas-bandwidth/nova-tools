package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// ONE PLACE (nova-tools#3692): card fsck, card ls --unplaced and bench
// reindex read and check the card model of
// internal/nsprint/fn/lua/02_card_move.lua (the one move primitive).

// cmdCardFsck: card fsck --sprint <S> --redis <addr> [--repair]. One
// function call walks the sprint's cards both ways and one more walks every
// table set's members (a member that is not the id of an existing record is
// MEMBER-NOT-A-CARD, #4054; --repair removes it with a ws:log receipt); exit
// 0 clean (or every drift repaired), 1 drift left (the remedy is --repair),
// 2 usage or Redis.
func cmdCardFsck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card fsck")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	repair := fs.Bool("repair", false, "repair every drift found")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "card", "fsck needs --sprint <name> and --redis <addr> [--repair]")
	}
	return runFsck(ctx, *addr, *sprint, *repair, "CARD FSCK", "card fsck --sprint "+*sprint+" --repair", stdout, stderr)
}

// runBenchReindex: bench reindex --sprint <S> --redis <addr>. The one-time
// rebuild: every record the sprint's state indexes, waiting set and pool name
// is adopted into sprint:<S>:cards with its one place and every view,
// including bench:<b>:cards:*, and every stray link is removed.
func runBenchReindex(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("bench reindex")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "bench", "reindex needs --sprint <name> and --redis <addr>")
	}
	return runFsck(ctx, *addr, *sprint, true, "BENCH REINDEX", "card fsck --sprint "+*sprint, stdout, stderr)
}

func runFsck(ctx context.Context, addr, sprint string, repair bool, verb, remedy string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	rep, err := card.Fsck(ctx, client, sprint, repair)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	mem, err := card.Members(ctx, client, repair, "card fsck")
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	for _, l := range rep.Lines {
		fmt.Fprintf(stderr, "DRIFT %s\n", oneline.Escape(l))
	}
	for _, l := range mem.Lines {
		fmt.Fprintf(stderr, "DRIFT %s\n", oneline.Escape(l))
	}
	orphans := fsckOrphans(ctx, client, stdout, stderr)
	nomirror := fsckNoMirror(ctx, client, stdout, stderr)
	fmt.Fprintf(stdout, "%s notacard=%d removed=%d orphansets=%s nomirror=%s\n", rep.Line(verb), mem.Bad, mem.Removed, orphans, nomirror)
	if !rep.Clean() || !mem.Clean() {
		fmt.Fprintf(stderr, "nova-sprint card: %d drift left; run: nova-sprint %s\n", rep.Drift-rep.Fixed+mem.Bad-mem.Removed, remedy)
		return 1
	}
	return 0
}

// fsckOrphans prints one ORPHAN-SET key=<k> members=<n> line per ws set
// whose members all belong to no card of any sprint in sprint:order and
// returns the receipt's orphansets count, or "?" when the walk failed. It is
// reported, never repaired (#4334 owns purging), and never changes the exit
// code.
func fsckOrphans(ctx context.Context, client redis.UniversalClient, stdout, stderr io.Writer) string {
	rep, err := card.OrphanSets(ctx, client)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint card: ws orphan sets not read: %s\n", oneline.Escape(err.Error()))
		return "?"
	}
	for _, l := range rep.Lines {
		fmt.Fprintln(stdout, oneline.Escape(l))
	}
	return strconv.FormatInt(rep.Orphans, 10)
}

// fsckNoMirror prints one NOMIRROR line per registered bench whose
// ci:nomirror:<bench> set is non-empty (#3804: a bench that lacks a repo's
// mirror is skipped by every CI claim of that repo, so it must be visible)
// and returns the receipt's nomirror count: the benches named, or "?" when
// the sets could not be read. It is a bench defect, not card drift: it
// never changes the exit code.
func fsckNoMirror(ctx context.Context, client redis.UniversalClient, stdout, stderr io.Writer) string {
	marked, err := ci.NoMirror(ctx, client)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint card: ci:nomirror not read: %s\n", oneline.Escape(err.Error()))
		return "?"
	}
	benches := make([]string, 0, len(marked))
	for b := range marked {
		benches = append(benches, b)
	}
	sort.Strings(benches)
	for _, b := range benches {
		fmt.Fprintf(stdout, "NOMIRROR bench=%s repos=%s\n", b, strings.Join(marked[b], ","))
	}
	return strconv.Itoa(len(benches))
}

// cmdCardLs: card ls --unplaced --sprint <S> --redis <addr>: the null cards
// (where empty, in no table set), oldest first, then the receipt line.
func cmdCardLs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("card ls")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	addr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	unplaced := fs.Bool("unplaced", false, "list only the cards no table set holds")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || !*unplaced || fs.NArg() > 0 {
		return refuse(stderr, "card", "ls needs --unplaced --sprint <name> --redis <addr>")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	ids, err := card.Unplaced(ctx, client, *sprint)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	if len(ids) > 0 {
		fmt.Fprintln(stdout, strings.Join(ids, "\n"))
	}
	fmt.Fprintf(stdout, "CARD LS sprint=%s unplaced=%d\n", *sprint, len(ids))
	return 0
}
