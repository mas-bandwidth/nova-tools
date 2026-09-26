package acl

import (
	"os"
	"strings"
	"testing"
)

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

// listing is ACL LIST from redis-server 8.10.2 after loading the mirror rows
// as a users.acl (the play's shape, test password "x").
func listing(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/acl-list-redis-8.10.2.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func lines(ds []Drift) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Line())
	}
	return strings.Join(out, "\n")
}

// The mirror of fleet/redis.yml parses: every declared user, the default one off.
func TestMirrorRowsParse(t *testing.T) {
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

// Redis's own rendering of the declared rules is no drift: canonical order,
// %RW~ as ~, resetchannels and a leading -@all spelled out, selectors included.
func TestRealListingOfTheRowsIsNoDrift(t *testing.T) {
	live, err := ParseList(listing(t))
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(mirror(t), live); len(d) != 0 {
		t.Fatalf("want no drift, got:\n%s", lines(d))
	}
}

// A hand edit on the live server is one line per user naming the missing and
// extra tokens; a user off, a user gone and an undeclared user each print.
func TestDriftLines(t *testing.T) {
	var ls []string
	for _, l := range listing(t) {
		switch {
		case strings.HasPrefix(l, "user bench "):
			l = strings.Replace(l, " ~cfg:deal", "", 1) + " +sort"
		case strings.HasPrefix(l, "user ns-friend "):
			l = strings.Replace(l, "(~friend:*:wake resetchannels -@all +blpop)", "(~friend:* resetchannels -@all +blpop)", 1)
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
	want := strings.Join([]string{
		`ACL DRIFT user=bench missing="~cfg:deal" extra="+sort"`,
		`ACL DRIFT user=viewer state=off want=on`,
		`ACL DRIFT user=ns-friend missing="(+blpop ~friend:*:wake)" extra="(+blpop ~friend:*)"`,
		`ACL DRIFT user=ns-deploy absent=live`,
		`ACL DRIFT user=ghost absent=declared`,
	}, "\n")
	if got := lines(Diff(mirror(t), live)); got != want {
		t.Fatalf("drift:\n%s\nwant:\n%s", got, want)
	}
}

func TestReduceSpellings(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"allkeys allchannels allcommands", "~* &* +@all"},
		{"resetkeys resetchannels -@all +GET %RW~k", "+get ~k"},
		{"%r~k %W~j", "%R~k %W~j"},
		{"+get +get ~k ~k", "+get ~k"},
		{"( ~a  +get )", "(+get ~a)"},
		{"(~a resetchannels -@all +get)", "(~a +get)"},
	} {
		_, x, err := Reduce(c.a, true)
		if err != nil {
			t.Fatalf("%q: %v", c.a, err)
		}
		_, y, err := Reduce(c.b, true)
		if err != nil {
			t.Fatalf("%q: %v", c.b, err)
		}
		if strings.Join(x, " ") != strings.Join(y, " ") {
			t.Errorf("%q -> %v, %q -> %v; want the same tokens", c.a, x, c.b, y)
		}
	}
	// -@all past the first command rule is a grant change, kept.
	if _, x, _ := Reduce("+get -@all", true); strings.Join(x, " ") != "+get -@all" {
		t.Errorf("a later -@all was dropped: %v", x)
	}
}

// Every refusal of the rows file names its line; a password never belongs there.
func TestRowsRefusals(t *testing.T) {
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
}
