package main

// push is the ready set's WRITER. `nova-swarm pull --stream <kind> --bench <name>` has read
// `nova:queue:<kind>:<lane>` since integration-11a (nova-tools#1436) and nothing has ever
// written to it: the queue shipped with a consumer and no producer, and every card reached a
// bench by a directory or by hand. This is nova-tools#1142 and the landable remainder of
// PR #1269, whose own Redis layer `dev` overtook with a different one.
//
// It writes through internal/redisq, the same package pull reads through, in the same two
// modes pull has: a Redis address, or the directory fallback. One mode per call and never
// both -- a card granted by two stores is a card two benches take.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

// pushLanes is pullLanes, and it is duplicated here deliberately rather than exported from
// cmd/nova-swarm, which is another main package. The rule it enforces is the reason it is
// worth the duplication: `nova-swarm pull` reads red, then green, then small, then next, and
// ONLY those four. A card pushed to a fifth lane is written, acknowledged and never read by
// anybody, which is the worst shape a queue can have -- it looks like it worked.
var pushLanes = []string{"red", "green", "small", "next"}

// pushLabelRe is what may name a card. The label reaches a slot directory name on a bench, so
// a label with a slash or a space is a path nobody meant to write.
var pushLabelRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// cmdPush writes one card into the ready set. Every path and every name comes from a flag:
// there is no default stream, no default lane and no discovery.
func cmdPush(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	stream := fs.String("stream", "", "")
	lane := fs.String("lane", "", "")
	card := fs.String("card", "", "")
	redisAddr := fs.String("redis", "", "")
	dir := fs.String("dir", "", "")
	priority := fs.Int("priority", 0, "")
	needs := fs.String("needs", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " push", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " push", fmt.Sprintf("unexpected argument %q (flags come before arguments)", fs.Arg(0)))
	}
	// Every problem this run can see is named in ONE run rather than one per rerun: a
	// coordinator pushing a batch by hand finds out about the lane AND the missing address
	// at once, not on the second try.
	var problems []string
	want := func(value, name, wants string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", oneline.Escape(name), oneline.Escape(wants)))
		}
	}
	want(*stream, "stream", "the card kind, which is the middle of nova:queue:<kind>:<lane>")
	want(*lane, "lane", "one of "+strings.Join(pushLanes, ", ")+", which is the read order nova-swarm pull walks")
	want(*card, "card", "a FILE holding the card text, whose first line is its RESULT: line")
	mode := redisq.ChooseMode(*redisAddr)
	if mode == redisq.ModeDirectory && strings.TrimSpace(*dir) == "" {
		problems = append(problems, "--redis or --dir is required; it wants where the queue lives: the Redis address the benches read, or the directory they fall back to")
	}
	if mode == redisq.ModeRedis && strings.TrimSpace(*dir) != "" {
		problems = append(problems, "--redis and --dir together name two stores; one mode per call, and a card written to both is a card two benches take")
	}
	if ln := strings.TrimSpace(*lane); ln != "" && !laneIsRead(ln) {
		problems = append(problems, fmt.Sprintf("--lane %s is read by nobody; nova-swarm pull walks %s, so a card in any other lane is written, acknowledged and never taken",
			oneline.Field(ln), oneline.Escape(strings.Join(pushLanes, ", "))))
	}
	if *priority < 0 {
		problems = append(problems, fmt.Sprintf("--priority is 0 or more, got %d; a negative priority is a typo with two readings", *priority))
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "nova-work push: %s\n", oneline.Escape(p))
		}
		return 2
	}

	body, err := os.ReadFile(*card)
	if err != nil {
		return refuse(stderr, " push", "--card wants a readable file: "+oneline.Err(err))
	}
	label := strings.TrimSuffix(filepath.Base(*card), filepath.Ext(*card))
	if !pushLabelRe.MatchString(label) {
		return refuse(stderr, " push", fmt.Sprintf("the label %s is not [A-Za-z0-9._-]+, so it cannot name a slot directory on a bench", oneline.Field(label)))
	}
	// The first line is the contract every worker card in this fleet opens with, and a card
	// that does not have one is a file somebody pushed by mistake. It is refused HERE, at the
	// one write, rather than on the bench that takes it twenty minutes later.
	if first := firstCardLine(body); !strings.HasPrefix(first, "RESULT:") {
		return refuse(stderr, " push", fmt.Sprintf("the card %s does not open with a RESULT: line; its first line is %s",
			oneline.Field(label), oneline.Field(oneline.Cap(first, oneline.TailBytes))))
	}

	name := "nova:queue:" + strings.TrimSpace(*stream) + ":" + strings.TrimSpace(*lane)
	fields := map[string]string{
		"card":      label,
		"body":      string(body),
		"priority":  strconv.Itoa(*priority),
		"needs":     needsOrNone(*needs),
		"pushed-at": now().UTC().Format(time.RFC3339),
	}

	if mode == redisq.ModeDirectory {
		// The id is minted HERE and printed, rather than taking DirQueue.Add's return:
		// Add answers with the PATH it wrote, and `nova-swarm pull` prints the card's ID.
		// A PUSH line whose card= is a path and a PULL line whose card= is an id cannot be
		// joined by anything reading both, which is the one job those two lines have.
		id := newCardID()
		if _, err := (&redisq.DirQueue{Root: *dir}).Add(name, id, fields); err != nil {
			return refuse(stderr, " push", oneline.Err(err))
		}
		fmt.Fprintf(stdout, "PUSH stream=%s card=%s label=%s\n",
			oneline.Field(name), oneline.Field(id), oneline.Field(label))
		return 0
	}

	q, err := redisq.Open(*redisAddr)
	if err != nil {
		return refuse(stderr, " push", oneline.Err(err))
	}
	defer q.Close()
	ctx := context.Background()
	// The group is made HERE as well as in pull, and by the same call, because whichever of
	// the two runs first must leave a stream a group can read. A card added to a stream with
	// no group is invisible to XREADGROUP's `>` until somebody makes one from `$`, and by
	// then it is behind the cursor: pushed, never delivered, never missed.
	if err := q.EnsureGroup(ctx, name); err != nil {
		return refuse(stderr, " push", oneline.Err(err))
	}
	id, err := q.Add(ctx, name, fields)
	if err != nil {
		return refuse(stderr, " push", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PUSH stream=%s card=%s label=%s\n",
		oneline.Field(name), oneline.Field(id), oneline.Field(label))
	return 0
}

// laneIsRead reports whether nova-swarm pull walks this lane.
func laneIsRead(lane string) bool {
	for _, ln := range pushLanes {
		if ln == lane {
			return true
		}
	}
	return false
}

// firstCardLine is the card's first line with a trailing CR removed, so a card written on
// Windows is not refused for its line ending.
func firstCardLine(body []byte) string {
	line, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimRight(line, "\r")
}

// needsOrNone renders an empty --needs as `-`, so every card in the stream carries the same
// field set and a reader never has to tell an absent field from an empty one.
func needsOrNone(needs string) string {
	if s := strings.TrimSpace(needs); s != "" {
		return s
	}
	return "-"
}

// newCardID is the id the directory mode needs and Redis mints for itself: random hex, so
// two pushes of one label are two cards rather than one overwriting the other.
func newCardID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "card-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}

// cmdPushNow is the legacyVerbs entry: push reads a clock for its `pushed-at` field, and the
// map that dispatches the in-process verbs carries no clock, so the production clock is bound
// here and the tests call cmdPush with their own.
func cmdPushNow(args []string, stdout, stderr io.Writer) int {
	return cmdPush(args, stdout, stderr, func() time.Time { return time.Now().UTC() })
}
