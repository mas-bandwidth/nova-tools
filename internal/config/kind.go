// Package config is nova-config's library: the permanent, non-ephemeral
// configuration of the fleet, kept in Postgres (schema `config`) and applied
// into Redis so Redis is always a rebuildable copy of the configuration.
//
// Every kind of configuration is one Kind descriptor: its name, its table,
// its fields with their types and validators, and the two Redis functions
// that write and remove one row. The CLI's add, remove, set, list, show and
// history verbs, the SQL, the history rows and the apply diff are all
// generated from the descriptor, so a new kind is one descriptor, one
// migration and one Redis writer (docs/SPEC-CONFIG.md, "Adding a kind").
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// Type is a field's type. It decides the SQL column, the flag's parsing and
// the validator.
type Type string

const (
	// TypeText is free text, stored as text, escaped on the typed line.
	TypeText Type = "text"
	// TypeInt is a non-negative integer, stored as integer.
	TypeInt Type = "int"
	// TypeEnum is one word from Field.Enum, stored as text.
	TypeEnum Type = "enum"
	// TypeList is a comma list of words from Field.Enum, deduplicated and
	// sorted, stored as text ("" is the empty list).
	TypeList Type = "list"
	// TypeNames is a comma list of names (Field.Pattern each), deduplicated
	// and sorted, stored as text.
	TypeNames Type = "names"
	// TypeRef is the name of a row of another kind (Field.Ref), stored as
	// text with a foreign key.
	TypeRef Type = "ref"
	// TypeBool is true or false, stored as boolean.
	TypeBool Type = "bool"
	// TypeKeys is a comma list of environment variable names (letters,
	// digits and underscores, not starting with a digit), deduplicated and
	// sorted, stored as text: the names of secrets, never their values.
	TypeKeys Type = "keys"
	// TypeArgv is a command as a JSON array of strings, the program first,
	// stored as text in its compact JSON spelling.
	TypeArgv Type = "argv"
	// TypeSeq is a comma list of row names of another kind (Field.Ref) in the
	// order given, a name kept as often as it is given, stored as text ("" is
	// the empty list): a tier's route array.
	TypeSeq Type = "seq"
	// TypeDecimal is a non-negative decimal number (digits, one point, no sign
	// or exponent), stored as text in its one spelling (cardcost.Canonical), ""
	// when not set: a price, never a float.
	TypeDecimal Type = "decimal"
)

// Field is one column of a kind: the flag `--<Name>` on add and set, the
// column of the same name (quoted) in Postgres, and the key on every typed
// line.
type Field struct {
	Name string
	Type Type
	// Required is true when add refuses a row without it. set never requires
	// a field: it updates the ones named.
	Required bool
	// Enum is the word list of a TypeEnum or TypeList field.
	Enum []string
	// Ref is the kind a TypeRef or TypeSeq field names.
	Ref string
	// Help is the flag's help line, one sentence.
	Help string
	// Default is the canonical value add stores for a field it is not
	// given. "" means the type's zero: 0 for an int, false for a bool, the
	// empty value for the rest, or an unset value when Nullable is true.
	Default string
	// Nullable leaves an omitted field unset, represented as SQL NULL.
	Nullable bool
}

// Kind is one kind of configuration. See the package comment.
type Kind struct {
	// Name is the kind's word in the grammar (`nova-config <kind> ...`), the
	// singular; Table is its table under schema config.
	Name  string
	Table string
	// Fields are the columns after the row key `name`, in the order every
	// line prints them and every migration declares them.
	Fields []Field
	// Doc is the one sentence `nova-config kinds` prints about the kind.
	Doc string
	// Singleton is a kind of exactly one row, named as the kind is (the
	// fleet has exactly one coordinator at a time). Its migration
	// creates the row, so the grammar has no add,
	// remove or list and its set, show and history take no name
	// (docs/SPEC-CONFIG.md, "Singleton kinds").
	Singleton bool
	// Seed are the rows the kind's migration creates, every field at its
	// default (the tiers), so set takes them on a new store. nil is none.
	Seed []string
	// Derive, when set, is run by Apply on the kind's rows before they are
	// planned: a value another kind's row decides (the sprint's coordinator
	// as a friend's Redis role) is added here, so Redis holds it and the
	// stored row does not. nil derives nothing.
	Derive func(ctx context.Context, st Store, rows []Row) ([]Row, error)
	// ApplyOrder sorts the rows of this kind for apply: a row with a lower
	// number is written first. nil keeps name order. Friends put the
	// coordinator first so ns_friend_roles' bootstrap has one.
	ApplyOrder func(r Row) int
	// Check, when set, is the rule across a row's fields that no one field
	// can say (a loop runs every n seconds or is kept alive, never both).
	// add runs it on the new row before any store is opened; every store
	// runs it on the row a set would leave, and refuses the set with
	// ErrInvalid. nil checks nothing. add also runs it when some other
	// field is already refused, so one refusal names every problem; a
	// field that failed its own validation is then absent from r.Fields,
	// and a rule that needs it is skipped (the field's own refusal says
	// what is wrong).
	Check func(r Row) error
}

