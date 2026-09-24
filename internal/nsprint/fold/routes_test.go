package fold_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// routesFixture is a closed sprint for control 47 (#2756 v6 4.10, #3104),
// seeded under the #2756 2.3 sprint keys:
//
//   - fix (a PR type): route X = ocglm53, 40 PRs, 0 landed, $0.01 a card and 20
//     of them approved 9 at head, so $0.02 per 8+ and nothing landed; route
//     Y = ordspro, 3 landed at $5 a card, so $5 per landed.
//   - nx (a PR type): orhaiku 2 of 2 landed, ocqwenplus 5 PRs 0 landed; both
//     under route_min_landed and neither over route_bench_after.
//   - read (a read type): ords $0.10 for 4 useful, orqwen38 $0.30 for 5
//     useful, ormimo $0.02 for none.
func routesFixture(t *testing.T) (*redis.Client, fold.Options) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	const sprint = "control-47"
	s := "s:" + sprint
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(client.HSet(ctx, s, "goal", "control 47", "status", "closed", "nova_work_sha", "09fbedc905218d5d4bdf5d039d0baff366ef2545").Err())
	must(client.HSet(ctx, s+":policy", "useful_min", "8").Err())
	n := 0
	card := func(fields ...string) string {
		t.Helper()
		n++
		label := fmt.Sprintf("c%03d", n)
		h := map[string]string{"kind": "model", "outcome": "DONE"}
		for i := 0; i+1 < len(fields); i += 2 {
			h[fields[i]] = fields[i+1]
		}
		must(client.HSet(ctx, s+":card:"+label, h).Err())
		must(client.SAdd(ctx, s+":idx:card:"+h["state"], label).Err())
		// Every PR has a state record, or the unknown gate (#3107) holds the fold.
		if pr := h["pr"]; pr != "" {
			state := "opened"
			if h["state"] == "landed" {
				state = "landed"
			}
			must(client.HSet(ctx, s+":pr:"+h["repo"]+":"+pr, "state", state).Err())
		}
		return label
	}
	for i := 0; i < 40; i++ {
		pr, head := fmt.Sprint(1000+i), fmt.Sprintf("aaaa%04d", i)
		card("type", "fix", "route", "ocglm53", "state", "review-ready", "repo", "nova-tools", "pr", pr, "head", head, "usd", "0.01")
		if i < 20 {
			must(client.HSet(ctx, s+":disp:nova-tools:"+pr, "stella@"+head, "APPROVE 9 https://example.invalid/r/"+pr).Err())
		}
	}
	for i := 0; i < 3; i++ {
		card("type", "fix", "route", "ordspro", "state", "landed", "repo", "nova-tools", "pr", fmt.Sprint(2000+i), "head", fmt.Sprintf("bbbb%04d", i), "usd", "5")
	}
	for i := 0; i < 2; i++ {
		card("type", "nx", "route", "orhaiku", "state", "landed", "repo", "nova-tools", "pr", fmt.Sprint(3000+i), "head", fmt.Sprintf("cccc%04d", i), "usd", "2")
	}
	for i := 0; i < 5; i++ {
		card("type", "nx", "route", "ocqwenplus", "state", "harvested", "repo", "nova-tools", "pr", fmt.Sprint(4000+i), "head", fmt.Sprintf("dddd%04d", i), "usd", "1")
	}
	for i := 0; i < 10; i++ {
		score := "6"
		if i < 4 {
			score = "8"
		}
		card("type", "read", "route", "ords", "state", "ended", "usd", "0.01", "score", score)
	}
	for i := 0; i < 10; i++ {
		score := "7"
		if i < 5 {
			score = "9"
		}
		card("type", "read", "route", "orqwen38", "state", "ended", "usd", "0.03", "score", score)
	}
	for i := 0; i < 2; i++ {
		card("type", "read", "route", "ormimo", "state", "ended", "usd", "0.01", "score", "5")
	}
	return client, opts(fixture{Sprint: sprint}, workRepo(t))
}

