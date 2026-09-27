// nova-table: a general table over Redis whose every body cell is an
// ordered set (internal/ntable). A table is columns with a projection and a
// fold each, rows in a stable order, and one Redis ZSET per body cell, owned
// by the table or bound to a set another tool owns; render prints it as
// fixed-width text, watch redraws it once a second. The sprint table's
// stream block is its first table (docs/nova-table/README.md).
//
// Exit 0 done, 1 refused (the store said no: a bound cell, a table that is
// not there, a member that is not in its set), 2 usage (could not run).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/redis/go-redis/v9"
)

const usage = `nova-table: a table over Redis, every cell an ordered set (see docs/nova-table/README.md)

usage:
  nova-table version
  nova-table help
  nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]
  nova-table set <table> [--footer <label>] [--rename <name>] [--columns <spec>] [--hide <cols>] [--show <cols>]
  nova-table drop <table>
  nova-table list
  nova-table row add <table> <row> [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]
  nova-table row set <table> <row> <col>=<value> ...
  nova-table row del <table> <row>
  nova-table cell add <table> <row> <col> <member> [--score <n>]
  nova-table cell remove <table> <row> <col> <member>
  nova-table cell move <table> <row> <from-col> <to-col> <member>
  nova-table cell members <table> <row> <col>
  nova-table clear <table>
  nova-table show <table>
  nova-table render <table> [--hide-zero-rows] [--width <col=n,...>]
  nova-table watch <table>[,<table>...] [--every <duration>] [--out <file>] [--title <text>] [--hide-zero-rows] [--once]

Every verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR,
then the seat's address) and dials as the seat nova-sprint dials as: --seat
<name> or NOVA_SEAT, else NOVA_SPRINT_REDIS_USER with the password in the
variable NOVA_SPRINT_REDIS_PASSWORD_ENV names. Flags may follow the words.

A column is name[:projection[:fold[:label]]]: the projection is what a body
cell prints, count (the set's size, the default), members (the members in
score order), first, last, or text (the row's label, no set); the fold is
what the footer prints over the column, sum (the default for count), max,
union (of members) or none. A row's cells are owned by the table unless
row add binds a column to a set another tool owns (<col>=<key>): a bound
cell is a view, read freely, and cell add, cell remove, cell move and clear
refuse it, naming the --owner verb. --exclude names one member the row's
counts and members leave out. render prints the table and nothing else,
nothing at all when it is empty; watch redraws it in place every --every
(1s) with no shell loop, or publishes it to --out by atomic rename.

exit codes: 0 done, 1 refused, 2 usage

example:
  nova-table create demo --columns job:text:none,ready,working,done
  nova-table row add demo build
  nova-table cell add demo build ready b1
  nova-table cell add demo build ready b2
  nova-table cell move demo build ready working b1
  nova-table show demo
  nova-table render demo
`

// version is empty in every ordinary build; a release stamps it with
// -ldflags "-X main.version=<tag>".
var version string

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// refuse is the one-line usage refusal: exit 2.
func refuse(stderr io.Writer, verb, what string) int {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	fmt.Fprintf(stderr, "nova-table%s: %s; run: nova-table help\n", where, oneline.Escape(what))
	return 2
}

// refused is the store's no, one line: exit 1.
func refused(stderr io.Writer, verb, what string) int {
	if !strings.Contains(what, "; run:") {
		what += "; run: nova-table help"
	}
	fmt.Fprintf(stderr, "nova-table %s: %s\n", verb, oneline.Escape(what))
	return 1
}

func run(args []string, stdout, stderr io.Writer) (code int) {
	// -h on any verb: its usage line and flags on stdout, exit 2.
	defer verbflag.Recover(stdout, "nova-table", &code)
	args, err := selectSeat(seatcred.Process(), args, os.Getenv, os.Setenv)
	if err != nil {
		return refuse(stderr, "", err.Error())
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb; create, row, cell, clear, show, render or watch a table")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help", "help takes no arguments")
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version", "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-table", version))
		return 0
	case "create":
		return cmdCreate(args[1:], stdout, stderr)
	case "set":
		return cmdSet(args[1:], stdout, stderr)
	case "drop":
		return cmdDrop(args[1:], stdout, stderr)
	case "list":
		return cmdList(args[1:], stdout, stderr)
	case "row":
		return cmdRow(args[1:], stdout, stderr)
	case "cell":
		return cmdCell(args[1:], stdout, stderr)
	case "clear":
		return cmdClear(args[1:], stdout, stderr)
	case "show":
		return cmdShow(args[1:], stdout, stderr)
	case "render":
		return cmdRender(args[1:], stdout, stderr)
	case "watch":
		return cmdWatch(args[1:], stdout, stderr)
	}
	return refuse(stderr, "", "unknown verb "+args[0]+"; create, row, cell, clear, show, render or watch a table")
}