// NamePattern is the shape of a row key: lower-case, digits and dashes, the
// registry key friends and machines already use in Redis.
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// namesPattern is a word of a TypeNames list: letters, digits and dashes,
// any case.
var namesPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// keyPattern is a word of a TypeKeys list: an environment variable's name.
var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// The bounds of a TypeArgv value: a command is at most MaxArgs words and
// MaxArgvBytes bytes in its canonical spelling, refused before any write.
const (
	MaxArgs      = 64
	MaxArgvBytes = 4096
)

// The kinds of this cut (docs/SPEC-CONFIG.md, "The kinds of this cut").
const (
	KindMachine = "machine"
	KindFleet   = "fleet"
	KindFriend  = "friend"
	KindSprint  = "sprint"
	KindLoop    = "loop"
	KindRoute   = "route"
	KindTier    = "tier"
)

// FriendRoles are the roles someone decides for a friend. The coordinator
// role is not one: who coordinates is the sprint row's one field, and apply
// derives the Redis role from it (Kind.Derive below), so ns_friend_roles
// still sees exactly one coordinator.
var FriendRoles = []string{"builder", "may-hold", "reader"}

// Tiers are the model tiers a friend can do, capacity.lua's filter_ok
// spelling (frontier, pro, flash).
var Tiers = []string{"flash", "frontier", "pro"}

// RouteTiers are the tiers a route serves: Tiers less frontier, whose cards
// are never drawn from routes and escalate to the coordinator.
var RouteTiers = []string{"flash", "pro"}

// CoordinatorRole is the Redis role ns_friend_roles and the deal read
// (friend:<f>:roles), derived at apply from the sprint row.
const CoordinatorRole = "coordinator"

