package pulse

// The pitstop verb (nova-tools #2414): the fleet Redis key `pitstop`, read by every
// launcher, loop and lander, written by `nova-pulse pitstop set` and read by `nova-pulse
// pitstop check`. It replaces bin/pitstop (#2022): the bash script checked a STOP file the
// harness left beside it, and on every bench that file was a second spelling of what
// nova-pulse already said -- so two cards that disagreed about whether the fleet was stopped
// held the loop at the wrong bench for ten minutes on 2026-09-21 (#2414).
//
// THE DATA IS ONE REDIS SET NAMED `pitstop`. Each member is the name of a nova-pulse verb
// that the fleet has paused: `launch`, `harvest`, `fill`, `sweep`, `reap`, `cut`. A
// bench-side launcher asks `check launch` before it claims a slot; a harvester asks
// `check harvest` before it folds a card; the answer is yes (paused, exit 3) or no (open,
// exit 0), and a store it cannot reach is exit 3 too -- "do not proceed", never "proceed".
//
// `set pause --except harvest` DELs the key and SADDs every known verb EXCEPT those the flag
// names; `set resume` DELs the key. The set is the whole state: one DEL is one reset, and
// one SADD is one pause, so a bench that races a `set` with a `check` reads either the old
// state or the new one, never a half-built one. The CLI also takes `--key` for a fleet that
// runs more than one pitstop at once; the default is the one the bash script always read.
//
// Every outside fact is a seam: the dial, the stdout, the stderr and the key. The CLI
// installs the production defaults and a test substitutes its own; nothing here opens a
// socket except the one test that says so, and it points at a miniredis.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// DefaultPitstopKey is the Redis key name. The bash script bin/pitstop used this name and
// every bench already reads it: a caller that names a different key in the fleet would be a
// fleet that disagreed with itself, and a `check` that disagreed with a `set` is a bench
// that is not on the fleet's stop.
const DefaultPitstopKey = "pitstop"

// PitstopVerbs is the closed set of verbs `set pause` pauses by default. Six: the verbs
// whose bench-side runners, fillers and harvesters check the key before they act. A bench
// that pauses a verb that is not in this list is a bench that paused nothing for the
// nova-pulse fleet -- the bash script's STOP-* files listed the same six, in the same
// order, and the order is the order they were written in 2026-09-19.
var PitstopVerbs = []string{
	"launch",
	"harvest",
	"fill",
	"sweep",
	"reap",
	"cut",
}

// PitstopSubcommands is the vocabulary in the order the help prints it.
var PitstopSubcommands = []string{"set", "check"}

// PitstopInput is one invocation of the verb. The CLI splits set/check; this struct carries
// both, with one of State or Verb per shape, so a test drives the same path the CLI does.
type PitstopInput struct {
	// Sub is "set" or "check". Anything else is a refusal that names the two.
	Sub string
	// State is "pause" or "resume". Required when Sub is "set"; ignored otherwise.
	State string
	// Except is the list of verb names a `set pause` leaves unpaused. Each is trimmed; an
	// unknown name is silently skipped, because a flag that passed `harvest` and `repair`
	// has the same effect either way when the verb list is the closed one above.
	Except []string
	// Verb is the verb name `check` answers about. Required when Sub is "check"; any name
	// is accepted because a check the fleet does not own still answers "open", and a
	// misspelling on the bench reads as the bench proceeding.
	Verb string

	// Store is the fleet Redis. Required for both set and check; the verb refuses rather
	// than guess where the stop lives. A test points it at a miniredis; the production CLI
	// fills it from --store / --user / --password-env.
	Store StoreOptions
	// Key overrides the Redis key name. Empty is DefaultPitstopKey.
	Key string

	Stdout io.Writer
	Stderr io.Writer

	// Dial is the seam: production calls DialStore. A test can substitute a function that
	// returns a *redis.Client pointed at a miniredis, or that always returns an error to
	// prove a refused check exits non-zero and never 0.
	Dial func(ctx context.Context, o StoreOptions) (*redis.Client, error)
	// Timeout bounds the dial and every command. Zero is the same five seconds the
	// fleet store picks: a row pusher on a one-second tick must never block past its
	// next tick, and a check a launcher runs in the foreground must never block past
	// the runner's poll.
	Timeout time.Duration
}