// selectSeat is nova-sprint's seat resolution (cmd/nova-sprint/seat.go,
// nova-tools#4330), the same here so a session that names a seat for one
// tool names it for the other: --seat <name> (or NOVA_SPRINT_SEAT, then
// NOVA_SEAT) is taken off the line, its row in nova-sprint's seats.tsv
// names its Redis address (the --redis default) and login, and a seat with
// no row is the nova-secrets seat of that name.
func selectSeat(sel *seatcred.Selection, args []string, getenv func(string) string, setenv func(k, v string) error) ([]string, error) {
	rest, err := sel.FromArgs(args, func(k string) string {
		if k == seatcred.SeatEnv {
			if v := getenv("NOVA_SPRINT_SEAT"); v != "" {
				return v
			}
		}
		return getenv(k)
	})
	if err != nil {
		return nil, err
	}
	seat := sel.Selected()
	if seat == "" {
		return rest, nil
	}
	var p seatcred.Profile
	path, err := seatcred.ProfilePath("nova-sprint", getenv)
	if err != nil {
		err = fmt.Errorf("seat %s: %w: %v", seat, seatcred.ErrNoProfileRow, err)
	} else {
		p, err = seatcred.LoadProfile(path, seat, getenv("HOME"))
	}
	if errors.Is(err, seatcred.ErrNoProfileRow) {
		rowErr := err
		sel.SelectWith(seat, "", func(s string) (seatcred.Cred, error) {
			c, err := seatcred.Resolve(s, getenv)
			if err != nil {
				return c, fmt.Errorf("%v; and as a nova-secrets seat: %w", rowErr, err)
			}
			return c, nil
		})
		return rest, nil
	}
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_REDIS"} {
		if err := setenv(k, p.Addr); err != nil {
			return nil, fmt.Errorf("seat %s: cannot set %s: %v", seat, k, err)
		}
	}
	sel.SelectProfile(p, func(string) (seatcred.Cred, error) { return seatcred.ResolveProfile(p, getenv) })
	return rest, nil
}

// redisFlag declares --redis on fs with the seat-aware default.
func redisFlag(fs interface {
	String(name, value, usage string) *string
}) *string {
	return fs.String("redis", redisDefault(os.Getenv), "the Redis address (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, then the seat's)")
}

// redisDefault is NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's
// address, the default every nova-sprint --redis has.
func redisDefault(getenv func(string) string) string {
	for _, k := range []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR"} {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return seatcred.Addr()
}

// parseInterleaved parses fs over args with the flags anywhere on the
// line: every argument the flag set does not take is a positional, in
// order, so `create demo --columns ...` and `create --columns ... demo`
// read the same.
func parseInterleaved(fs interface {
	Parse([]string) error
	Args() []string
}, args []string) ([]string, error) {
	var pos []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	return pos, nil
}

// open dials the store for a verb; a missing address is a usage refusal.
func open(ctx context.Context, addr string) (*store.Store, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat)")
	}
	return store.Open(ctx, addr)
}

// quietRedis keeps go-redis's own pool log off the line: the verb's one
// refusal is its redis line, never five library lines ahead of it
// (nova-sprint doctor's rule); SetLogger writes a package variable, so once
// per process.
type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

var quietRedisOnce sync.Once

// client is open's client for a verb, or its refusal.
func client(ctx context.Context, verb, addr string, stderr io.Writer) (*store.Store, *redis.Client, int) {
	quietRedisOnce.Do(func() { redis.SetLogger(quietRedis{}) })
	st, err := open(ctx, addr)
	if err != nil {
		return nil, nil, refuse(stderr, verb, err.Error())
	}
	return st, st.Client(), 0
}

// field is a value of a key=value field: quoted when it holds a space, a
// tab or a quote (nova-sprint's spelling), else as it is.
func field(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return oneline.Escape(s)
}

// storeRefusal maps a library error to the exit code the line carries: a
// store that could not be reached is 2, the store's own no is 1.
func storeRefusal(stderr io.Writer, verb string, err error) int {
	msg := err.Error()
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "NOAUTH") || strings.Contains(msg, "WRONGPASS") || strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "no such host") {
		return refuse(stderr, verb, msg)
	}
	return refused(stderr, verb, msg)
}