// Kinds is the registry, in apply order: machines first, the fleet row next
// (it names machines, and a friend's desired slots are charged to the
// fleet's coordinator machine when her beat names none), friends, the
// sprint row (it names a friend), loops (each names a machine), routes
// (each names no row), and tiers last (each names routes).
//
// A machine's record is exactly the declared facts something reads, one
// reader each, and nothing invented. Its name is the tailnet host: `ssh <name>`
// reaches it, so there is no address field ("All fleet machines must be on
// the tailnet. This is a hard requirement."). Measured facts (os, arch,
// cores, memory) are never typed: they come live from the machine's own
// beat (Beat, docs/SPEC-CONFIG.md, "Declared and measured").
var Kinds = []*Kind{
	{
		Name:  KindMachine,
		Table: "machines",
		Doc:   "a machine of the fleet, named by its tailnet host: the login, the seat, its ceiling, its runners, and the sprint member's width on it",
		Fields: []Field{
			{Name: "user", Type: TypeText, Required: true, Help: "the login the plays and seals use on it (ssh <user>@<name>)"},
			{Name: "seat", Type: TypeText, Required: true, Help: "its nova-secrets seat: the identity it opens secrets as, one <seat>.yaml in the store"},
			{Name: "slots", Type: TypeInt, Required: true, Help: "the machine ceiling apply writes to machine:<m>:ceiling, which the friends' desired slots must fit under; not the sprint's width"},
			{Name: "runners", Type: TypeInt, Help: "how many CI runners it hosts; 0 (the default) hosts none"},
			{Name: "width", Type: TypeInt, Help: "the most work cards the sprint's member on it runs at once, what nova-sprint fleet sync sets; set apart from --slots, never derived from it; 0 (the default) is no member, dealt no work"},
		},
	},
	{
		Name:      KindFleet,
		Table:     "fleet",
		Singleton: true,
		Doc:       "the one row of fleet-wide facts: the store and coordinator machines, Redis port and explicit password-free Postgres URI",
		Fields: []Field{
			{Name: "store", Type: TypeRef, Ref: KindMachine, Help: "the machine that runs Redis (a machine row), or empty"},
			{Name: "coordinator", Type: TypeRef, Ref: KindMachine, Help: "the machine the coordinator's loops run on (a machine row), or empty"},
			{Name: "redis_port", Type: TypeInt, Nullable: true, Help: "the explicit TCP port Redis listens on, from 1 through 65535; unset until declared"},
			{Name: "pg_dsn", Type: TypeText, Help: "the explicit password-free postgres:// URI the configuration store uses; empty until set"},
			{Name: "loops_dir", Type: TypeText, Help: "the directory on each machine where loops write their logs; seeded with ~/nova-bench/loops"},
		},
		Check: checkFleet,
	},
	{
		// A friend's row is what someone decides for her: how wide, which tiers
		// and which roles. Where she runs, her harness and her logins are
		// runtime data a friend would just know, reported by her own presence
		// rather than stored in configuration.
		Name:  KindFriend,
		Table: "friends",
		Doc:   "an AI friend: how wide she runs, which tiers she can do, and her roles",
		Fields: []Field{
			{Name: "slots", Type: TypeInt, Required: true, Help: "her desired slots, under the ceiling of the machine her beat reports; no machine's width"},
			{Name: "tiers", Type: TypeList, Enum: Tiers, Required: true, Help: "which tiers she can do: comma list of " + strings.Join(Tiers, ", ")},
			{Name: "roles", Type: TypeList, Enum: FriendRoles, Help: "comma list of " + strings.Join(FriendRoles, ", ") + " (who coordinates is the sprint row's)"},
		},
		ApplyOrder: func(r Row) int {
			if hasWord(r.Fields["roles"], CoordinatorRole) {
				return 0
			}
			return 1
		},
		Derive: deriveCoordinator,
	},
	{
		Name:      KindSprint,
		Table:     "sprint",
		Singleton: true,
		Doc:       "the one row of sprint-global facts: which friend coordinates",
		Fields: []Field{
			{Name: "coordinator", Type: TypeRef, Ref: KindFriend, Help: "the friend who holds the coordinator role (a friend row), or empty; set it to hand over"},
		},
	},
	{
		// A loop is a supervised process on one machine: the command, the
		// seat it opens its secrets from and the names of the secrets it
		// needs, and how it runs (every n seconds, or kept alive). Every
		// value is data in the row; the code names no machine, seat or
		// secret (docs/SPEC-CONFIG.md, "loop"). The plays render one unit
		// per row from the Redis view apply writes; the log path is derived
		// from the fleet row's loops_dir and the loop's name (LoopLog), never typed.
		Name:  KindLoop,
		Table: "loops",
		Doc:   "a supervised loop on one machine: its command, the seat and secret names it opens, and how it runs (every n seconds or kept alive)",
		Fields: []Field{
			{Name: "machine", Type: TypeRef, Ref: KindMachine, Required: true, Help: "the machine it runs on (a machine row)"},
			{Name: "argv", Type: TypeArgv, Required: true, Help: `the command as a JSON array of strings, the program first: '["/path/prog","--flag","v"]'; never a secret, which goes by name in --keys`},
			{Name: "seat", Type: TypeText, Help: "the nova-secrets seat on that machine it opens its secrets from, or empty when it needs none"},
			{Name: "keys", Type: TypeKeys, Help: "comma list of the names of the secrets it needs from the seat (API_KEY,...), never a value; empty when none"},
			{Name: "every", Type: TypeInt, Help: "seconds between runs of a periodic loop; 0 (the default) when it is kept alive"},
			{Name: "keepalive", Type: TypeBool, Help: "true for a long-running unit restarted when it exits; false (the default) when it runs --every n"},
			{Name: "width", Type: TypeInt, Help: "the --width its command runs with: above 0 it replaces the argv's --width, or is appended when the argv has none; 0 (the default) runs the argv as written. A reader loop's width; a work member's is its machine row's (machine set <m> --width <n>), so leave it 0 there"},
			{Name: "enabled", Type: TypeBool, Default: "true", Help: "false writes the unit and does not start it; true (the default) runs it"},
		},
		Check: checkLoop,
	},
	{
		// A route is one way to run a model tier: the provider and model a
		// card of that tier runs on, its budget and deadline. The deal takes
		// the routes of a card's tier in the order of the tier's array (the
		// tier kind; docs/SPEC-CONFIG.md, "route").
		Name:  KindRoute,
		Table: "routes",
		Doc:   "a route of a model tier: the provider and model a card of that tier runs on, its token budget and deadline; the tier's array orders its routes; frontier cards are never dealt from routes, they escalate to the coordinator",
		Fields: []Field{
			{Name: "tier", Type: TypeEnum, Enum: RouteTiers, Required: true, Help: "the tier it serves: one of " + strings.Join(RouteTiers, ", ") + " (frontier cards are never drawn from routes, they escalate to the coordinator)"},
			{Name: "provider", Type: TypeText, Required: true, Help: "the provider word of the model id <provider>/<model> the harness is launched with: one word, no slash"},
			{Name: "model", Type: TypeText, Required: true, Help: "the model name after the provider, which may hold slashes (x-ai/grok-4); no blank"},
			{Name: "tokens", Type: TypeInt, Help: "the token budget per card; 0 (the default) is unmetered and the deadline is the only stop"},
			{Name: "deadline", Type: TypeInt, Required: true, Help: "the seconds a card on this route may run, above 0"},
			{Name: "enabled", Type: TypeBool, Default: "true", Help: "false takes it out of the deal; true (the default) keeps it in"},
			// The price sheet: optional, so a card's predicted cost can be worked
			// out from its tokens using the pricing configuration saved per route tuple.
			// Prices are USD per million tokens.
			{Name: cardcost.FieldInput, Type: TypeDecimal, Help: "USD per million uncached input tokens, a decimal like 0.30; empty (the default) when not known"},
			{Name: cardcost.FieldCacheRead, Type: TypeDecimal, Help: "USD per million cached input tokens read"},
			{Name: cardcost.FieldCacheWrite, Type: TypeDecimal, Help: "USD per million tokens written to the cache"},
			{Name: cardcost.FieldOutput, Type: TypeDecimal, Help: "USD per million output tokens"},
			{Name: cardcost.FieldReasoningAsOutput, Type: TypeBool, Default: "true", Help: "true (the default) bills reasoning tokens at the output price; false when the provider does not bill them apart"},
			{Name: cardcost.FieldLongContext, Type: TypeInt, Help: "the prompt size in tokens above which a request is priced at the long prices; 0 (the default) is none"},
			{Name: cardcost.FieldInputLong, Type: TypeDecimal, Help: "USD per million input tokens of a request above --" + cardcost.FieldLongContext},
			{Name: cardcost.FieldOutputLong, Type: TypeDecimal, Help: "USD per million output tokens of a request above --" + cardcost.FieldLongContext},
			{Name: cardcost.FieldRequest, Type: TypeDecimal, Help: "USD per request, on top of the tokens; empty when there is no fee"},
			{Name: cardcost.FieldBilling, Type: TypeEnum, Enum: cardcost.Billings, Default: cardcost.BillingMetered, Help: "how it is paid: metered (the default: per token) or plan (a subscription, so the predicted cost is the metered price of the same tokens)"},
			{Name: cardcost.FieldGateway, Type: TypeDecimal, Help: "the percent a gateway adds on top of the prices, a decimal like 5.5; empty when none"},
			{Name: cardcost.FieldSource, Type: TypeText, Help: "where the prices were read, free text (a URL)"},
			{Name: cardcost.FieldAsOf, Type: TypeText, Help: "the date the prices were read, YYYY-MM-DD"},
		},
		Check: checkRoute,
	},
	{
		// A tier's route array: the deal takes routes[index mod len] for each
		// card of the tier, the index a uint64 counter on the fleet table
		// (internal/sprint/route.go, tla/RouteIndex.tla).
		Name:  KindTier,
		Table: "tiers",
		Doc:   "a model tier's route array: the deal takes routes[index mod len] for each card of the tier, a route named twice taking two turns; one row each for " + strings.Join(RouteTiers, " and ") + ", created by migrate",
		Fields: []Field{
			{Name: "routes", Type: TypeSeq, Ref: KindRoute, Help: "the ordered comma list of the tier's routes, a name repeated for more turns; each an enabled route of the tier; empty takes the tier's enabled routes in name order"},
		},
		Seed:  RouteTiers,
		Check: checkTier,
	},
}

