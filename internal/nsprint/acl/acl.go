// Package acl is `nova-sprint acl check` (nova-tools #4333): the store's live
// Redis ACL (ACL LIST, read as the admin user) against the declared rows, one
// drift line per user whose rules differ. Before it, apply-acl.sh and
// fix-acl.py rewrote users by hand and nothing compared the running server
// with rowan-tools fleet/redis.yml, so drift was silent until a NOPERM. The
// verb never writes: the play (rowan-tools fleet/redis.yml, users.acl and
// ACL LOAD) is the only writer, and --fix is refused with its command.
//
// THE ROWS FILE (acl-rows.tsv) is the declared users, one per line:
//
//	<user> TAB <rules>
//
// <rules> is the rule list exactly as ACL SETUSER takes it after the
// password: fleet/redis.yml's redis_users `rules` and one line of
// fleet/templates/redis-acl.rules (whose `<name> <rules>` becomes
// `<name>\t<rules>`), plus the default user the play writes first. A leading
// `on` or `off` is the user's state (default on). A line starting with # and
// a blank line are skipped. A password token (>..., <..., #<hash>, !<hash>,
// nopass, resetpass) is refused: passwords live on the store, never in the
// rows. The play installs the file beside the server as DefaultRowsPath;
// testdata/acl-rows.tsv is the mirror of fleet/redis.yml at rowan-tools
// main 2026-09-26.
//
// COMPARISON is by rule tokens, not text: ACL LIST prints the rules in its own
// canonical form (key patterns first, `%RW~` as `~`, `resetchannels` and a
// leading `-@all` spelled out, `allkeys` as `~*`), so both sides are reduced
// to one token set per user. The reset tokens and a user's leading `-@all`
// (the state a new user starts in) carry no grant and are dropped; `allkeys`,
// `allchannels`, `allcommands` and `nocommands` become `~*`, `&*`, `+@all`
// and `-@all`; commands are lower-cased (keys and channels are not); a
// selector `( ... )` is one token of its own sorted, reduced inner tokens.
// A drifted user prints the tokens the live server lacks (missing) and the
// ones it holds beyond the rows (extra).
package acl

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// DefaultRowsPath is where the play installs the rows file: the store's
// redis_dir (fleet/redis.yml), beside users.acl.
const DefaultRowsPath = "/var/lib/nova-redis/acl-rows.tsv"

// PlayCommand converges the store's ACL: rowan-tools fleet/Makefile `store`,
// which runs fleet/redis.yml (users.acl from the declared users, ACL LOAD).
const PlayCommand = "make -C rowan-tools/fleet store"

// User is one user's rules reduced to tokens: On is its state, Rules its
// grant tokens sorted and unique. Line is the rows file line (0 when live).
type User struct {
	Name  string
	On    bool
	Rules []string
	Line  int
}

// ParseRows reads the rows file. Every refusal names the line.
func ParseRows(r io.Reader) ([]User, error) {
	var out []User
	seen := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rules, ok := strings.Cut(line, "\t")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("rows line %d: want <user> TAB <rules>", n)
		}
		if prev, dup := seen[name]; dup {
			return nil, fmt.Errorf("rows line %d: user %s already declared on line %d", n, name, prev)
		}
		seen[name] = n
		on, toks, err := Reduce(rules, true)
		if err != nil {
			return nil, fmt.Errorf("rows line %d user %s: %v", n, name, err)
		}
		out = append(out, User{Name: name, On: on, Rules: toks, Line: n})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("rows: %v", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rows: no users declared")
	}
	return out, nil
}

// ParseList reads ACL LIST's lines (`user <name> <rules...>`).
func ParseList(lines []string) (map[string]User, error) {
	out := map[string]User{}
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "user" {
			return nil, fmt.Errorf("ACL LIST line %d is not `user <name> <rules>`", i+1)
		}
		on, toks, err := Reduce(strings.Join(f[2:], " "), false)
		if err != nil {
			return nil, fmt.Errorf("ACL LIST user %s: %v", f[1], err)
		}
		out[f[1]] = User{Name: f[1], On: on, Rules: toks}
	}
	return out, nil
}

// Reduce turns one rules string into the user's state and its sorted, unique
// grant tokens. declared refuses password tokens (a rows file never holds
// one); a live line's are dropped unread.
func Reduce(rules string, declared bool) (on bool, toks []string, err error) {
	raw, err := split(rules)
	if err != nil {
		return false, nil, err
	}
	on = declared // a declared user is on unless it says off; a live one says which
	var grants []string
	for _, t := range raw {
		switch lt := strings.ToLower(t); {
		case lt == "on":
			on = true
		case lt == "off":
			on = false
		case lt == "nopass" || lt == "resetpass" || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "<") ||
			strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!"):
			if declared {
				return false, nil, fmt.Errorf("a password token is not a rule; passwords stay on the store")
			}
		case lt == "sanitize-payload" || lt == "skip-sanitize-payload":
			// a payload flag, not a grant
		default:
			grants = append(grants, t)
		}
	}
	toks, err = reduceGrants(grants)
	return on, toks, err
}

