// Package redisacl renders the fleet store's ACL users from the function
// library a build embeds and the key families its tools use, and compares a
// rendering with a store's live ACL. It is what `nova-redis acl` runs and
// what fleet/redis.yml converges a store with (docs/FLEET.md, "The store's
// ACL").
//
// A role is declared once, here: its user, the key families it reads or
// writes, the command categories it holds and the library files whose
// functions it may call. The function names are never listed: they are read
// out of the library (redisfn.Library.Registered) at render time, FCALL for
// each and FCALL_RO for each the library flags no-writes, so a function added
// to a file reaches every role of that file at the next render, and a file
// added to the library is refused until a role names it or it is declared the
// coordinator's alone (TestEveryLibraryFileHasItsRoles).
package redisacl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// The roles, by the name the CLI prints.
const (
	Coordinator = "coordinator"
	Member      = "member"
	Table       = "table"
	Friend      = "friend"
)

// Family is one family of keys the tools use, with the code that names
// them; render prints each with its source (ACL FAMILY lines).
type Family struct {
	Name     string
	Patterns []string
	From     string
}

// Families are the key families of the fleet store, each held to its
// owner's key functions by TestFamiliesAreTheOwnersKeys. A key a table cell
// is bound to (a binding may name any key outside table:) is in none of
// them unless it falls in one: only the coordinator reads such a key.
var Families = []Family{
	{"tables", []string{"table:*", "tables"}, "internal/ntable DefKey, ChangesKey, RowsKeyAt, Registry"},
	{"views", []string{"view:*", "views"}, "internal/ntable: a view's key and the views registry"},
	{"sprint", []string{"sprint:*"}, "internal/sprint Names.Key: the epoch, the beats, the tick's keys"},
	{"machines", []string{"machine:*", "machines"}, "internal/config MachineKey, MachineCeilingKey, MachinesKey"},
	{"beats", []string{"bench:*"}, "internal/config BeatKey: a machine's measured facts"},
	{"friends", []string{"friend:*", "friends", "friends:*"}, "internal/config FriendBeatKey, FriendsKey: a friend's beat, desired slots and roles"},
	{"fleet", []string{"fleet:*"}, "internal/config FleetKey"},
	{"loops", []string{"loops", "loop:*"}, "internal/config LoopsKey, LoopKey"},
	{"config", []string{"config:decl"}, "internal/config DeclKey"},
	{"tokens", []string{"tokens:ledger:*"}, "internal/record LedgerPrefix: nova-tokens ledger and report, logged in as the seat's user"},
	{"events", []string{"ev:github"}, "internal/ghevent/wire Stream: the CI run receipt (nova-ci github receipt, tools/ci/reportrun.go) XADDs it as the bench user"},
}

// keys is the key rules for families by access: "rw" families as ~<pattern>,
// "r" families as %R~<pattern>, in the order of Families.
func keys(access map[string]string) []string {
	var out []string
	for _, f := range Families {
		for _, p := range f.Patterns {
			switch access[f.Name] {
			case "rw":
				out = append(out, "~"+p)
			case "r":
				out = append(out, "%R~"+p)
			}
		}
	}
	return out
}

// The command rules, applied in order. A writer holds the data categories
// and loses every dangerous command and all of scripting (EVAL, SCRIPT,
// FUNCTION, FCALL) before its functions are granted one by one, so no role
// but the coordinator can load, delete or flush the library. A reader holds
// the read category only.
var (
	writerCommands = []string{"-@all", "+@read", "+@write", "+@keyspace", "+@connection", "+@transaction", "-@dangerous", "-@scripting"}
	readerCommands = []string{"-@all", "+@read", "+@connection", "-@dangerous", "-@scripting"}
)

// CoordinatorOnly are the library files no role but the coordinator calls:
// nova-config's apply writes the capacity and the friends' roles through
// them.
var CoordinatorOnly = []string{"lua/capacity.lua", "lua/friend_roles.lua"}

// Role is one ACL user's declaration.
type Role struct {
	// Name is the role; User the ACL user that holds it.
	Name, User string
	// Keys are the key rules: ~<pattern> to read and write, %R~<pattern> to
	// read.
	Keys []string
	// Commands are the command rules before the functions.
	Commands []string
	// Files are the library files whose functions it may call; every file
	// when All.
	Files []string
	All   bool
	// ReadOnly grants only the no-writes functions, through FCALL_RO.
	ReadOnly bool
	// Extra are command rules after the functions.
	Extra []string
}