// checkFleet keeps both store endpoints explicit and safe to print, and
// checks that the loops directory is non-empty. The endpoints may be unset
// so an older fleet can migrate before an operator declares them; apply
// and inventory refuse incomplete endpoints.
func checkFleet(r Row) error {
	if raw := r.Fields["redis_port"]; raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("--redis_port wants an integer from 1 through 65535")
		}
	}
	if dir, ok := r.Fields["loops_dir"]; ok && dir == "" {
		if len(r.Fields) == 1 || (r.Fields["store"] == "" && r.Fields["coordinator"] == "" && r.Fields["redis_port"] == "" && r.Fields["pg_dsn"] == "") {
			return fmt.Errorf("--loops_dir cannot be empty; run: fleet set --loops-dir <path>")
		}
	}
	dsn, ok := r.Fields["pg_dsn"]
	if !ok || dsn == "" {
		return nil
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || strings.TrimPrefix(u.Path, "/") == "" {
		return fmt.Errorf("--pg_dsn wants a password-free postgres://user@host/database URI (port optional)")
	}
	if _, has := u.User.Password(); has {
		return fmt.Errorf("--pg_dsn carries a password; leave it out and deliver the password through NOVA_PG_PASSWORD_ENV")
	}
	if port := u.Port(); port != "" || strings.HasSuffix(u.Host, ":") {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("--pg_dsn wants a TCP port from 1 through 65535 when a port is supplied")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("--pg_dsn wants a valid URI query with percent-encoded values and & between parameters; leave passwords out and deliver them through NOVA_PG_PASSWORD_ENV")
	}
	for key := range query {
		if strings.EqualFold(key, "password") {
			return fmt.Errorf("--pg_dsn carries a password; leave it out and deliver the password through NOVA_PG_PASSWORD_ENV")
		}
	}
	return nil
}