// Control 47 (#2756 v6 4.10, nova-tools #3104): the fold ranks PR types on $
// per landed and read types on $ per useful, writes routes:<type>, and the
// router refuses a benched route for that type.
func TestControl47(t *testing.T) {
	client, o := routesFixture(t)
	ctx := context.Background()
	var out bytes.Buffer
	res, err := fold.Run(ctx, client, o, &out)
	if err != nil {
		t.Fatalf("fold: %v\n%s", err, out.String())
	}

	h := client.HGetAll(ctx, route.FoldKey("fix")).Val()
	fix, err := route.ParseFold("fix", h)
	if err != nil || fix == nil {
		t.Fatalf("routes:fix after the fold: %v %v\n%s", fix, err, out.String())
	}
	if fix.Kind != route.KindPR || fix.Metric != route.MetricLanded {
		t.Errorf("fix ranks as kind=%s metric=%s, want pr on %s", fix.Kind, fix.Metric, route.MetricLanded)
	}
	if got := strings.Join(fix.Order(), " "); got != "ordspro" {
		t.Errorf("routes:fix = [%s], want [ordspro]: Y landed 3, X landed none", got)
	}
	x, _ := fix.Route("ocglm53")
	if x.Status != route.StatusBenched || x.Reason != "landed 0 of 40" {
		t.Errorf("X in routes:fix: status %q reason %q, want benched `landed 0 of 40` (cheap per 8+ is not landing)", x.Status, x.Reason)
	}
	y, _ := fix.Route("ordspro")
	if y.Status != route.StatusIn || y.Metric != "5" || y.Landed != 3 {
		t.Errorf("Y in routes:fix: %+v, want in at $5 per landed with 3 landed", y)
	}
	if fix.FoldSHA != res.FoldSHA || fix.Sprint != o.Sprint {
		t.Errorf("routes:fix carries fold_sha %s sprint %s, want %s %s", fix.FoldSHA, fix.Sprint, res.FoldSHA, o.Sprint)
	}
	if h["benched"] != "ocglm53" || h["ocglm53.reason"] != "landed 0 of 40" || h["ocglm53.n"] != "40" || h["ocglm53.useful"] != "20" {
		t.Errorf("routes:fix hash fields: %v", h)
	}

	// A PR type under the minimum: probation, not in, and not benched under 15 PRs.
	nx, err := route.ReadFold(ctx, client, "nx")
	if err != nil || nx == nil {
		t.Fatalf("routes:nx: %v %v", nx, err)
	}
	for name, want := range map[string]string{"orhaiku": "landed 2 of 2, under route_min_landed 3", "ocqwenplus": "landed 0 of 5, under route_min_landed 3"} {
		r, ok := nx.Route(name)
		if !ok || r.Status != route.StatusProbation || r.Reason != want {
			t.Errorf("%s in routes:nx: %+v, want probation %q", name, r, want)
		}
	}
	if got := strings.Join(nx.Order(), " "); got != "orhaiku ocqwenplus" {
		t.Errorf("routes:nx order [%s], want [orhaiku ocqwenplus]: $2 per landed first, nothing landed last", got)
	}
	if nx.ProbationShare != "0.1" {
		t.Errorf("routes:nx probation_share %q, want the interim 0.1", nx.ProbationShare)
	}

	// The read type orders by $ per useful read.
	rd, err := route.ReadFold(ctx, client, "read")
	if err != nil || rd == nil {
		t.Fatalf("routes:read: %v %v", rd, err)
	}
	if rd.Kind != route.KindRead || rd.Metric != route.MetricUseful {
		t.Errorf("read ranks as kind=%s metric=%s", rd.Kind, rd.Metric)
	}
	if got := strings.Join(rd.Order(), " "); got != "ords orqwen38 ormimo" {
		t.Errorf("routes:read order [%s], want [ords orqwen38 ormimo] ($0.025, $0.06, none useful)", got)
	}
	if r, _ := rd.Route("ords"); r.Metric != "0.025" || r.Useful != 4 {
		t.Errorf("ords in routes:read: %+v, want $0.025 per useful over 4", r)
	}

	// The router refuses X for a fix card, admits Y (which the static table
	// drops on $ per 8+), and refuses a route the fold never measured on fix.
	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	err = tab.CheckFold(route.Card{Rung: "pro", Type: "fix", Route: "ocglm53"}, fix)
	if err == nil || !strings.HasPrefix(err.Error(), "REFUSED") || !strings.Contains(err.Error(), "landed 0 of 40") {
		t.Errorf("router on X for a fix card: %v, want REFUSED ... landed 0 of 40", err)
	}
	if err := tab.CheckFold(route.Card{Rung: "pro", Type: "fix", Route: "ordspro"}, fix); err != nil {
		t.Errorf("router on Y for a fix card: %v, want allowed", err)
	}
	if err := tab.CheckFold(route.Card{Rung: "pro", Type: "fix", Route: "ocqwenplus"}, fix); err == nil || !strings.Contains(err.Error(), "no row in routes:fix") {
		t.Errorf("router on a route the fold never measured for fix: %v, want REFUSED no row", err)
	}
	if got := strings.Join(tab.AllowedFold("pro", "fix", fix), " "); got != "ordspro" {
		t.Errorf("AllowedFold(pro, fix) = [%s], want [ordspro]", got)
	}

	// The fold prints the per-type lines it wrote.
	for _, want := range []string{
		"FOLD TYPE sprint=control-47 type=fix kind=pr metric=usd_per_landed routes=ordspro benched=ocglm53\n",
		`FOLD TYPE-ROUTE sprint=control-47 type=fix route=ocglm53 status=benched usd_per_landed=- cards=40 prs=40 landed=0 useful=20 usd=0.4 unpriced=0 reason="landed 0 of 40"` + "\n",
		"FOLD TYPE sprint=control-47 type=read kind=read metric=usd_per_useful routes=ords,orqwen38,ormimo benched=-\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing line %q in\n%s", want, out.String())
		}
	}
}

