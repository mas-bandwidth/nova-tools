package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A reader row's tiers (docs/SPEC-SPRINT.md section 6; internal/sprint
// reader_tiers.go): reader add --tiers, reader set --tiers, the OK line both
// print, and the line where --all prints under the readers table.

// readerTiersUsage is the --tiers flag's words, for reader add and reader set.
var readerTiersUsage = "the tiers the reader reads, a comma list of " + strings.Join(sprint.ReaderTierNames, ", ") + ": the ask asks it a read only of a card whose read tier it names (reader add's default: every tier)"

// cmdReaderSet sets the tiers the named readers read (--tiers, required), all
// or none: a named reader with no row refuses the whole call, and nothing is
// written. The next tick's ask asks each a read only of a card whose read tier
// it names; a read it holds already stays (a returned one is never asked again
// in its place: docs/SPEC-SPRINT.md section 6).
func (a *app) cmdReaderSet(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("reader set")
	tiersFlag := fs.String("tiers", "", readerTiersUsage+" (required)")
	names, code := readerNames("reader set", args, stderr, fs)
	if code != 0 {
		return code
	}
	if *tiersFlag == "" {
		return refuse(stderr, "reader set", "wants --tiers <tier>[,<tier>...] ("+strings.Join(sprint.ReaderTierNames, ", ")+")")
	}
	tiers, err := sprint.ParseReaderTiers(*tiersFlag)
	if err != nil {
		return refuse(stderr, "reader set", err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "reader set", err.Error())
	}
	ctx := context.Background()
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return a.readFailed("reader set", err, stderr)
	}
	if bad := unknownReaders(rows, names); len(bad) > 0 {
		fmt.Fprintf(stderr, "%s reader set: no reader %s on the readers table (readers: %s); nothing was changed; run: nova-sprint reader add <name> --tiers %s\n", prog, strings.Join(bad, ","), strings.Join(rows, ","), strings.Join(tiers, ","))
		return 1
	}
	for _, n := range names {
		if err := st.SetReaderTiers(ctx, n, tiers); err != nil {
			fmt.Fprintf(stderr, "%s reader set: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
	}
	return a.sayReaderTiers(ctx, st, c, "reader set", names, stdout, stderr)
}

// sayReaderTiers is reader add's and reader set's OK line: the readers and the
// tiers their rows now read, read back from the store (tiers=<list> when every
// named reader reads the same, else tiers=<reader>:<list>;...).
func (a *app) sayReaderTiers(ctx context.Context, st *store.Store, c *common, verbName string, names []string, stdout, stderr io.Writer) int {
	m, err := st.ReaderTiers(ctx, names)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	s := &sprint.Snapshot{ReaderTiers: m}
	each := map[string][]string{}
	var words []string
	same := true
	for _, n := range names {
		each[n] = s.ReaderTiersOf(n)
		words = append(words, n+":"+strings.Join(each[n], ","))
		same = same && slices.Equal(each[n], each[names[0]])
	}
	word := strings.Join(words, ";")
	if same {
		word = strings.Join(each[names[0]], ",")
	}
	sayOK(stdout, c.json, verbName, token(verbName)+" OK readers="+strings.Join(names, ",")+" tiers="+word, map[string]any{"readers": names, "tiers": each})
	return 0
}

// readerTiersLine is the line where --all prints under the readers table,
// naming every reader of some tiers only and the tiers it reads ("" when
// every reader reads every tier).
func readerTiersLine(ctx context.Context, st *store.Store, readers []string) (string, error) {
	m, err := st.ReaderTiers(ctx, readers)
	if err != nil || len(m) == 0 {
		return "", err
	}
	s := &sprint.Snapshot{ReaderTiers: m}
	var some []string
	for _, r := range readers {
		if len(m[r]) > 0 {
			some = append(some, r+" reads "+s.ReaderTiersText(r))
		}
	}
	return "tiers: " + strings.Join(some, "; ") + "\n", nil
}