// checkTier is the tier kind's Check: the row is one of RouteTiers.
func checkTier(r Row) error {
	if !hasWord(strings.Join(RouteTiers, ","), r.Name) {
		return fmt.Errorf("tier %s: want one of %s", r.Name, strings.Join(RouteTiers, ", "))
	}
	return nil
}

// checkRoute is the route kind's Check: the provider is one word with no
// slash or blank, the model has no blank, and the deadline is above 0. A
// field absent from the row (refused on its own, or a required one not
// given) is skipped, so its own refusal stands alone.
func checkRoute(r Row) error {
	var problems []string
	if p, ok := r.Fields["provider"]; ok && (p == "" || strings.ContainsFunc(p, func(c rune) bool { return c == '/' || unicode.IsSpace(c) })) {
		problems = append(problems, fmt.Sprintf("route %s has --provider %q; want the provider word of the model id <provider>/<model>: one word, no slash, no blank", r.Name, p))
	}
	if m, ok := r.Fields["model"]; ok && (m == "" || strings.ContainsFunc(m, unicode.IsSpace)) {
		problems = append(problems, fmt.Sprintf("route %s has --model %q; want the model name after the provider, not empty and with no blank", r.Name, m))
	}
	if _, ok := r.Fields["deadline"]; ok && r.Int("deadline") <= 0 {
		problems = append(problems, fmt.Sprintf("route %s has --deadline 0; want the seconds a card on it may run, above 0", r.Name))
	}
	// the long prices go with the threshold: one without the other prices nothing
	if _, ok := r.Fields[cardcost.FieldLongContext]; ok {
		long := r.Int(cardcost.FieldLongContext) > 0
		for _, f := range []string{cardcost.FieldInputLong, cardcost.FieldOutputLong} {
			v, given := r.Fields[f]
			switch {
			case given && long && v == "":
				problems = append(problems, fmt.Sprintf("route %s has --%s %s and no --%s; want both long prices with the threshold, or --%s 0", r.Name, cardcost.FieldLongContext, r.Fields[cardcost.FieldLongContext], f, cardcost.FieldLongContext))
			case given && !long && v != "":
				problems = append(problems, fmt.Sprintf("route %s has --%s %s and no --%s; want the prompt size in tokens above which it applies", r.Name, f, v, cardcost.FieldLongContext))
			}
		}
	}
	if d := r.Fields[cardcost.FieldAsOf]; d != "" {
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			problems = append(problems, fmt.Sprintf("route %s has --%s %q; want the date the prices were read, YYYY-MM-DD", r.Name, cardcost.FieldAsOf, d))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// LoopLog is where a loop's unit writes its output on its machine, derived
// from the fleet row's loops_dir and the loop's name: <loops_dir>/<name>.log. apply
// writes it into the loop's Redis hash beside the row's fields.
func LoopLog(dir, name string) string {
	return strings.TrimRight(dir, "/") + "/" + name + ".log"
}

// LoopCommand is the command a loop's unit runs: its argv, with the width
// field as the value of its --width when the field is above 0, so a loop's
// width is set as one value (loop set <name> --width <n>) and never by
// editing the argv. The last spelling of the flag (--width n, -width n,
// --width=n, -width=n) after the program word and before a --, the one a
// flag parser keeps, takes the value; an argv with none gets --width n
// before its -- or at its end. A
// width of 0 leaves the argv as written, so a row whose argv carries --width
// and whose field is 0 keeps its own (migration 0013 sets the field from
// the argv). The inventory renders nova_loops' argv with it (parseLoop), and
// loop show prints it as command=.
func LoopCommand(argv []string, width int) []string {
	out := append([]string{}, argv...)
	if width <= 0 || len(out) == 0 {
		return out
	}
	n := strconv.Itoa(width)
	end := len(out)
	for i := 1; i < len(out); i++ {
		if out[i] == "--" {
			end = i
			break
		}
	}
	last, prefix := -1, "" // the index of the last spelling's value, and what precedes the value there
	for i := 1; i < end; i++ {
		switch t := out[i]; {
		case (t == "--width" || t == "-width") && i+1 < end:
			last, prefix = i+1, ""
			i++
		case strings.HasPrefix(t, "--width=") || strings.HasPrefix(t, "-width="):
			last, prefix = i, t[:strings.IndexByte(t, '=')+1]
		}
	}
	if last < 0 {
		return append(out[:end:end], append([]string{"--width", n}, out[end:]...)...)
	}
	out[last] = prefix + n
	return out
}

// checkLoop is the loop kind's Check: exactly one of every and keepalive
// says how it runs, and secret names need a seat to open them from.
func checkLoop(r Row) error {
	var problems []string
	// A field absent from the row failed its own validation in add; a rule
	// that needs it is skipped and the field's refusal stands alone.
	_, everyOK := r.Fields["every"]
	_, keepOK := r.Fields["keepalive"]
	if everyOK && keepOK {
		periodic := r.Int("every") > 0
		kept := r.Fields["keepalive"] == "true"
		switch {
		case periodic && kept:
			problems = append(problems, fmt.Sprintf("loop %s has --every %s and --keepalive true; a loop runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false", r.Name, r.Fields["every"]))
		case !periodic && !kept:
			problems = append(problems, fmt.Sprintf("loop %s has neither --every nor --keepalive; want --every <seconds> for a periodic loop or --keepalive true for a long-running one", r.Name))
		}
	}
	_, keysOK := r.Fields["keys"]
	_, seatOK := r.Fields["seat"]
	if keysOK && seatOK && r.Fields["keys"] != "" && r.Fields["seat"] == "" {
		problems = append(problems, fmt.Sprintf("loop %s names secrets (--keys %s) and no --seat to open them from; want --seat <seat>", r.Name, r.Fields["keys"]))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// deriveCoordinator is the friend kind's Derive: the sprint row's
// coordinator gets the coordinator role in the rows apply writes, so
// friend:<f>:roles in Redis (what ns_friend_roles guards and the deal
// reads) carries exactly one coordinator, and a handover (sprint set
// --coordinator) is two SET lines on the next apply: the new coordinator's
// roles first (ApplyOrder), then the former coordinator's without the coordinator role.
func deriveCoordinator(ctx context.Context, st Store, rows []Row) ([]Row, error) {
	sprint, _, err := st.Get(ctx, KindSprint, KindSprint)
	if err != nil {
		return nil, err
	}
	who := sprint.Fields["coordinator"]
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		r = r.Clone()
		if r.Name == who {
			words, _ := splitList(r.Fields["roles"] + "," + CoordinatorRole)
			r.Fields["roles"] = strings.Join(words, ",")
		}
		out = append(out, r)
	}
	return out, nil
}

// Lookup finds a kind by name.
func Lookup(name string) (*Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// KindNames lists the kinds in apply order.
func KindNames() []string {
	out := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}

// Field finds a field by name.
func (k *Kind) Field(name string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FieldNames lists the fields in declaration order.
func (k *Kind) FieldNames() []string {
	out := make([]string, 0, len(k.Fields))
	for _, f := range k.Fields {
		out = append(out, f.Name)
	}
	return out
}

// Row is one row of a kind: its name and every field as canonical text
// (ints as digits, lists sorted and comma joined, "" for an empty value).
// Values are text end to end so the descriptor, not the row, knows the type.
type Row struct {
	Name   string
	Fields map[string]string
	// CreatedAt and UpdatedAt are RFC 3339 UTC, "" for a row a store has not
	// stamped (a row built from flags).
	CreatedAt, UpdatedAt string
}

// Clone copies a row.
func (r Row) Clone() Row {
	out := Row{Name: r.Name, Fields: make(map[string]string, len(r.Fields)), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	maps.Copy(out.Fields, r.Fields)
	return out
}

// Int reads an int field of a canonical row (0 when absent).
func (r Row) Int(field string) int {
	n, _ := strconv.Atoi(r.Fields[field])
	return n
}

// ValidateName checks a row key.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("the name is required: the first argument after the verb")
	}
	if !NamePattern.MatchString(name) {
		return fmt.Errorf("name %q: want lower-case letters, digits and dashes, starting with a letter or digit", name)
	}
	return nil
}

// Canonical validates one field's raw value and returns its canonical text:
// an int as digits, an enum as its word, a list deduplicated and sorted, a
// wake path in its one spelling. The error names the field and what it wants.
func (f Field) Canonical(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch f.Type {
	case TypeText:
		if strings.ContainsAny(raw, "\n\r") {
			return "", fmt.Errorf("--%s: want one line", f.Name)
		}
		return raw, nil
	case TypeInt:
		if raw == "" {
			return "", fmt.Errorf("--%s: want a non-negative integer", f.Name)
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return "", fmt.Errorf("--%s %q: want a non-negative integer", f.Name, raw)
		}
		return strconv.Itoa(n), nil
	case TypeEnum:
		for _, w := range f.Enum {
			if raw == w {
				return raw, nil
			}
		}
		return "", fmt.Errorf("--%s %q: want one of %s", f.Name, raw, strings.Join(f.Enum, ", "))
	case TypeList:
		words, err := splitList(raw)
		if err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		for _, w := range words {
			if !hasWord(strings.Join(f.Enum, ","), w) {
				return "", fmt.Errorf("--%s %q: want a comma list of %s", f.Name, raw, strings.Join(f.Enum, ", "))
			}
		}
		return strings.Join(words, ","), nil
	case TypeNames:
		words, err := splitList(raw)
		if err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		for _, w := range words {
			if !namesPattern.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of names (letters, digits and dashes)", f.Name, w)
			}
		}
		return strings.Join(words, ","), nil
	case TypeBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("--%s %q: want true or false", f.Name, raw)
		}
		return strconv.FormatBool(b), nil
	case TypeKeys:
		words, err := splitList(raw)
		if err != nil {
			return "", fmt.Errorf("--%s: %v (a secret goes by name, never by value)", f.Name, err)
		}
		for _, w := range words {
			if !keyPattern.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of variable names (letters, digits and underscores, not starting with a digit)", f.Name, w)
			}
		}
		return strings.Join(words, ","), nil
	case TypeArgv:
		return canonicalArgv(f.Name, raw)
	case TypeDecimal:
		c, err := cardcost.Canonical(raw)
		if err != nil {
			return "", fmt.Errorf("--%s %v", f.Name, err)
		}
		return c, nil
	case TypeSeq:
		var words []string
		for _, w := range strings.Split(raw, ",") {
			if w = strings.TrimSpace(w); w == "" && raw != "" || w != "" && !NamePattern.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of %s names in order", f.Name, raw, f.Ref)
			} else if w != "" {
				words = append(words, w)
			}
		}
		return strings.Join(words, ","), nil
	case TypeRef:
		if raw == "" {
			if f.Required {
				return "", fmt.Errorf("--%s: want the name of a %s row", f.Name, f.Ref)
			}
			return "", nil
		}
		if err := ValidateName(raw); err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		return raw, nil
	}
	return "", fmt.Errorf("--%s: unknown field type %q", f.Name, f.Type)
}

