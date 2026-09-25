package sprint

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// foldSeat is one closed sprint in miniredis, seeded the way the card model
// keeps it: s:<S>:card:<label> records listed in sprint:<S>:cards, and the
// friend and Jev reads in s:<S>:disp:<repo>:<pr> at the card's head.
type foldSeat struct {
	t   *testing.T
	c   *redis.Client
	s   string
	n   int
	ctx context.Context
}

func newFoldSeat(t *testing.T, status string, policy ...string) *foldSeat {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	f := &foldSeat{t: t, c: c, s: "fold-s", ctx: context.Background()}
	if status != "" {
		c.HSet(f.ctx, "s:"+f.s, "status", status)
	}
	if len(policy) > 0 {
		c.HSet(f.ctx, "s:"+f.s+":policy", policy)
	}
	return f
}

// card seeds one card; reads are "<who>=<value>" disposition fields at its head.
func (f *foldSeat) card(label string, fields map[string]string, reads ...string) {
	f.n++
	head := fmt.Sprintf("c0de%04d", f.n)
	id := "s:" + f.s + ":card:" + label
	rec := map[string]string{"where": "done", "state": "landed", "kind": "code", "repo": "a", "pr": strconv.Itoa(f.n), "head": head}
	for k, v := range fields {
		rec[k] = v
	}
	f.c.HSet(f.ctx, id, rec)
	f.c.ZAdd(f.ctx, "sprint:"+f.s+":cards", redis.Z{Score: float64(1000 + f.n), Member: id})
	for _, r := range reads {
		who, v, _ := strings.Cut(r, "=")
		f.c.HSet(f.ctx, "s:"+f.s+":disp:"+rec["repo"]+":"+rec["pr"], who+"@"+head, v)
	}
}

func (f *foldSeat) fold() FoldReport {
	f.t.Helper()
	rep, refused, err := Fold(f.ctx, f.c, f.s, time.UnixMilli(1758800000000))
	if err != nil || refused != "" {
		f.t.Fatalf("Fold: refused=%q err=%v", refused, err)
	}
	return rep
}

func hasLine(t *testing.T, rep FoldReport, want string) {
	t.Helper()
	for _, l := range rep.Lines {
		if l == want {
			return
		}
	}
	t.Fatalf("no line %q in:\n%s", want, strings.Join(rep.Lines, "\n"))
}

