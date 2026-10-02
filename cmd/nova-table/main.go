// nova-table: work tables of ordered sets, text and formulas over Redis
// (internal/ntable). A table is columns with a projection and a
// fold each, rows in a stable order, and one Redis ZSET per body cell, owned
// by the table or bound to a set another tool owns; render prints it as
// fixed-width text, watch redraws it once a second. The sprint table's
// stream block is its first table (docs/nova-table/README.md).
//
// Exit 0 done, 1 refused (the store said no: a bound cell, a table that is
// not there, a member that is not in its set), 2 usage (could not run).
package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/redis/go-redis/v9"
)

const usageDetails = `Table write verbs take --epoch <observed epoch> (default 0), --actor, --fence,
--idem (receipt metadata only; does not deduplicate retries) and --receipt. create also takes --epoch-key,
--epoch-field (default n), and --member-prefix (default table::member:).
A stale epoch is refused. drop keeps the saved column definition and the
table's identity unless --definition is given, which removes both and the rows
of every epoch (and repairs a store left with the identity alone); the
definition snapshots of earlier epochs remain available.
View configuration has no table epoch or receipt.
Quote column specs containing parentheses, for example 'done,pct:pct(done)'.

Store verbs take --redis <addr> (host:port or an absolute Unix socket path)
(else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address) and
dial as the seat --seat <name> or NOVA_SEAT names, else as
NOVA_SPRINT_REDIS_USER with the password in the variable
NOVA_SPRINT_REDIS_PASSWORD_ENV names. Flags may follow the words.

A column is name[:projection[:fold[:label]]]: the projection is what a body
cell prints, count (the set's size, the default), members (the members in
score order), first, last, text (the value written by row set, no set),
pct(<count-column>) (the share of all count columns in the row),
pct(<count-column>/<a>+<b>) (the share of the named count columns a, b of the
row), or sum(<a>+<b>) (the named count columns of the row added). A formula
names count columns of the same table, hidden or not. The row label is a
separate cell. The fold is what the footer prints over the column, sum (the
default for count and sum), max, avg (of count cells), union (of members),
pooled (the default for pct: the numerators summed over the denominators
summed), or none. Known-empty percentages print 0.0%; unread inputs print ?.
Example: 'ok,failed,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%'.
set --hide/--show hides or shows columns without removing their data. A row's cells are owned by the table unless
row add binds a column to a set another tool owns (<col>=<key>): a bound
cell is a view, read freely, and cell add, cell remove, cell move and clear
refuse it, naming the --owner verb. --exclude names one member the row's
counts and members leave out. render <table> prints the table and nothing else,
nothing at all when it is empty. render --view <name> prints one frame with
the view's timestamp, title and summary line; view state sets a text the
summary line shows alone, in place of the counts, until --clear. watch redraws it in place every --every
(1s) with no shell loop, or publishes it to --out by atomic rename.

Order is kept by the table: rows draw in the order they were added and
columns in the order they were declared, until a verb moves them. row sort
orders the rows once; with --keep (by name or label) the sort stands, every
row added later takes its place, and row move and row order are refused
until row sort --manual. row del of a missing row succeeds with existed=0.
col del refuses a column that holds members or text, naming all blocking
members and batch removal commands, or the text to clear first.

shell reads one command per line on a shared connection. It prints write
receipts by default; --receipt=false disables them. Enter help, quit or exit.

exit codes: 0 done, 1 refused, 2 usage

example:
  nova-table create demo --columns ready,working,done
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

// remedied reports a line that already names its next step: a "; run:" of
// this tool's, or a "; next:" of redisconn's.
func remedied(what string) bool {
	return strings.Contains(what, "; run:") || strings.Contains(what, "; next:")
}

// refuse is the one-line usage refusal: exit 2.
func refuse(stderr io.Writer, verb, what string) int { return refuseWith(stderr, verb, what, 2) }

// refused is the store's no, one line: exit 1.
func refused(stderr io.Writer, verb, what string) int { return refuseWith(stderr, verb, what, 1) }

// refuseWith prints a refusal in the one grammar every nova tool's refusal
// has (docs/STANDARD.md section 2, internal/tool): `<VERB> REFUSED: <what
// was wrong>; run: <remedy>`, the verb upper case with its words joined by
// dashes, TABLE for the tool itself. A line naming no next step of its own
// points at the verb's help, or the tool's. A verb asked for --json gets the
// same reason and remedy as JSON (jsonRefusals).
func refuseWith(stderr io.Writer, verb, what string, code int) int {
	if !remedied(what) {
		what += "; run: " + helpFor(verb)
	}
	if j, ok := stderr.(*jsonRefusals); ok {
		why, remedy := what, ""
		if i := strings.LastIndex(what, "; run: "); i >= 0 {
			why, remedy = what[:i], what[i+len("; run: "):]
		}
		j.out.Why = append(j.out.Why, why)
		j.out.Remedy = cmp.Or(remedy, j.out.Remedy)
		return code
	}
	fmt.Fprintf(stderr, "%s REFUSED: %s\n", token(verb), oneline.Escape(what))
	return code
}