// Pitstop runs one invocation. Exit codes follow SPEC.md: 0 open, 3 paused (or refused: the
// bench reads both as "do not proceed" and never as 0), 2 a refusal the CLI could not run.
func Pitstop(in PitstopInput) int {
	stdout, stderr := in.Stdout, in.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if in.Dial == nil {
		in.Dial = DialStore
	}
	if in.Timeout <= 0 {
		in.Timeout = 5 * time.Second
	}
	sub := strings.TrimSpace(in.Sub)
	if sub != "set" && sub != "check" {
		return pitstopRefusal(stderr, "%s is not a subcommand; pitstop takes %s", oneline.Field(sub), strings.Join(PitstopSubcommands, " or "))
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = DefaultPitstopKey
	}
	if strings.TrimSpace(in.Store.Addr) == "" {
		return pitstopRefusal(stderr, "--store is required; the fleet Redis is where the stop lives and there is no default to guess")
	}

	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()
	rdb, err := in.Dial(ctx, in.Store)
	if err != nil {
		// A store that cannot be reached is "do not proceed". The exit is non-zero and
		// never 0, the way the DONE-WHEN requires; 3 is the same code a paused check uses,
		// so a launcher that reads the exit code reads both as the same brake.
		fmt.Fprintf(stderr, "PITSTOP CHECK REFUSED key=%s: %s\n", oneline.Field(key), oneline.Err(err))
		return 3
	}
	defer rdb.Close()

	switch sub {
	case "set":
		return pitstopSet(in, key, rdb, ctx, stdout, stderr)
	case "check":
		return pitstopCheck(in, key, rdb, ctx, stdout, stderr)
	}
	return 2
}

// pitstopSet handles `set pause` and `set resume`. The store is open and the key is the one
// the bench reads; the only thing left is to validate the verb shape and to write it.
func pitstopSet(in PitstopInput, key string, rdb *redis.Client, ctx context.Context, stdout, stderr io.Writer) int {
	state := strings.TrimSpace(in.State)
	switch state {
	case "pause", "resume":
	default:
		return pitstopRefusal(stderr, "%s is not a state; set wants pause or resume", oneline.Field(state))
	}
	if err := rdb.Del(ctx, key).Err(); err != nil {
		return pitstopRefusal(stderr, "del %s: %s", oneline.Field(key), oneline.Err(err))
	}
	paused := 0
	if state == "pause" {
		except := make(map[string]bool, len(in.Except))
		for _, e := range in.Except {
			if e = strings.TrimSpace(e); e != "" {
				except[e] = true
			}
		}
		members := make([]interface{}, 0, len(PitstopVerbs))
		for _, v := range PitstopVerbs {
			if except[v] {
				continue
			}
			members = append(members, v)
			paused++
		}
		if len(members) > 0 {
			if err := rdb.SAdd(ctx, key, members...).Err(); err != nil {
				return pitstopRefusal(stderr, "sadd %s: %s", oneline.Field(key), oneline.Err(err))
			}
		}
	}
	except := strings.Join(in.Except, ",")
	if state == "pause" {
		fmt.Fprintf(stdout, "PITSTOP SET state=pause paused=%d except=%s key=%s\n",
			paused, oneline.Field(except), oneline.Field(key))
	} else {
		fmt.Fprintf(stdout, "PITSTOP SET state=resume paused=0 key=%s\n", oneline.Field(key))
	}
	return 0
}

// pitstopCheck handles `check <verb>`. The store is open and the key is the one the bench
// reads; the only thing left is to ask and to print the line the bench's launcher reads.
func pitstopCheck(in PitstopInput, key string, rdb *redis.Client, ctx context.Context, stdout, stderr io.Writer) int {
	verb := strings.TrimSpace(in.Verb)
	if verb == "" {
		return pitstopRefusal(stderr, "check wants a verb name (one of %s, or any verb the bench asks about)", strings.Join(PitstopVerbs, ", "))
	}
	paused, err := rdb.SIsMember(ctx, key, verb).Result()
	if err != nil {
		fmt.Fprintf(stderr, "PITSTOP CHECK REFUSED verb=%s key=%s: %s\n",
			oneline.Field(verb), oneline.Field(key), oneline.Err(err))
		return 3
	}
	if paused {
		fmt.Fprintf(stdout, "PITSTOP CHECK verb=%s state=paused key=%s\n", oneline.Field(verb), oneline.Field(key))
		return 3
	}
	fmt.Fprintf(stdout, "PITSTOP CHECK verb=%s state=open key=%s\n", oneline.Field(verb), oneline.Field(key))
	return 0
}

// pitstopRefusal is one PITSTOP REFUSED line and exit 2: a refusal the CLI could not run
// is a refusal the caller has to fix, not a "do not proceed", so the code is 2 and not 3.
func pitstopRefusal(stderr io.Writer, format string, a ...interface{}) int {
	fmt.Fprintf(stderr, "PITSTOP REFUSED: %s\n", oneline.Err(fmt.Errorf(format, a...)))
	return 2
}
