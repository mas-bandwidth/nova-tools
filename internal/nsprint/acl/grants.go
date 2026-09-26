package acl

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Cats is a server's ACL categories, each category's commands and
// subcommands as ACL CAT <category> names them (`hset`, `client|kill`).
type Cats map[string][]string

// Server is what one check reads from the store in one pipeline pass: its
// version (INFO server), ACL LIST, and ACL CAT for every category.
type Server struct {
	Version string
	List    []string
	Cats    Cats
}

// Grants is the set a user's rules grant on this server, sorted: key and
// channel patterns as tokens, each selector as one token of its own grants,
// and the commands the command rules allow, applied in order over the
// server's categories, as `+<command>` (`+client|kill` for a subcommand). A
// command rule naming no command the server knows, and a first-argument
// grant (`+fcall|ns_ping`) of a command not wholly granted, stay tokens.
func (c Cats) Grants(rules []string) []string {
	universe, subs := c.index()
	allowed := map[string]bool{}
	lits := map[string]bool{}
	set := map[string]bool{}
	for _, t := range rules {
		switch {
		case strings.HasPrefix(t, "("):
			inner := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(t, "("), ")"))
			set["("+strings.Join(c.Grants(inner), " ")+")"] = true
			continue
		case t[0] != '+' && t[0] != '-':
			set[t] = true
			continue
		}
		grant, body := t[0] == '+', t[1:]
		var names []string
		switch {
		case body == "@all":
			names = universe
		case strings.HasPrefix(body, "@"):
			members, ok := c[body[1:]]
			if !ok {
				lit(lits, grant, t)
				continue
			}
			names = members
		case !strings.Contains(body, "|") && (inUniverse(universe, body) || len(subs[body]) > 0):
			if inUniverse(universe, body) {
				names = append(names, body)
			}
			names = append(names, subs[body]...)
			for l := range lits {
				if strings.HasPrefix(l, "+"+body+"|") {
					delete(lits, l)
				}
			}
		case inUniverse(universe, body):
			names = []string{body}
		default:
			lit(lits, grant, t)
			continue
		}
		for _, n := range names {
			if grant {
				allowed[n] = true
			} else {
				delete(allowed, n)
			}
		}
	}
	for n := range allowed {
		set["+"+n] = true
	}
	for l := range lits {
		if parent, _, ok := strings.Cut(l[1:], "|"); !ok || !allowed[parent] {
			set[l] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func lit(lits map[string]bool, grant bool, t string) {
	if grant {
		lits[t] = true
	} else {
		delete(lits, "+"+t[1:])
	}
}

// index is every command the categories name, sorted, and each command's
// subcommands.
func (c Cats) index() ([]string, map[string][]string) {
	seen := map[string]bool{}
	subs := map[string][]string{}
	for _, members := range c {
		for _, m := range members {
			if seen[m] {
				continue
			}
			seen[m] = true
			if parent, _, ok := strings.Cut(m, "|"); ok {
				subs[parent] = append(subs[parent], m)
			}
		}
	}
	all := make([]string, 0, len(seen))
	for m := range seen {
		all = append(all, m)
	}
	sort.Strings(all)
	for p := range subs {
		sort.Strings(subs[p])
	}
	return all, subs
}

func inUniverse(universe []string, name string) bool {
	i := sort.SearchStrings(universe, name)
	return i < len(universe) && universe[i] == name
}

// compress prints a run of `+<command>` tokens that is a whole category of
// the server (two commands or more) as `+@<category>`, largest category
// first, then name; everything else is left as it is. Display only.
func (c Cats) compress(toks []string) []string {
	have := map[string]bool{}
	for _, t := range toks {
		have[t] = true
	}
	names := make([]string, 0, len(c))
	for n := range c {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(c[names[i]]) != len(c[names[j]]) {
			return len(c[names[i]]) > len(c[names[j]])
		}
		return names[i] < names[j]
	})
	var out []string
	for _, n := range names {
		members := c[n]
		if len(members) < 2 {
			continue
		}
		whole := true
		for _, m := range members {
			if !have["+"+m] {
				whole = false
				break
			}
		}
		if !whole {
			continue
		}
		for _, m := range members {
			delete(have, "+"+m)
		}
		out = append(out, "+@"+n)
	}
	for t := range have {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ParseCapture reads a server capture: `# ...` comment lines, then
// `== version` and one `redis_version:<v>` line, `== list` and the ACL LIST
// lines, `== cat` and one `<category> TAB <command> <command>...` line per
// category. testdata holds one per redis-server version the check was held
// against.
func ParseCapture(r io.Reader) (Server, error) {
	s := Server{Cats: Cats{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	section := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "" || strings.HasPrefix(line, "# "):
		case strings.HasPrefix(line, "== "):
			section = line[3:]
		case section == "version":
			s.Version = strings.TrimPrefix(line, "redis_version:")
		case section == "list":
			s.List = append(s.List, line)
		case section == "cat":
			name, members, _ := strings.Cut(line, "\t")
			s.Cats[name] = strings.Fields(members)
		default:
			return Server{}, fmt.Errorf("capture line %q is outside a section", line)
		}
	}
	if err := sc.Err(); err != nil {
		return Server{}, err
	}
	if s.Version == "" || len(s.List) == 0 || len(s.Cats) == 0 {
		return Server{}, fmt.Errorf("capture needs a version, ACL LIST lines and ACL CAT lines")
	}
	return s, nil
}