// canonicalArgv validates a command given as a JSON array of strings and
// returns its compact JSON spelling: at least the program, a non-empty
// program, no line break or NUL in any word, at most MaxArgs words and
// MaxArgvBytes bytes.
func canonicalArgv(field, raw string) (string, error) {
	const want = `want the command as a JSON array of strings, the program first, like '["/path/prog","--flag","v"]'`
	if !strings.HasPrefix(raw, "[") {
		return "", fmt.Errorf("--%s: %s", field, want)
	}
	var words []string
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&words); err != nil || dec.More() {
		return "", fmt.Errorf("--%s: %s", field, want)
	}
	switch {
	case len(words) == 0 || words[0] == "":
		return "", fmt.Errorf("--%s: the command names no program; %s", field, want)
	case len(words) > MaxArgs:
		return "", fmt.Errorf("--%s: %d words, over the maximum of %d", field, len(words), MaxArgs)
	}
	for i, w := range words {
		if strings.ContainsAny(w, "\n\r\x00") {
			return "", fmt.Errorf("--%s: word %d holds a line break or a NUL; want one line per word", field, i)
		}
	}
	out, err := marshalArgv(words)
	if err != nil {
		return "", fmt.Errorf("--%s: %v", field, err)
	}
	if len(out) > MaxArgvBytes {
		return "", fmt.Errorf("--%s: %d bytes, over the maximum of %d", field, len(out), MaxArgvBytes)
	}
	return string(out), nil
}