// token is the first word of a refusal: the verb, upper case, its words
// joined by dashes (CELL-ADD), as internal/tool spells it; TABLE for the tool.
func token(verb string) string {
	if verb == "" {
		return "TABLE"
	}
	return strings.ToUpper(strings.Join(strings.Fields(verb), "-"))
}

// helpFor is the help a refusal of verb points at: the verb's own (a group's
// lists its verbs), else the tool's.
func helpFor(verb string) string {
	if isGroup(verb) || slices.ContainsFunc(commands, func(c command) bool { return c.name == verb }) {
		return "nova-table help " + verb
	}
	return "nova-table help"
}

func run(args []string, stdout, stderr io.Writer) int {
	return (&application{in: os.Stdin}).run(args, stdout, stderr)
}

func (app *application) run(args []string, stdout, stderr io.Writer) (code int) {
	defer recoverHelp(stdout, &code)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verbs are "+rootNames())
	}
	if isHelp(args[0]) || args[0] == "help" {
		return helpCommand(args[1:], stdout, stderr)
	}
	// Help never needs a seat profile or credentials.
	if len(args) == 2 && (isHelp(args[1]) || args[1] == "help") && isGroup(args[0]) {
		return helpCommand(args[:1], stdout, stderr)
	}
	for _, c := range commands {
		words := strings.Fields(c.name)
		if len(args) == len(words)+1 && strings.Join(args[:len(words)], " ") == c.name && isHelp(args[len(words)]) {
			return helpCommand(words, stdout, stderr)
		}
	}
	if verb := jsonVerb(args); verb != "" && verbflag.BoolAsked(args, "json") {
		// A verb that answers in JSON answers a refusal in JSON too: the one JSON
		// object every nova tool's result is (internal/tool's Out, docs/STANDARD.md
		// section 2), {"result":{"verb","status":"refused","exit","remedy","why"}},
		// on stdout, the exit the refusal carried (2 could not run, 1 the store said
		// no). refuseWith fills it; anything else said on stderr is a note.
		j := &jsonRefusals{out: tool.Out{Verb: verb, Status: tool.Refused}}
		if code = app.seated(args, stdout, j); code == 0 {
			_, err := stderr.Write(j.said.Bytes())
			return exitOf(err)
		}
		j.out.Exit = code
		for _, l := range strings.Split(strings.TrimSpace(j.said.String()), "\n") {
			if l != "" {
				j.out.Notes = append(j.out.Notes, l)
			}
		}
		if j.out.Render(stdout, true) != code {
			return 1
		}
		return code
	}
	return app.seated(args, stdout, stderr)
}

// jsonRefusals stands in for stderr while a verb asked for --json runs: the
// refusals refuseWith prints go into out, whatever else is written into said.
type jsonRefusals struct {
	out  tool.Out
	said bytes.Buffer
}

func (j *jsonRefusals) Write(p []byte) (int, error) { return j.said.Write(p) }

// seated is the verb run as the seat the line or the environment selects.
func (app *application) seated(args []string, stdout, stderr io.Writer) int {
	var err error
	if app.shared == nil {
		args, err = selectSeat(seatcred.Process(), args, os.Getenv, os.Setenv)
		if err != nil {
			return refuse(stderr, "", err.Error())
		}
	}
	return app.dispatch(args, stdout, stderr)
}

