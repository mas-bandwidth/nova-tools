// Package acl is `nova-sprint acl check` (nova-tools #4333): the store's live
// Redis ACL (ACL LIST, read as the admin user) against the declared rows, one
// drift line per user whose rules differ. Before it, apply-acl.sh and
// fix-acl.py rewrote users by hand and nothing compared the running server
// with rowan-tools/fleet/redis.yml, so drift was silent until a NOPERM. The
// verb never writes: the play (rowan-tools/fleet/redis.yml, users.acl and
// ACL LOAD) is the only writer, and --fix is refused with its command.
//
// THE ROWS FILE (acl-rows.tsv) is the declared users, one per line:
//
//	<user> TAB <rules>
//
// <rules> is the rule list exactly as ACL SETUSER takes it after the
// password: rowan-tools/fleet/redis.yml's redis_users `rules` and one line of
// rowan-tools/fleet/templates/redis-acl.rules (whose `<name> <rules>` becomes
// `<name>\t<rules>`), plus the default user the play writes first. A leading
// `on` or `off` is the user's state (default on). A line starting with # and
// a blank line are skipped. A password token (>..., <..., #<hash>, !<hash>,
// nopass, resetpass) is refused: passwords live on the store, never in the
// rows. The play installs the file beside the server as DefaultRowsPath;
// testdata/acl-rows.tsv is the mirror of rowan-tools/fleet/redis.yml at rowan-tools
// main 2026-09-26.
//
// COMPARISON is by effective grant, not text: a server renders the rules it
// holds in its own form, and that form differs by version. redis-server 8.x
// prints them as written (`+@all -@dangerous +info`); 7.0 prints a
// compaction of its command bitmap (`+@all -@admin -flushall ... -swapdb`).
// So each side's command rules are applied in order over the SERVER'S OWN
// categories (ACL CAT, read in the same pipeline as ACL LIST) into the set of
// commands and subcommands the user may run; a first-argument grant such as
// `+fcall|ns_ping` stays a token of its own unless the whole command is
// granted. Key and channel patterns compare as tokens (`%RW~` read as `~`,
// `allkeys` as `~*`, `allchannels` as `&*`; the reset tokens dropped); a
// selector `( ... )` is one token of its own reduced grants. A drifted user
// prints the grants the live server lacks (missing) and the ones it holds
// beyond the rows (extra); a run of commands that is a whole category of the
// server prints as `+@<category>`.
package acl

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// DefaultRowsPath is where the play installs the rows file: the store's
// redis_dir (rowan-tools/fleet/redis.yml), beside users.acl.
const DefaultRowsPath = "/var/lib/nova-redis/acl-rows.tsv"

// PlayCommand converges the store's ACL: rowan-tools/fleet/Makefile `store`,
// which runs rowan-tools/fleet/redis.yml (users.acl from the declared users, ACL LOAD).
const PlayCommand = "make -C rowan-tools/fleet store"

// User is one user's rules: On is its state, Rules its normalized rule
// tokens in order (Reduce). Line is the rows file line (0 when live).
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

// Reduce turns one rules string into the user's state and its rule tokens
// in order, spellings normalized (see the package doc). declared refuses
// password tokens (a rows file never holds one); a live line's are dropped
// unread.
func Reduce(rules string, declared bool) (on bool, toks []string, err error) {
	raw, err := split(rules)
	if err != nil {
		return false, nil, err
	}
	on = declared // a declared user is on unless it says off; a live one says which
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
			n, keep, err := normalize(t)
			if err != nil {
				return false, nil, err
			}
			if keep {
				toks = append(toks, n)
			}
		}
	}
	return on, toks, nil
}

// normalize is one rule token in its compared spelling; keep is false for a
// reset token, which grants nothing. A selector's inner tokens are normalized
// in order and kept inside its parentheses.
func normalize(t string) (string, bool, error) {
	if strings.HasPrefix(t, "(") {
		var inner []string
		for _, f := range strings.Fields(strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")) {
			n, keep, err := normalize(f)
			if err != nil {
				return "", false, err
			}
			if keep {
				inner = append(inner, n)
			}
		}
		return "(" + strings.Join(inner, " ") + ")", true, nil
	}
	lt := strings.ToLower(t)
	switch {
	case lt == "reset" || lt == "resetkeys" || lt == "resetchannels":
		return "", false, nil
	case lt == "allkeys":
		return "~*", true, nil
	case lt == "allchannels":
		return "&*", true, nil
	case lt == "allcommands":
		return "+@all", true, nil
	case lt == "nocommands":
		return "-@all", true, nil
	case strings.HasPrefix(lt, "%rw~") || strings.HasPrefix(lt, "%wr~"):
		return "~" + t[4:], true, nil
	case strings.HasPrefix(t, "%"):
		i := strings.IndexByte(t, '~')
		if i < 0 {
			return "", false, fmt.Errorf("key rule %s has no ~", t)
		}
		return strings.ToUpper(t[:i]) + t[i:], true, nil
	case strings.HasPrefix(t, "~") || strings.HasPrefix(t, "&"):
		return t, true, nil
	case strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-"):
		return lt, true, nil
	}
	return "", false, fmt.Errorf("unknown rule %s", t)
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

// Diff compares the declared users with the live ones over the server's
// categories: declared users in rows order, then the undeclared live users
// by name.
func Diff(declared []User, live map[string]User, cats Cats) []Drift {
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
		want, have := cats.Grants(d.Rules), cats.Grants(l.Rules)
		dr.Missing, dr.Extra = cats.compress(minus(want, have)), cats.compress(minus(have, want))
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