// Roles are the four roles, in the order they render. The users are the
// tools' own defaults: the member's is the bench user every member loop
// logs in as (redisauth.NoUserHint), the table reader's and the friend's the
// ns- users the tools already name.
func Roles() []Role {
	member := []string{"lua/00_ping.lua", "lua/table.lua"}
	// Every store verb lists the library before it calls it (fn.LoadMissing,
	// the sprint store's open): FUNCTION LIST is every role's.
	list := []string{"+function|list"}
	read := map[string]string{"tables": "r", "views": "r", "sprint": "r", "machines": "r", "beats": "r", "friends": "r", "fleet": "r", "loops": "r", "config": "r", "tokens": "r", "events": "r"}
	with := func(over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range read {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	return []Role{
		{Name: Coordinator, User: "coordinator", Keys: []string{"~*"}, Commands: writerCommands, All: true,
			Extra: []string{"+function|load", "+function|list"}},
		{Name: Member, User: "bench", Keys: keys(with(map[string]string{"tables": "rw", "views": "rw", "sprint": "rw", "beats": "rw", "tokens": "rw", "events": "rw"})),
			Commands: writerCommands, Files: member, Extra: list},
		{Name: Table, User: "ns-table", Keys: keys(read), Commands: readerCommands, Files: []string{"lua/table.lua"}, ReadOnly: true, Extra: list},
		{Name: Friend, User: "ns-friend", Keys: keys(with(map[string]string{"tables": "rw", "views": "rw", "sprint": "rw", "friends": "rw", "tokens": "rw"})),
			Commands: writerCommands, Files: member, Extra: list},
	}
}

// User is one rendered ACL user: the arguments of ACL SETUSER after its
// name. A rendering never touches a password.
type User struct {
	Role, Name string
	Rules      []string
	// Functions is how many library functions it may call.
	Functions int
}

// Line is the user as a pasteable ACL SETUSER command.
func (u User) Line() string { return "ACL SETUSER " + u.Name + " " + strings.Join(u.Rules, " ") }

// Render is every role's user for the library. A role naming a file the
// library does not have is refused: a renamed file must not leave a role
// silently without its functions.
func Render(lib redisfn.Library) ([]User, error) {
	fns, err := lib.Registered()
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, f := range fns {
		files[f.File] = true
	}
	var out []User
	for _, r := range Roles() {
		wanted := map[string]bool{}
		for _, f := range r.Files {
			if !files[f] {
				return nil, fmt.Errorf("role %s names library file %s, which registers no function in this build", r.Name, f)
			}
			wanted[f] = true
		}
		rules := append([]string{"on", "clearselectors", "resetkeys", "resetchannels"}, r.Keys...)
		rules = append(rules, r.Commands...)
		n := 0
		for _, f := range fns {
			if !r.All && !wanted[f.File] {
				continue
			}
			switch {
			case r.ReadOnly && !f.NoWrites:
				continue
			case r.ReadOnly:
				rules = append(rules, "+fcall_ro|"+f.Name)
			case f.NoWrites:
				rules = append(rules, "+fcall|"+f.Name, "+fcall_ro|"+f.Name)
			default:
				rules = append(rules, "+fcall|"+f.Name)
			}
			n++
		}
		rules = append(rules, r.Extra...)
		out = append(out, User{Role: r.Name, Name: r.User, Rules: rules, Functions: n})
	}
	return out, nil
}

// Live is what a store's ACL GETUSER says of one user: never its passwords.
type Live struct {
	Exists bool
	On     bool
	// NoPass is the nopass flag: the user logs in with any password.
	NoPass bool
	// Keys, Channels and Commands are the reply's fields as the store
	// prints them; Selectors counts the user's selectors.
	Keys, Channels, Commands string
	Selectors                int
}

// Catalog is each command category and its commands, as ACL CAT prints them,
// read from the store a rendering is compared with: the categories mean
// what that store's version says they mean.
type Catalog map[string][]string

// Commands expands command rules applied in order into the commands they
// allow: +@<category> and -@<category> by the catalog, +@all (allcommands)
// and -@all (nocommands) over every category, +<command> and
// +<command>|<sub> as named, -<command> with every subcommand of it. Rules
// that are not command rules are skipped.
func (c Catalog) Commands(rules []string) map[string]bool {
	set := map[string]bool{}
	all := func(add bool) {
		for _, cmds := range c {
			for _, x := range cmds {
				if add {
					set[x] = true
				} else {
					delete(set, x)
				}
			}
		}
	}
	for _, r := range rules {
		r = strings.ToLower(r)
		switch {
		case r == "+@all" || r == "allcommands":
			all(true)
		case r == "-@all" || r == "nocommands":
			all(false)
			for x := range set {
				delete(set, x)
			}
		case strings.HasPrefix(r, "+@"):
			for _, x := range c[r[2:]] {
				set[x] = true
			}
		case strings.HasPrefix(r, "-@"):
			for _, x := range c[r[2:]] {
				delete(set, x)
			}
		case strings.HasPrefix(r, "+"):
			set[r[1:]] = true
		case strings.HasPrefix(r, "-"):
			for x := range set {
				if x == r[1:] || strings.HasPrefix(x, r[1:]+"|") {
					delete(set, x)
				}
			}
		}
	}
	return set
}

// Drift is how a live user differs from its rendering; zero is none.
type Drift struct {
	Missing, Off, Selectors  bool
	KeysAdd, KeysDel         []string
	ChannelsAdd, ChannelsDel []string
	CommandsAdd, CommandsDel []string
}

// None reports whether the live user is the rendering.
func (d Drift) None() bool {
	return !d.Missing && !d.Off && !d.Selectors && len(d.KeysAdd)+len(d.KeysDel)+len(d.ChannelsAdd)+len(d.ChannelsDel)+len(d.CommandsAdd)+len(d.CommandsDel) == 0
}

// Compare is the drift of a live user from its rendering, the commands
// compared as the sets the catalog expands both to, so a store that prints
// the same permissions in other words is no drift.
func Compare(want User, live Live, cat Catalog) Drift {
	if !live.Exists {
		return Drift{Missing: true}
	}
	var keys, chans, cmds []string
	for _, r := range want.Rules {
		switch {
		case strings.HasPrefix(r, "~") || strings.HasPrefix(r, "%"):
			keys = append(keys, r)
		case strings.HasPrefix(r, "&"):
			chans = append(chans, r)
		case strings.HasPrefix(r, "+") || strings.HasPrefix(r, "-"):
			cmds = append(cmds, r)
		}
	}
	d := Drift{Off: !live.On, Selectors: live.Selectors > 0}
	d.KeysAdd, d.KeysDel = diff(words(keys, normKey), words(strings.Fields(live.Keys), normKey))
	d.ChannelsAdd, d.ChannelsDel = diff(words(chans, nil), words(strings.Fields(live.Channels), nil))
	d.CommandsAdd, d.CommandsDel = diff(cat.Commands(cmds), cat.Commands(strings.Fields(live.Commands)))
	return d
}

// normKey writes a key rule the way the store prints it: %RW~ is ~.
func normKey(k string) string {
	if rest, ok := strings.CutPrefix(k, "%RW~"); ok {
		return "~" + rest
	}
	return k
}

func words(ws []string, norm func(string) string) map[string]bool {
	set := map[string]bool{}
	for _, w := range ws {
		if norm != nil {
			w = norm(w)
		}
		set[w] = true
	}
	return set
}

// diff is what want has and have lacks (to add), and what have has and want
// lacks (to remove), each sorted.
func diff(want, have map[string]bool) (add, del []string) {
	for w := range want {
		if !have[w] {
			add = append(add, w)
		}
	}
	for h := range have {
		if !want[h] {
			del = append(del, h)
		}
	}
	sort.Strings(add)
	sort.Strings(del)
	return add, del
}

// MaxShown bounds each list a drift line prints; the count of the rest
// follows.
const MaxShown = 8

// Fields is the drift as key=value fields for one line: what the apply
// would add (+=) and remove (-=), each list bounded by MaxShown.
func (d Drift) Fields() string {
	var b strings.Builder
	if d.Off {
		b.WriteString(" off=true")
	}
	if d.Selectors {
		b.WriteString(" selectors=true")
	}
	for _, f := range []struct {
		name string
		list []string
	}{
		{"keys+", d.KeysAdd}, {"keys-", d.KeysDel}, {"channels+", d.ChannelsAdd}, {"channels-", d.ChannelsDel},
		{"commands+", d.CommandsAdd}, {"commands-", d.CommandsDel},
	} {
		if len(f.list) == 0 {
			continue
		}
		shown := f.list
		more := ""
		if len(shown) > MaxShown {
			more = fmt.Sprintf(",+%d", len(shown)-MaxShown)
			shown = shown[:MaxShown]
		}
		fmt.Fprintf(&b, " %s=%s%s", f.name, strings.Join(shown, ","), more)
	}
	return b.String()
}