// jsonVerb is the verb of args when it takes --json (batch, member read),
// else "".
func jsonVerb(args []string) string {
	switch {
	case len(args) > 0 && args[0] == "batch":
		return "batch"
	case len(args) > 1 && args[0] == "member" && args[1] == "read":
		return "member read"
	}
	return ""
}

// exitOf is 0, or 1 when what a verb printed did not reach its reader.
func exitOf(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

// selectSeat is the seat resolution nova-sprint defined
// (the old nova-sprint's seat.go, nova-tools#4330), carried here:
// --seat <name> (or NOVA_SPRINT_SEAT, then NOVA_SEAT) is taken off the line,
// its row in nova-sprint's seats.tsv names its Redis address (the --redis
// default) and login, and a seat with no row is the nova-secrets seat of that
// name.
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
func (app *application) redisFlag(fs interface {
	String(name, value, usage string) *string
}) *string {
	addr := redisDefault(os.Getenv)
	if app.shared != nil {
		addr = app.addr
	}
	return fs.String("redis", addr, "the Redis address (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, then the seat's)")
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
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	rest := args
	for len(rest) > 0 {
		if rest[0] == "--" {
			return append(pos, rest[1:]...), nil
		}
		if !strings.HasPrefix(rest[0], "-") || rest[0] == "-" {
			pos = append(pos, rest[0])
			rest = rest[1:]
			continue
		}
		// Parse one flag and its value at a time, so flag.Parse cannot consume
		// a -- separator before this loop sees it. A flag value of -- is legal.
		n := 1
		name, _, inline := strings.Cut(strings.TrimLeft(rest[0], "-"), "=")
		if f := fs.Lookup(name); f != nil && !inline && len(rest) > 1 {
			boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
			if !ok || !boolean.IsBoolFlag() {
				n = 2
			}
		}
		if err := fs.Parse(rest[:n]); err != nil {
			// verbflag's one wording: an unknown flag with the verb's flags and
			// the nearest, a bad value with what its flag wants
			return nil, fmt.Errorf("%s; run: nova-table help %s", verbflag.Explain(fs, err), fs.Name())
		}
		rest = rest[n:]
	}
	return pos, nil
}

// open dials the store for a verb through redisconn, the one way a nova
// tool opens Redis (#4492): the dial and the handshake (HELLO 3, with the
// login) are done, or refused in one line, before the verb's first command,
// which is still its first round trip. The client's first hook puts the
// function library on a store that holds none (withLibrary, library.go).
// A missing address is a usage refusal.
func open(ctx context.Context, addr string, getenv func(string) string) (*redisconn.Conn, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat)")
	}
	o, getenv, err := login(addr, seatcred.Process(), getenv)
	if err != nil {
		return nil, err
	}
	conn, err := redisconn.Open(ctx, o, getenv)
	if err != nil {
		if text := seatWords(seatcred.Process(), err.Error()); text != err.Error() {
			err = &reworded{text, err}
		}
		return nil, err
	}
	withLibrary(conn.Client())
	return conn, nil
}

// login is who nova-table dials as, the login nova-sprint's store dialed as
// (internal/nsprint/store authFromEnv), in redisconn's terms. A seat (--seat,
// NOVA_SEAT) logs in as its user, with the password the seat's file holds
// under its key: the key is named as the password's variable, and the getenv
// handed to redisconn answers it from memory, so the password never enters
// this process's environment. With no seat, NOVA_SPRINT_REDIS_USER names the
// user and NOVA_SPRINT_REDIS_PASSWORD_ENV the variable that holds its
// password, NOVA_REDIS_BENCH_PASSWORD when it names none (redisauth.Auth's
// default, named here because redisconn reads no default of its own); no
// user is the default user with no password, whatever
// NOVA_SPRINT_REDIS_PASSWORD_ENV holds, as it was.
func login(addr string, sel *seatcred.Selection, getenv func(string) string) (redisconn.Options, func(string) string, error) {
	o := redisconn.Options{Addr: addr}
	c, ok, err := sel.Active()
	if ok {
		if err != nil {
			return o, nil, err
		}
		var password string
		// ignored: Use fails only when its function is nil or fails, and this one does neither
		_ = c.Password.Use(func(pw string) error { password = pw; return nil })
		o.User, o.PasswordEnv = c.User, c.Key
		return o, func(k string) string {
			if k == c.Key {
				return password
			}
			return getenv(k)
		}, nil
	}
	o.Env = redisconn.Env{User: redisauth.UserEnv}
	if getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	return o, getenv, nil
}