// marshalArgv is the compact JSON spelling of the words with & < > written as
// themselves: json.Marshal turns them into \u0026 \u003c \u003e, which makes
// a command like '["sh","-c","a && b > c"]' unreadable in list and show.
func marshalArgv(words []string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(words); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// LoopCommandText is a loop row's command (LoopCommand of its argv and
// width) in the argv's own canonical JSON text: what loop show prints as
// command=, the words the unit runs.
func LoopCommandText(row Row) (string, error) {
	raw, err := marshalArgv(LoopCommand(Argv(row.Fields["argv"]), row.Int("width")))
	if err != nil {
		return "", fmt.Errorf("loop %s: render its command: %w", row.Name, err)
	}
	return string(raw), nil
}

// Argv decodes a canonical TypeArgv value ("" is none).
func Argv(canonical string) []string {
	var words []string
	if canonical == "" || json.Unmarshal([]byte(canonical), &words) != nil {
		return nil
	}
	return words
}

// splitList splits a comma (or space) list into sorted, deduplicated words.
func splitList(raw string) ([]string, error) {
	seen := map[string]bool{}
	var words []string
	for _, w := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if strings.ContainsAny(w, "=") {
			return nil, fmt.Errorf("%q: a list word holds no =", w)
		}
		if !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	sort.Strings(words)
	return words, nil
}

// hasWord reports whether the comma list holds the word.
func hasWord(list, word string) bool {
	return slices.Contains(strings.Split(list, ","), word)
}

// NewRow builds a canonical row of the kind from raw flag values: every
// field named in raw is validated, every required field must be present,
// and every problem is reported in one error so a first run is refused once
// (docs/ONBOARDING.md point 2). Fields not named are "" (an int, 0).
func (k *Kind) NewRow(name string, raw map[string]string) (Row, error) {
	var problems []string
	if err := ValidateName(name); err != nil {
		problems = append(problems, err.Error())
	}
	row := Row{Name: name, Fields: map[string]string{}}
	for _, f := range k.Fields {
		v, given := raw[f.Name]
		if !given {
			if f.Required {
				// Absent from the row, so a Check rule that reads it waits.
				problems = append(problems, fmt.Sprintf("--%s is required: %s", f.Name, f.Help))
				continue
			}
			row.Fields[f.Name] = f.zero()
			continue
		}
		c, err := f.Canonical(v)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		row.Fields[f.Name] = c
	}
	for name := range raw {
		if _, ok := k.Field(name); !ok {
			problems = append(problems, fmt.Sprintf("--%s is not a %s field; the fields are %s", name, k.Name, strings.Join(k.FieldNames(), ", ")))
		}
	}
	if k.Check != nil {
		if err := k.Check(row); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return Row{}, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return row, nil
}

// zero is the canonical value add stores for a field it is not given:
// Default when the field has one, else the type's zero.
func (f Field) zero() string {
	switch {
	case f.Default != "":
		return f.Default
	case f.Nullable:
		return ""
	case f.Type == TypeInt:
		return "0"
	case f.Type == TypeBool:
		return "false"
	}
	return ""
}

// Changes validates the named fields of a set and returns their canonical
// values. Every problem is reported at once.
func (k *Kind) Changes(raw map[string]string) (map[string]string, error) {
	var problems []string
	out := map[string]string{}
	for name, v := range raw {
		f, ok := k.Field(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("--%s is not a %s field; the fields are %s", name, k.Name, strings.Join(k.FieldNames(), ", ")))
			continue
		}
		c, err := f.Canonical(v)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out[name] = c
	}
	if len(out) == 0 && len(problems) == 0 {
		problems = append(problems, "set names no field; the fields are "+strings.Join(k.FieldNames(), ", "))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return out, nil
}

// Sorted returns the rows in apply order: Kind.ApplyOrder first, then name.
func (k *Kind) Sorted(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		if k.ApplyOrder != nil {
			a, b := k.ApplyOrder(out[i]), k.ApplyOrder(out[j])
			if a != b {
				return a < b
			}
		}
		return out[i].Name < out[j].Name
	})
	return out
}