func linesWith(rep FoldReport, prefix string) (out []string) {
	for _, l := range rep.Lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

const jev9 = "jev=APPROVE 9 u 1"

// TestSprintFoldFromRedis is the #2618 DONE-WHEN on the card model: the fold
// of a closed sprint read only from Redis prints the type candidates, the
// per-type score summary, the Jev calibration set and the cost per type with
// the next ceiling, and writes each on s:<S>:fold:<section>.
func TestSprintFoldFromRedis(t *testing.T) {
	t.Run("insufficient", func(t *testing.T) {
		f := newFoldSeat(t, "closed")
		for i := 0; i < 3; i++ {
			f.card(fmt.Sprintf("r%d", i), map[string]string{"kind": "read"})
		}
		rep := f.fold()
		if got := linesWith(rep, "FOLD TYPE-CAND"); len(got) != 1 || got[0] != "FOLD TYPE-CAND type=read field=repo value=a INSUFFICIENT reason=min_n n=3 rest_n=0 need=8" {
			t.Fatalf("TYPE-CAND lines %q", got)
		}
		hasLine(t, rep, "FOLD CEILING type=read ABSENT dep=#3104 field=cost_ceiling")
		hasLine(t, rep, "FOLD COST type=read closed=3 useful=0 ABSENT dep=card-end field=tok_in,usd retries=0")
		hasLine(t, rep, "FOLD JEV type=read INSUFFICIENT reason=min_n n=0 need=8 labeled=0 unlabeled=3 conflicts=0 ties=0 friend_unscored=0 jev_unscored=3")
		if rep.Proposals != 0 {
			t.Fatalf("proposals=%d, want 0", rep.Proposals)
		}
	})

	t.Run("candidate", func(t *testing.T) {
		f := newFoldSeat(t, "closed")
		for i := 0; i < 8; i++ {
			f.card(fmt.Sprintf("a%d", i), nil, "stella=APPROVE 9 u 1", jev9)
		}
		for i := 0; i < 8; i++ {
			read := "stella=HOLD 5 u 1"
			if i == 0 {
				read = "stella=APPROVE 9 u 1"
			}
			f.card(fmt.Sprintf("b%d", i), map[string]string{"repo": "b"}, read, jev9)
		}
		rep := f.fold()
		hasLine(t, rep, "FOLD TYPE-CAND type=code field=repo value=a useful=8/8 rest=1/8 ci=[0.676,1.000] rest_ci=[0.022,0.471] usd_per_useful=- min_n=8")
		hasLine(t, rep, "FOLD TYPE-CAND type=code field=repo value=b useful=1/8 rest=8/8 ci=[0.022,0.471] rest_ci=[0.676,1.000] usd_per_useful=- min_n=8 mirror=a")
		props := linesWith(rep, "FOLD PROPOSAL")
		if len(props) != 1 || rep.Proposals != 1 || !strings.Contains(props[0], "kind=type-rule type=code value=repo=a n=8 denominator=8 stat=wilson:0.676:1.000/0.022:0.471 cards=a0,") || strings.Count(props[0], ",")+1 != 16 {
			t.Fatalf("proposals %q", props)
		}
		hasLine(t, rep, "FOLD SCORE type=code type_src=kind cards=16 closed=16 landed=16 useful=9/16 labeled=16 mean=7.25 range=5-9 useful_min=8")
	})

	t.Run("labels", func(t *testing.T) {
		f := newFoldSeat(t, "closed", "useful_min", "8", "fold_min_n", "2")
		f.card("lab-conflict", nil, "stella=APPROVE 9 u 1", "johnny=HOLD 6 u 2", jev9)
		f.card("lab-jev-only", nil, jev9)
		f.card("lab-noscore", nil, "stella=APPROVE", "johnny=APPROVE 8 u 2", jev9)
		f.card("lab-slash", nil, "stella=APPROVE 9/10 u 1", jev9)
		f.card("lab-hold-noscore", nil, "stella=HOLD", "johnny=APPROVE 9 u 2", jev9)
		f.card("lab-zero", nil, "stella=APPROVE 0 u 1", jev9)
		f.card("lab-bare", nil, "stella=APPROVE", jev9)
		rep := f.fold()
		hasLine(t, rep, "FOLD JEV type=code false_pass=2/3 labeled=3 unlabeled=4 conflicts=1 ties=0 friend_unscored=4 jev_unscored=0")
		want := []string{
			"FOLD CALIB card=lab-conflict type=code score=6 who=johnny jev=9 head=c0de0001",
			"FOLD CALIB card=lab-noscore type=code score=8 who=johnny jev=9 head=c0de0003",
			"FOLD CALIB card=lab-zero type=code score=0 who=stella jev=9 head=c0de0006",
		}
		if got := linesWith(rep, "FOLD CALIB"); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("calibration rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		calib := f.c.HGetAll(f.ctx, FoldKey(f.s, "calib")).Val()
		if len(calib) != 3 || calib["lab-zero"] != "type=code score=0 who=stella jev=9 head=c0de0006" {
			t.Fatalf("s:%s:fold:calib = %v", f.s, calib)
		}
	})

	t.Run("tie", func(t *testing.T) {
		f := newFoldSeat(t, "closed", "useful_min", "8", "fold_min_n", "8")
		for i := 0; i < 8; i++ {
			f.card(fmt.Sprintf("a%d", i), nil, "stella=APPROVE 9 u 1", jev9)
		}
		f.card("code-tie", nil, "stella=APPROVE 9 u 1", "johnny=HOLD 9 u 2", jev9)
		for i := 0; i < 8; i++ {
			read := "stella=HOLD 5 u 1"
			if i == 0 {
				read = "stella=APPROVE 9 u 1"
			}
			f.card(fmt.Sprintf("b%d", i), map[string]string{"repo": "b"}, read, jev9)
		}
		rep := f.fold()
		hasLine(t, rep, "FOLD JEV type=code false_pass=7/16 labeled=16 unlabeled=0 conflicts=1 ties=1 friend_unscored=0 jev_unscored=0")
		hasLine(t, rep, "FOLD TYPE-CAND type=code field=repo value=a useful=8/9 rest=1/8 ci=[0.565,0.980] rest_ci=[0.022,0.471] usd_per_useful=- min_n=8")
		hasLine(t, rep, "FOLD TYPE-CAND type=code field=repo value=b useful=1/8 rest=8/9 ci=[0.022,0.471] rest_ci=[0.565,0.980] usd_per_useful=- min_n=8 mirror=a")
		props := linesWith(rep, "FOLD PROPOSAL")
		if len(props) != 1 || !strings.Contains(props[0], "code-tie") || strings.Count(props[0], ",")+1 != 17 {
			t.Fatalf("proposals %q", props)
		}
		if rep.Calib != 16 || strings.Contains(strings.Join(linesWith(rep, "FOLD CALIB"), "\n"), "code-tie") {
			t.Fatalf("calib=%d; the tie card must not enter the calibration set", rep.Calib)
		}
	})

	t.Run("cost", func(t *testing.T) {
		seed := func(f *foldSeat, blobless bool) {
			for i := 0; i < 9; i++ {
				fields := map[string]string{"usd": "0.40", "tok_in": "30000", "tok_out": "10000", "rates_blob": "b10b5eed0123456789"}
				read := "stella=APPROVE 9 u 1"
				if i == 8 {
					read, fields["attempt"] = "stella=HOLD 5 u 1", "2"
				}
				if blobless && i == 0 {
					delete(fields, "rates_blob")
				}
				f.card(fmt.Sprintf("c%d", i), fields, read, jev9)
			}
			f.c.HSet(f.ctx, "routes:code", "cost_ceiling", "0.50")
		}
		f := newFoldSeat(t, "closed")
		seed(f, false)
		rep := f.fold()
		cost := linesWith(rep, "FOLD COST")
		if len(cost) != 1 {
			t.Fatalf("cost lines %q", cost)
		}
		const want = "FOLD COST type=code closed=9 useful=8 tokens=360000 tokens_per_useful=45000.0 usd_per_mtok=10.0000 usd_per_useful=0.4500 retries=1 unmetered=0 unpriced=0 rates=b10b5eed"
		if cost[0] != want {
			t.Fatalf("cost\n%s\nwant\n%s", cost[0], want)
		}
		// tokens_per_useful x usd_per_token = usd_per_useful, to 4 dp.
		if got := fmt.Sprintf("%.4f", 45000.0*10.0/1e6); got != "0.4500" {
			t.Fatalf("identity: %s", got)
		}
		hasLine(t, rep, "FOLD CEILING type=code current=0.5000 next=0.4500 useful=8 min_n=8 p90=0.4000 verdict=propose")

		g := newFoldSeat(t, "closed")
		seed(g, true)
		rep = g.fold()
		hasLine(t, rep, "FOLD COST type=code closed=9 useful=8 tokens=360000 tokens_per_useful=45000.0 usd_per_mtok=- usd_per_useful=- retries=1 unmetered=0 unpriced=1 rates=b10b5eed")
		hasLine(t, rep, "FOLD CEILING type=code current=0.5000 next=0.4500 useful=8 min_n=8 INSUFFICIENT reason=unpriced n=1")
	})

	t.Run("record", func(t *testing.T) {
		f := newFoldSeat(t, "closed", "fold_min_n", "1")
		f.card("one", nil, "stella=APPROVE 9 u 1", jev9)
		f.c.HSet(f.ctx, FoldKey(f.s, "calib"), "stale", "from an earlier fold")
		first := f.fold()
		second := f.fold()
		if strings.Join(first.Lines, "\n") != strings.Join(second.Lines, "\n") {
			t.Fatal("a re-fold of the same sprint printed different lines")
		}
		calib := f.c.HGetAll(f.ctx, FoldKey(f.s, "calib")).Val()
		if _, stale := calib["stale"]; stale || len(calib) != 1 {
			t.Fatalf("s:%s:fold:calib = %v; a fold replaces the section", f.s, calib)
		}
		if got := f.c.HGet(f.ctx, FoldKey(f.s, "sum"), "receipt").Val(); got != second.Receipt ||
			got != "FOLD sprint=fold-s cards=1 closed=1 types=1 proposals=0 calib=1 useful_min=8 min_n=1 type_src=kind at=1758800000000" {
			t.Fatalf("receipt %q", got)
		}
		if got := f.c.HGet(f.ctx, FoldKey(f.s, "jev"), "code").Val(); got != "FOLD JEV type=code false_pass=0/1 labeled=1 unlabeled=0 conflicts=0 ties=0 friend_unscored=0 jev_unscored=0" {
			t.Fatalf("s:%s:fold:jev code = %q", f.s, got)
		}
	})

	t.Run("refused", func(t *testing.T) {
		for status, want := range map[string]string{
			"":     "REFUSED fold-s no such sprint",
			"open": "REFUSED fold-s is open; a fold reads a closed sprint; remedy: nova-sprint sprint close --sprint fold-s",
		} {
			f := newFoldSeat(t, status)
			_, refused, err := Fold(f.ctx, f.c, f.s, time.Now())
			if err != nil || !strings.HasPrefix(refused, want) {
				t.Fatalf("status %q: refused=%q err=%v; want %q", status, refused, err, want)
			}
			if n := f.c.Exists(f.ctx, FoldKey(f.s, "sum")).Val(); n != 0 {
				t.Fatalf("status %q: a refused fold wrote s:%s:fold:sum", status, f.s)
			}
		}
		f := newFoldSeat(t, "closed", "useful_min", "11")
		if _, _, err := Fold(f.ctx, f.c, f.s, time.Now()); err == nil || !strings.Contains(err.Error(), `useful_min="11"`) {
			t.Fatalf("useful_min=11: err=%v; want the policy named", err)
		}
	})
}
