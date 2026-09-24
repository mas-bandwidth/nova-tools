package reader_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reader"
)

// Fixture heads are full 40-hex shas. A shared prefix is not the same head.
const (
	repo  = "fixture/repo"
	headA = "4139b79f4139b79f4139b79f4139b79f4139b79f"
	headB = "99a8814999a8814999a8814999a8814999a88149"
	headW = "fc2c25d3fc2c25d3fc2c25d3fc2c25d3fc2c25d3"
	// headA12 shares headA's first 12 hex and differs after that.
	headA12 = "4139b79f4139aaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func up(name string, load int) reader.Friend {
	return reader.Friend{
		Name: name, State: "up", MayHold: true,
		Desired: 8, Starting: 0, Living: 1, Load: load,
	}
}

func logins() []reader.Login {
	return []reader.Login{
		{Login: "rowan-claude", Friend: "rowan"},
		{Login: "gafferongames", Friend: "johnny"},
		{Login: "gafferongames", Friend: "emma"},
		{Login: "gafferongames", Friend: "stella"},
	}
}

func base(pr int, head string, friends []reader.Friend) reader.Request {
	return reader.Request{
		Repo: repo, PR: pr, Head: head,
		AuthorLogin: "rowan-claude", Logins: logins(),
		Friends: friends,
	}
}

// TestControl44 is control 44: the author is never chosen, nor is a friend
// who is down, out of credits, or away. When nobody remains the line is
// NO-READER <pr> and no task is assigned. The reader who is chosen is the
// least-loaded may-hold friend with free width.
func TestControl44(t *testing.T) {
	t.Run("author and down excluded", func(t *testing.T) {
		stella := up("stella", 0)
		stella.State = "down"
		req := base(3050, headA, []reader.Friend{
			up("rowan", 0),
			stella,
			up("johnny", 4),
		})
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Assign() || got.Friend != "johnny" {
			t.Fatalf("choice=%+v, want johnny assigned", got)
		}
		if len(got.Lines) != 0 {
			t.Fatalf("lines=%q, want none when a reader remains", got.Lines)
		}
	})

	t.Run("out of credits and away excluded", func(t *testing.T) {
		emma := up("emma", 0)
		emma.State = "out-of-credits"
		away := up("stella", 0)
		away.State = "away"
		req := base(3051, headA, []reader.Friend{emma, away, up("johnny", 3)})
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Friend != "johnny" || !got.Assign() {
			t.Fatalf("choice=%+v, want johnny", got)
		}
	})

	t.Run("nobody remains", func(t *testing.T) {
		stella := up("stella", 0)
		stella.State = "down"
		emma := up("emma", 0)
		emma.State = "out-of-credits"
		away := up("johnny", 0)
		away.State = "away"
		req := base(3052, headA, []reader.Friend{up("rowan", 0), stella, emma, away})
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Assign() || got.Friend != "" {
			t.Fatalf("choice=%+v, want no task", got)
		}
		if len(got.Lines) != 1 || got.Lines[0] != "NO-READER 3052" {
			t.Fatalf("lines=%q, want NO-READER 3052", got.Lines)
		}
	})

	t.Run("least-loaded may-hold with free width", func(t *testing.T) {
		busy := up("johnny", 5)
		busy.State = "underfull"
		light := up("emma", 1)
		light.State = "idle"
		noWidth := up("stella", 0)
		noWidth.Desired = 1
		noWidth.Living = 1
		noHold := up("glenn", 0)
		noHold.MayHold = false
		req := base(3053, headA, []reader.Friend{busy, noWidth, noHold, light})
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Friend != "emma" {
			t.Fatalf("friend=%q, want least-loaded emma; choice=%+v", got.Friend, got)
		}
	})

	t.Run("shared login is not an author guess", func(t *testing.T) {
		req := base(3054, headA, []reader.Friend{up("johnny", 2), up("emma", 1), up("stella", 3)})
		req.AuthorLogin = "gafferongames"
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Friend != "emma" {
			t.Fatalf("friend=%q, want emma; a shared login must exclude nobody", got.Friend)
		}
	})
}