func reduceGrants(grants []string) ([]string, error) {
	set := map[string]bool{}
	first := true // the first command rule: a leading -@all is a new user's state
	for _, t := range grants {
		if strings.HasPrefix(t, "(") {
			inner, err := reduceGrants(strings.Fields(strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")))
			if err != nil {
				return nil, err
			}
			set["("+strings.Join(inner, " ")+")"] = true
			continue
		}
		lt := strings.ToLower(t)
		switch {
		case lt == "reset" || lt == "resetkeys" || lt == "resetchannels":
			continue
		case lt == "allkeys":
			t = "~*"
		case lt == "allchannels":
			t = "&*"
		case lt == "allcommands":
			t = "+@all"
		case lt == "nocommands":
			t = "-@all"
		case strings.HasPrefix(lt, "%rw~") || strings.HasPrefix(lt, "%wr~"):
			t = "~" + t[4:]
		case strings.HasPrefix(t, "%"):
			i := strings.IndexByte(t, '~')
			if i < 0 {
				return nil, fmt.Errorf("key rule %s has no ~", t)
			}
			t = strings.ToUpper(t[:i]) + t[i:]
		case strings.HasPrefix(t, "~") || strings.HasPrefix(t, "&"):
		case strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-"):
			t = lt
		default:
			return nil, fmt.Errorf("unknown rule %s", t)
		}
		if t[0] == '+' || t[0] == '-' {
			if first && t == "-@all" {
				first = false
				continue
			}
			first = false
		}
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// split is strings.Fields with a selector `( ... )` kept as one token.
func split(rules string) ([]string, error) {
	var out, sel []string
	for _, f := range strings.Fields(rules) {
		switch {
		case sel != nil:
			sel = append(sel, f)
			if strings.HasSuffix(f, ")") {
				out, sel = append(out, strings.Join(sel, " ")), nil
			}
		case strings.HasPrefix(f, "("):
			if strings.HasSuffix(f, ")") {
				out = append(out, f)
				continue
			}
			sel = []string{f}
		default:
			out = append(out, f)
		}
	}
	if sel != nil {
		return nil, fmt.Errorf("selector %s is not closed", strings.Join(sel, " "))
	}
	return out, nil
}

// Drift is one user whose live ACL differs from its row. Absent is "live"
// (declared, not on the server) or "declared" (on the server, not declared);
// State is the live state when it differs from the row's.
type Drift struct {
	User           string
	Absent         string
	State          string
	Missing, Extra []string
	declaredOn     bool
}

// Line is the drift line: `ACL DRIFT user=<u> ...`.
func (d Drift) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ACL DRIFT user=%s", d.User)
	if d.Absent != "" {
		fmt.Fprintf(&b, " absent=%s", d.Absent)
		return b.String()
	}
	if d.State != "" {
		want := "on"
		if !d.declaredOn {
			want = "off"
		}
		fmt.Fprintf(&b, " state=%s want=%s", d.State, want)
	}
	if len(d.Missing) > 0 {
		fmt.Fprintf(&b, " missing=%q", strings.Join(d.Missing, " "))
	}
	if len(d.Extra) > 0 {
		fmt.Fprintf(&b, " extra=%q", strings.Join(d.Extra, " "))
	}
	return b.String()
}

// Diff compares the declared users with the live ones: declared users in
// rows order, then the undeclared live users by name.
func Diff(declared []User, live map[string]User) []Drift {
	var out []Drift
	named := map[string]bool{}
	for _, d := range declared {
		named[d.Name] = true
		l, ok := live[d.Name]
		if !ok {
			out = append(out, Drift{User: d.Name, Absent: "live"})
			continue
		}
		dr := Drift{User: d.Name, declaredOn: d.On}
		if l.On != d.On {
			dr.State = "off"
			if l.On {
				dr.State = "on"
			}
		}
		dr.Missing, dr.Extra = minus(d.Rules, l.Rules), minus(l.Rules, d.Rules)
		if dr.State != "" || len(dr.Missing) > 0 || len(dr.Extra) > 0 {
			out = append(out, dr)
		}
	}
	var extra []string
	for name := range live {
		if !named[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		out = append(out, Drift{User: name, Absent: "declared"})
	}
	return out
}

// minus is a's tokens that b lacks, in a's (sorted) order.
func minus(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, t := range b {
		in[t] = true
	}
	var out []string
	for _, t := range a {
		if !in[t] {
			out = append(out, t)
		}
	}
	return out
}