// client is open's connection for a verb, or its refusal.
func (app *application) client(ctx context.Context, verb, addr string, stderr io.Writer) (*connection, *redis.Client, int) {
	if app.shared != nil {
		if addr != app.addr {
			return nil, nil, refuse(stderr, verb, "the shell connection is fixed; choose --redis when entering nova-table shell")
		}
		if err := app.shared.prepare(); err != nil {
			return nil, nil, refuse(stderr, verb, err.Error())
		}
		return app.shared, app.shared.Client(), 0
	}
	conn, err := open(ctx, addr, app.env())
	if err != nil {
		text := err.Error()
		if strings.TrimSpace(addr) == "" || redisconn.Classify(err) == redisconn.Unreachable {
			text = firstTry(text, app.lookPath)
		}
		return nil, nil, refuse(stderr, verb, text)
	}
	return &connection{Conn: conn}, conn.Client(), 0
}

// firstTry adds to a refusal for a store that is not there (no address, or
// none answering at it) the way to a first try, checked here rather than left
// to the reader: with redis-server on PATH, the one command that starts a
// throwaway local store and prints the --redis that reaches it; without one,
// that it is missing, and that every verb that writes runs with no store
// under --dry-run.
func firstTry(why string, lookPath func(string) (string, error)) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("redis-server")
	if err != nil {
		return why + "; for a first try, no redis-server is on PATH to start a throwaway store (it wants Redis 7 or later), " +
			"and every verb that writes runs with no store under --dry-run; run: nova-table help"
	}
	return why + "; for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '" +
		strings.ReplaceAll(bin, "'", `'\''`) + `' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"`
}

// field is a value of a key=value field: quoted when it holds a space, a
// tab or a quote (nova-sprint's spelling), else as it is.
func field(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return oneline.Escape(s)
}

// lost reports an error that says the store could not be reached or would
// not let this client in (redisconn.Classify: Unreachable or AuthRefused),
// or a client already closed: the connection is not to be used again.
func lost(err error) bool {
	return err != nil && (redisconn.Classify(err) != redisconn.Other || errors.Is(err, redis.ErrClosed))
}

// refusal maps a verb's error to the exit code its line carries: a store
// that could not be reached or refused the login is 2, the store's own no
// is 1. A lost-store error is put in redisconn's words (what was tried,
// what came back, the next step) only when the cause names no next step of
// its own: a write whose reply was lost says `run: nova-table show <table>`,
// because the write may have committed, and "start the store" over it
// would invite the duplicate AtMostOnce exists to prevent. One remedy a
// line.
func (c *connection) refusal(stderr io.Writer, verb string, err error) int {
	if lost(err) {
		text := err.Error()
		if c != nil && c.Conn != nil && !remedied(text) {
			text = seatWords(seatcred.Process(), c.Explain(err).Error())
		}
		return refuse(stderr, verb, text)
	}
	return refused(stderr, verb, err.Error())
}

// seatWords says a seat's password as the seat's: redisconn names the
// password by the variable it read, and a seat's is the key of the seat's
// file, answered from memory, not a variable of the environment.
func seatWords(sel *seatcred.Selection, text string) string {
	c, ok, err := sel.Active()
	if !ok || err != nil || c.Key == "" {
		return text
	}
	return strings.NewReplacer(
		"(password from "+c.Key+")", "(password from seat "+c.Seat+", key "+c.Key+" of its file)",
		"check that "+c.Key+" holds", "check that seat "+c.Seat+"'s file holds under "+c.Key,
	).Replace(text)
}

// reworded is an error in other words, whose class is still its cause's
// (errors.As and redisconn.Classify see through Unwrap).
type reworded struct {
	text string
	err  error
}

func (r *reworded) Error() string { return r.text }
func (r *reworded) Unwrap() error { return r.err }