// TestControl61 is control 61: a friend with a closed read of the same repo,
// PR and full head, or a typed line at that head, gets no new task. The line
// says DEDUP and the reason. A friend with neither gets the one task. A
// different full head does not dedup, including one that shares a prefix.
func TestControl61(t *testing.T) {
	t.Run("closed read and typed line", func(t *testing.T) {
		req := base(3073, headA, []reader.Friend{
			up("stella", 0),
			up("emma", 1),
			up("johnny", 2),
		})
		req.Tasks = []reader.Task{{
			ID: "read-3073-4139b79f", Friend: "stella", Repo: repo, PR: 3073,
			Head: headA, State: "closed",
		}}
		req.Lines = []reader.Line{{
			ID: "emma@99a8814999a8", Friend: "emma", Repo: repo, PR: 3073, Head: headA,
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Assign() || got.Friend != "johnny" {
			t.Fatalf("choice=%+v, want one new task for johnny", got)
		}
		want := []string{
			"DEDUP read-3073-4139b79f: closed same-head read",
			"DEDUP emma@99a8814999a8: typed line at head",
		}
		if len(got.Lines) != len(want) {
			t.Fatalf("lines=%q, want %q", got.Lines, want)
		}
		for i := range want {
			if got.Lines[i] != want[i] {
				t.Fatalf("line %d=%q, want %q", i, got.Lines[i], want[i])
			}
		}
	})

	t.Run("working task", func(t *testing.T) {
		req := base(3075, headW, []reader.Friend{up("stella", 0), up("johnny", 1)})
		req.Tasks = []reader.Task{{
			ID: "read-3075-fc2c25d3", Friend: "stella", Repo: repo, PR: 3075,
			Head: headW, State: "working",
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Friend != "johnny" {
			t.Fatalf("friend=%q, want johnny", got.Friend)
		}
		if len(got.Lines) != 1 || got.Lines[0] != "DEDUP read-3075-fc2c25d3: working task at head" {
			t.Fatalf("lines=%q", got.Lines)
		}
	})

	t.Run("other head is not this read", func(t *testing.T) {
		req := base(3074, headB, []reader.Friend{up("stella", 0), up("emma", 2)})
		req.Tasks = []reader.Task{{
			ID: "read-3074-old", Friend: "stella", Repo: repo, PR: 3074,
			Head: headA, State: "closed",
		}}
		req.Lines = []reader.Line{{
			ID: "stella-old", Friend: "stella", Repo: repo, PR: 3074, Head: headA12,
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Friend != "stella" || len(got.Lines) != 0 {
			t.Fatalf("choice=%+v, want stella with no DEDUP", got)
		}
	})

	t.Run("same prefix is not the full head", func(t *testing.T) {
		req := base(3074, headA12, []reader.Friend{up("stella", 0)})
		req.Tasks = []reader.Task{{
			ID: "read-3074-prefix", Friend: "stella", Repo: repo, PR: 3074,
			Head: headA, State: "closed",
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Assign() || got.Friend != "stella" || len(got.Lines) != 0 {
			t.Fatalf("choice=%+v, want stella; a shared prefix is not a dedup", got)
		}
	})

	t.Run("other repo is not this read", func(t *testing.T) {
		req := base(3073, headA, []reader.Friend{up("stella", 0)})
		req.Tasks = []reader.Task{{
			ID: "read-other", Friend: "stella", Repo: "fixture/other", PR: 3073,
			Head: headA, State: "closed",
		}}
		req.Lines = []reader.Line{{
			ID: "other-line", Friend: "stella", Repo: "fixture/other", PR: 3073, Head: headA,
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Assign() || got.Friend != "stella" || len(got.Lines) != 0 {
			t.Fatalf("choice=%+v, want stella; identity includes the repo", got)
		}
	})

	t.Run("only candidate already holds it", func(t *testing.T) {
		req := base(3073, headA, []reader.Friend{up("stella", 0)})
		req.Lines = []reader.Line{{
			Friend: "stella", Repo: repo, PR: 3073, Head: stringsUpper(headA),
		}}
		got, err := reader.Choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Assign() {
			t.Fatalf("choice=%+v, want no new task", got)
		}
		wantTyped := "DEDUP disp:stella@" + headA + ": typed line at head"
		if len(got.Lines) != 2 || got.Lines[0] != wantTyped || got.Lines[1] != "NO-READER 3073" {
			t.Fatalf("lines=%q, want DEDUP then NO-READER", got.Lines)
		}
	})
}

func stringsUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