// routes:<type> is written in the same call that records the fold, so a fold
// killed after its commit leaves no ranking, and the re-run writes it with the
// commit's sha.
func TestFoldRoutesWrittenOnlyWithTheRecord(t *testing.T) {
	client, o := routesFixture(t)
	ctx := context.Background()
	killed := o
	killed.Hook = func(step string) error {
		if step == fold.StepCommitted {
			return errKilled
		}
		return nil
	}
	var out bytes.Buffer
	if _, err := fold.Run(ctx, client, killed, &out); err != errKilled {
		t.Fatalf("killed run: %v", err)
	}
	if n := client.Exists(ctx, route.FoldKey("fix")).Val(); n != 0 {
		t.Fatalf("routes:fix exists after a fold killed before its record")
	}
	res, err := fold.Run(ctx, client, o, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.HGet(ctx, route.FoldKey("fix"), "fold_sha").Val(); got != res.FoldSHA {
		t.Fatalf("routes:fix fold_sha %q, want %s", got, res.FoldSHA)
	}
}

// A policy that is not a count or a share is refused before anything is written.
func TestFoldRoutesPolicyRefusesNonsense(t *testing.T) {
	for field, v := range map[string]string{"route_min_landed": "three", "route_bench_after": "0", "probation_share": "1.5"} {
		client, o := routesFixture(t)
		ctx := context.Background()
		client.HSet(ctx, "s:"+o.Sprint+":policy", field, v)
		var out bytes.Buffer
		if _, err := fold.Run(ctx, client, o, &out); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("policy %s=%s: %v, want a refusal naming it", field, v, err)
		}
	}
}
