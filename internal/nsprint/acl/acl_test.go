package acl

import (
	"os"
	"strings"
	"testing"
)

// versions are the redis-server captures in testdata: the Studio's, the
// space runners' and the fleet store's (8.0.5), and hetzner's Ubuntu 7.0.15,
// which prints a compaction of its command bitmap instead of the rules.
var versions = []string{"8.10.2", "8.0.5", "7.0.15"}

func mirror(t *testing.T) []User {
	t.Helper()
	f, err := os.Open("testdata/acl-rows.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := ParseRows(f)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func capture(t *testing.T, version string) Server {
	t.Helper()
	f, err := os.Open("testdata/redis-" + version + ".acl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := ParseCapture(f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != version {
		t.Fatalf("capture says redis %s, file names %s", s.Version, version)
	}
	return s
}

func lines(ds []Drift) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Line())
	}
	return strings.Join(out, "\n")
}

// The mirror of rowan-tools/fleet/redis.yml parses: every declared user, the default one off.
func TestMirrorRowsParse(t *testing.T) {
	t.Parallel()

	rows := mirror(t)
	var names []string
	for _, u := range rows {
		names = append(names, u.Name)
		if u.On != (u.Name != "default") {
			t.Errorf("%s: on=%v", u.Name, u.On)
		}
	}
	want := "default bench coordinator viewer admin ns-friend ns-bench ns-reconciler ns-consumer ns-coordinator ns-table ns-deploy"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("users %q, want %q", got, want)
	}
}

// Each server's own rendering of the declared rules is no drift, whatever
// form that version prints: 8.x the rules as written, 7.0.15 a compaction
// (`+@all -@admin -flushall ...` for the coordinator's `+@all -@dangerous
// +info +config|get`), read over that server's own ACL CAT.
func TestEveryServerRenderingOfTheRowsIsNoDrift(t *testing.T) {
	t.Parallel()

	for _, v := range versions {
		s := capture(t, v)
		live, err := ParseList(s.List)
		if err != nil {
			t.Fatal(err)
		}
		if d := Diff(mirror(t), live, s.Cats); len(d) != 0 {
			t.Errorf("redis %s: want no drift, got:\n%s", v, lines(d))
		}
	}
	// The two renderings differ as text: the comparison is doing the work.
	a, b := capture(t, "8.10.2"), capture(t, "7.0.15")
	if strings.Join(a.List, "\n") == strings.Join(b.List, "\n") {
		t.Fatal("the 8.10.2 and 7.0.15 listings are the same text; the fixture no longer holds the case")
	}
}

// A hand edit on the live server is one line per user naming the missing and
// extra grants; a user off, a user gone and an undeclared user each print.
// The same edits read the same on every version.
func TestDriftLines(t *testing.T) {
	t.Parallel()

	want := strings.Join([]string{
		`ACL DRIFT user=bench missing="+hset ~cfg:deal" extra="+sort"`,
		`ACL DRIFT user=viewer state=off want=on`,
		`ACL DRIFT user=ns-friend missing="(+blpop ~friend:*:wake)" extra="(+blpop ~friend:*)"`,
		`ACL DRIFT user=ns-deploy absent=live`,
		`ACL DRIFT user=ghost absent=declared`,
	}, "\n")
	for _, v := range versions {
		s := capture(t, v)
		var ls []string
		for _, l := range s.List {
			switch {
			case strings.HasPrefix(l, "user bench "):
				l = strings.Replace(l, " ~cfg:deal", "", 1) + " +sort -hset"
			case strings.HasPrefix(l, "user ns-friend "):
				l = strings.Replace(l, "~friend:*:wake ", "~friend:* ", 1)
			case strings.HasPrefix(l, "user viewer "):
				l = strings.Replace(l, "user viewer on ", "user viewer off ", 1)
			case strings.HasPrefix(l, "user ns-deploy "):
				continue
			}
			ls = append(ls, l)
		}
		ls = append(ls, "user ghost on nopass ~* +@all")
		live, err := ParseList(ls)
		if err != nil {
			t.Fatal(err)
		}
		if got := lines(Diff(mirror(t), live, s.Cats)); got != want {
			t.Errorf("redis %s drift:\n%s\nwant:\n%s", v, got, want)
		}
	}
}

// Grants reads rule order and the server's categories; a whole category of
// drift prints as +@<category>.
func TestGrants(t *testing.T) {
	t.Parallel()

	c := capture(t, "8.10.2").Cats
	g := func(rules string) string {
		_, toks, err := Reduce(rules, true)
		if err != nil {
			t.Fatalf("%q: %v", rules, err)
		}
		return strings.Join(c.Grants(toks), " ")
	}
	for _, same := range [][2]string{
		{"allkeys allchannels allcommands", "~* &* +@all"},
		{"resetkeys resetchannels -@all +GET %RW~k", "+get ~k"},
		{"%r~k %W~j", "%R~k %W~j"},
		{"+get +get ~k ~k", "~k +get"},
		{"( ~a  +get )", "(~a resetchannels -@all +get)"},
		{"+@hash -hset", "+@hash -hset -hset"},
	} {
		if a, b := g(same[0]), g(same[1]); a != b {
			t.Errorf("%q grants %q, %q grants %q; want the same", same[0], a, same[1], b)
		}
	}
	// A whole command grants each of its subcommands.
	_, subs := c.index()
	whole := " " + g("+client") + " "
	for _, sub := range subs["client"] {
		if !strings.Contains(whole, " +"+sub+" ") {
			t.Errorf("+client does not grant %s: %q", sub, whole)
		}
	}
	if len(subs["client"]) < 2 {
		t.Fatalf("the capture names %d client subcommands", len(subs["client"]))
	}
	if a, b := g("+get -get"), g(""); a != b {
		t.Errorf("order: +get -get grants %q", a)
	}
	if got := g("+fcall|ns_ping"); got != "+fcall|ns_ping" {
		t.Errorf("a first-argument grant is its own token: %q", got)
	}
	if got := g("+fcall|ns_ping +fcall"); strings.Contains(got, "ns_ping") {
		t.Errorf("a first-argument grant under a whole grant is redundant: %q", got)
	}
	if got := strings.Join(c.compress(c.Grants([]string{"+@hash", "+get"})), " "); got != "+@hash +get" {
		t.Errorf("compress: %q", got)
	}
}

// Every refusal of the rows file names its line; a password never belongs there.
func TestRowsRefusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ rows, want string }{
		{"bench ~* +@all\n", "rows line 1: want <user> TAB <rules>"},
		{"bench\t~*\nbench\t~*\n", "rows line 2: user bench already declared on line 1"},
		{"bench\ton >secret ~*\n", "rows line 1 user bench: a password token is not a rule"},
		{"bench\ton #abc ~*\n", "rows line 1 user bench: a password token is not a rule"},
		{"bench\tnopass ~*\n", "rows line 1 user bench: a password token is not a rule"},
		{"bench\t(~a +get\n", "rows line 1 user bench: selector (~a +get is not closed"},
		{"bench\tfrobnicate\n", "rows line 1 user bench: unknown rule frobnicate"},
		{"# only a comment\n\n", "rows: no users declared"},
	} {
		_, err := ParseRows(strings.NewReader(c.rows))
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%q: err %v, want prefix %q", c.rows, err, c.want)
		}
	}
	if _, err := ParseList([]string{"bogus line"}); err == nil {
		t.Error("a listing line without `user` parsed")
	}
	if _, err := ParseCapture(strings.NewReader("== version\nredis_version:1\n")); err == nil {
		t.Error("a capture with no ACL LIST parsed")
	}
}
