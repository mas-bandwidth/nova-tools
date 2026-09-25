package sprint

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Fold is `nova-sprint sprint fold --sprint <S>` (nova-tools #2618): the
// end-of-sprint refinement, read only from Redis and written back to it. Three
// pipelined reads (the sprint record, its policy and sprint:<S>:cards; every
// card record; the dispositions at each card's PR and routes:<type>) and one
// MULTI that replaces the s:<S>:fold:<section> hashes. No SCAN, no files, no
// GitHub. The lines follow the #2618 contract: a card's type is its kind,
// useful is landed with a friend label at or above useful_min, intervals are
// Wilson at z=1.96, a zero denominator prints -, and a side under min_n prints
// INSUFFICIENT, never a number.

// FoldSections are the hashes one fold replaces, s:<S>:fold:<section>.
var FoldSections = []string{"cand", "proposal", "score", "jev", "calib", "cost", "ceiling", "sum"}

// FoldKey is one section's hash.
func FoldKey(name, section string) string { return "s:" + name + ":fold:" + section }

// FoldReport is one fold: the lines in print order, the hashes it wrote and
// the counts on its receipt.
type FoldReport struct {
	Lines                                  []string
	Records                                map[string]map[string]string
	Cards, Closed, Types, Proposals, Calib int
	Receipt                                string
}

// foldLabel is R2 on one card: state labeled, unlabeled or tie.
type foldLabel struct {
	state, who           string
	score, unscored, jev int
	conflict, jevOK      bool
}

type foldCard struct {
	label, head                             string
	f                                       map[string]string
	closed, landed, useful, priced, metered bool
	lab                                     foldLabel
	usd                                     float64
	tokens                                  int64
}

type folder struct {
	name            string
	usefulMin, minN int
	rep             *FoldReport
}

func (fd *folder) rec(section, field, value string) {
	k := FoldKey(fd.name, section)
	if fd.rep.Records[k] == nil {
		fd.rep.Records[k] = map[string]string{}
	}
	fd.rep.Records[k][field] = value
}

func (fd *folder) emit(section, field, line string) {
	fd.rep.Lines = append(fd.rep.Lines, line)
	fd.rec(section, field, line)
}

// Fold reads the sprint and writes its fold. refused is a one-line remedy
// when the sprint has no record or is still open; nothing is written then.
func Fold(ctx context.Context, c redis.UniversalClient, name string, now time.Time) (rep FoldReport, refused string, err error) {
	if !ValidName(name) {
		return rep, "", fmt.Errorf("sprint name %q is not [a-z0-9-]{1,40}", name)
	}
	pipe := c.Pipeline()
	stCmd := pipe.HGet(ctx, "s:"+name, "status")
	polCmd := pipe.HMGet(ctx, "s:"+name+":policy", "useful_min", "fold_min_n", "fold_req_fields")
	idsCmd := pipe.ZRange(ctx, "sprint:"+name+":cards", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return rep, "", fmt.Errorf("read s:%s: %w", name, err)
	}
	switch status := stCmd.Val(); status {
	case "":
		return rep, "REFUSED " + name + " no such sprint (no s:" + name + " status); nothing folded; remedy: sprint status lists the sprints", nil
	case "closed", "folded":
	default:
		return rep, "REFUSED " + name + " is " + status + "; a fold reads a closed sprint; remedy: nova-sprint sprint close --sprint " + name, nil
	}
	fd := &folder{name: name, usefulMin: 8, minN: 8, rep: &rep}
	pol := make([]string, 3)
	for i, v := range polCmd.Val() {
		pol[i], _ = v.(string)
	}
	fields := strings.FieldsFunc(cmp.Or(strings.TrimSpace(pol[2]), "repo"), func(r rune) bool { return r == ',' || r == ' ' })
	for i, p := range []*int{&fd.usefulMin, &fd.minN} {
		if n, err := strconv.Atoi(pol[i]); pol[i] != "" && (err != nil || n < 1 || i == 0 && n > 10) {
			return rep, "", fmt.Errorf("s:%s:policy %s=%q is not a count (useful_min a score 1-10)", name, []string{"useful_min", "fold_min_n"}[i], pol[i])
		} else if pol[i] != "" {
			*p = n
		}
	}

	ids := idsCmd.Val()
	pipe = c.Pipeline()
	recs := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		recs[i] = pipe.HGetAll(ctx, id)
	}
	if _, err := pipe.Exec(ctx); err != nil { // an empty pipeline sends nothing
		return rep, "", fmt.Errorf("read the cards of sprint:%s:cards: %w", name, err)
	}
	byType := map[string][]*foldCard{}
	pipe = c.Pipeline()
	disp := map[*foldCard]*redis.MapStringStringCmd{}
	for i, id := range ids {
		f := recs[i].Val()
		cd := &foldCard{label: strings.TrimPrefix(id, "s:"+name+":card:"), f: f, head: cmp.Or(f["head"], f["pushed_sha"])}
		cd.closed, cd.landed = f["where"] == "done", f["state"] == "landed" || f["outcome"] == "landed"
		usd, err := strconv.ParseFloat(f["usd"], 64)
		// a price names the rates that made it (rates_blob), or the card is unpriced
		cd.usd, cd.priced = usd, err == nil && usd >= 0 && !math.IsInf(usd, 0) && f["rates_blob"] != ""
		for _, k := range []string{"tok_in", "tok_out", "tok_cache_read", "tok_cache_write"} {
			if n, err := strconv.ParseInt(f[k], 10, 64); err == nil && n >= 0 {
				cd.tokens, cd.metered = cd.tokens+n, true
			}
		}
		typ := cmp.Or(f["kind"], "-")
		byType[typ] = append(byType[typ], cd)
		if cd.closed && f["repo"] != "" && f["pr"] != "" && cd.head != "" {
			disp[cd] = pipe.HGetAll(ctx, "s:"+name+":disp:"+f["repo"]+":"+f["pr"])
		}
	}
	types := make([]string, 0, len(byType))
	ceil := map[string]*redis.StringCmd{}
	for t := range byType {
		types = append(types, t)
		ceil[t] = pipe.HGet(ctx, "routes:"+t, "cost_ceiling")
	}
	sort.Strings(types)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return rep, "", fmt.Errorf("read the dispositions and routes of %s: %w", name, err)
	}

	rep.Records, rep.Cards, rep.Types = map[string]map[string]string{}, len(ids), len(types)
	for _, t := range types {
		var closed []*foldCard
		for _, cd := range byType[t] {
			if !cd.closed {
				continue
			}
			cd.lab = foldLabel{state: "unlabeled"}
			if d := disp[cd]; d != nil {
				cd.lab = foldLabelAt(d.Val(), cd.head)
			}
			cd.useful = cd.landed && cd.lab.state == "labeled" && cd.lab.score >= fd.usefulMin
			closed = append(closed, cd)
		}
		rep.Closed += len(closed)
		fd.cands(t, closed, fields)
		fd.scores(t, len(byType[t]), closed)
		fd.cost(t, closed, ceil[t].Val())
	}
	rep.Receipt = fmt.Sprintf("FOLD sprint=%s cards=%d closed=%d types=%d proposals=%d calib=%d useful_min=%d min_n=%d type_src=kind at=%d",
		name, rep.Cards, rep.Closed, rep.Types, rep.Proposals, rep.Calib, fd.usefulMin, fd.minN, now.UnixMilli())
	fd.rec("sum", "receipt", rep.Receipt)
	fd.rec("sum", "at", strconv.FormatInt(now.UnixMilli(), 10))
	if _, err = c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, s := range FoldSections {
			p.Del(ctx, FoldKey(name, s))
		}
		for k, m := range rep.Records {
			p.HSet(ctx, k, m)
		}
		return nil
	}); err != nil {
		return rep, "", fmt.Errorf("write s:%s:fold: %w", name, err)
	}
	return rep, "", nil
}

// cands is R1: per requirement field and value, the useful rate of T's closed
// cards with f=v against the rest. One proposal per split: the lower side of
// a two-valued field prints mirror=<the other value> and proposes nothing.
func (fd *folder) cands(t string, closed []*foldCard, fields []string) {
	for _, f := range fields {
		group := map[string][]*foldCard{}
		var vals []string
		for _, cd := range closed {
			v := cmp.Or(cd.f[f], "-")
			if group[v] == nil {
				vals = append(vals, v)
			}
			group[v] = append(group[v], cd)
		}
		sort.Strings(vals)
		for _, v := range vals {
			n, k := len(group[v]), foldUseful(group[v])
			rn, rk := len(closed)-n, foldUseful(closed)-k
			field, head := t+" "+f+"="+v, fmt.Sprintf("FOLD TYPE-CAND type=%s field=%s value=%s", t, f, v)
			if n < fd.minN || rn < fd.minN {
				fd.emit("cand", field, fmt.Sprintf("%s INSUFFICIENT reason=min_n n=%d rest_n=%d need=%d", head, n, rn, fd.minN))
				continue
			}
			lo, hi := wilson(k, n)
			rlo, rhi := wilson(rk, rn)
			line := fmt.Sprintf("%s useful=%d/%d rest=%d/%d ci=[%s,%s] rest_ci=[%s,%s] usd_per_useful=%s min_n=%d",
				head, k, n, rk, rn, f3(lo), f3(hi), f3(rlo), f3(rhi), perUseful(group[v], k), fd.minN)
			fires := lo > rhi || rlo > hi
			if fires && len(vals) == 2 && k*rn < rk*n {
				other := vals[0]
				if other == v {
					other = vals[1]
				}
				line, fires = line+" mirror="+other, false
			}
			fd.emit("cand", field, line)
			if fires {
				labels := make([]string, len(closed))
				for i, cd := range closed {
					labels[i] = cd.label
				}
				sort.Strings(labels)
				fd.emit("proposal", field, fmt.Sprintf("FOLD PROPOSAL kind=type-rule type=%s value=%s=%s n=%d denominator=%d stat=wilson:%s:%s/%s:%s cards=%s",
					t, f, v, k, n, f3(lo), f3(hi), f3(rlo), f3(rhi), strings.Join(labels, ",")))
				fd.rep.Proposals++
			}
		}
	}
}

// scores is R2: the per-type score summary, the Jev false-pass line (Jev at
// or above useful_min on a card the friends labeled below it, over Jev at or
// above useful_min on labeled cards), then the calibration set: one row per
// labeled card; unlabeled and tie cards never enter it.
func (fd *folder) scores(t string, all int, closed []*foldCard) {
	var labeled, unlabeled, ties, conflicts, funs, juns, fp, fpn, landed, sum int
	var calib []string
	lo, hi := 11, -1
	for _, cd := range closed {
		l := cd.lab
		funs, landed, conflicts, juns = funs+l.unscored, landed+b2i(cd.landed), conflicts+b2i(l.conflict), juns+b2i(!l.jevOK)
		if l.state != "labeled" {
			ties, unlabeled = ties+b2i(l.state == "tie"), unlabeled+b2i(l.state == "unlabeled")
			continue
		}
		labeled, sum, lo, hi = labeled+1, sum+l.score, min(lo, l.score), max(hi, l.score)
		jev := "-"
		if l.jevOK {
			jev = strconv.Itoa(l.jev)
			if l.jev >= fd.usefulMin {
				fpn, fp = fpn+1, fp+b2i(l.score < fd.usefulMin)
			}
		}
		row := fmt.Sprintf("type=%s score=%d who=%s jev=%s head=%s", t, l.score, l.who, jev, cd.head)
		calib = append(calib, "FOLD CALIB card="+cd.label+" "+row)
		fd.rec("calib", cd.label, row)
	}
	mean, rng := "-", "-"
	if labeled > 0 {
		mean, rng = strconv.FormatFloat(float64(sum)/float64(labeled), 'f', 2, 64), fmt.Sprintf("%d-%d", lo, hi)
	}
	fd.emit("score", t, fmt.Sprintf("FOLD SCORE type=%s type_src=kind cards=%d closed=%d landed=%d useful=%s labeled=%d mean=%s range=%s useful_min=%d",
		t, all, len(closed), landed, rate(foldUseful(closed), len(closed)), labeled, mean, rng, fd.usefulMin))
	counts := fmt.Sprintf("labeled=%d unlabeled=%d conflicts=%d ties=%d friend_unscored=%d jev_unscored=%d", labeled, unlabeled, conflicts, ties, funs, juns)
	if fpn < fd.minN {
		fd.emit("jev", t, fmt.Sprintf("FOLD JEV type=%s INSUFFICIENT reason=min_n n=%d need=%d %s", t, fpn, fd.minN, counts))
	} else {
		fd.emit("jev", t, fmt.Sprintf("FOLD JEV type=%s false_pass=%d/%d %s", t, fp, fpn, counts))
	}
	fd.rep.Lines, fd.rep.Calib = append(fd.rep.Lines, calib...), fd.rep.Calib+len(calib)
}

// cost is R4 and the R3 ceiling: tokens per useful card, price per million
// tokens and usd per useful over every closed card of T (so their product is
// usd per useful), then one notch under routes:<T> cost_ceiling
// (floor_cents(0.9 x current)), proposed when min_n useful cards have a 90th
// percentile usd that fits under it.
func (fd *folder) cost(t string, closed []*foldCard, current string) {
	var tokens int64
	var usd float64
	var unmetered, unpriced, retries int
	var blobs []string
	var usefulUSD []float64
	for _, cd := range closed {
		tokens, usd = tokens+cd.tokens, usd+cd.usd
		unmetered, unpriced = unmetered+b2i(!cd.metered), unpriced+b2i(!cd.priced)
		if cd.priced && cd.useful {
			usefulUSD = append(usefulUSD, cd.usd)
		}
		if a, err := strconv.Atoi(cd.f["attempt"]); err == nil && a > 1 {
			retries += a - 1
		}
		if b := cd.f["rates_blob"]; b != "" && !slices.Contains(blobs, b[:min(8, len(b))]) {
			blobs = append(blobs, b[:min(8, len(b))])
		}
	}
	useful := foldUseful(closed)
	head := fmt.Sprintf("FOLD COST type=%s closed=%d useful=%d", t, len(closed), useful)
	if len(closed) > 0 && unmetered == len(closed) && unpriced == len(closed) {
		fd.emit("cost", t, fmt.Sprintf("%s ABSENT dep=card-end field=tok_in,usd retries=%d", head, retries))
	} else {
		tpu, upm, upu := "-", "-", "-"
		pricedAll := unpriced == 0 && len(blobs) < 2 // one rate table priced every card
		if unmetered == 0 && useful > 0 {
			tpu = strconv.FormatFloat(float64(tokens)/float64(useful), 'f', 1, 64)
		}
		if pricedAll && unmetered == 0 && tokens > 0 {
			upm = fmt.Sprintf("%.4f", usd/float64(tokens)*1e6)
		}
		if pricedAll && useful > 0 {
			upu = fmt.Sprintf("%.4f", usd/float64(useful))
		}
		sort.Strings(blobs)
		rates := cmp.Or(strings.Join(blobs, ","), "-")
		fd.emit("cost", t, fmt.Sprintf("%s tokens=%d tokens_per_useful=%s usd_per_mtok=%s usd_per_useful=%s retries=%d unmetered=%d unpriced=%d rates=%s",
			head, tokens, tpu, upm, upu, retries, unmetered, unpriced, rates))
	}
	cur, err := strconv.ParseFloat(strings.TrimSpace(current), 64)
	if err != nil || cur <= 0 {
		fd.emit("ceiling", t, fmt.Sprintf("FOLD CEILING type=%s ABSENT dep=#3104 field=cost_ceiling", t))
		return
	}
	next := math.Floor(cur*90+1e-9) / 100
	head = fmt.Sprintf("FOLD CEILING type=%s current=%.4f next=%.4f useful=%d min_n=%d", t, cur, next, useful, fd.minN)
	switch {
	case unpriced > 0:
		fd.emit("ceiling", t, fmt.Sprintf("%s INSUFFICIENT reason=unpriced n=%d", head, unpriced))
	case len(usefulUSD) < fd.minN:
		fd.emit("ceiling", t, fmt.Sprintf("%s INSUFFICIENT reason=min_n n=%d need=%d", head, len(usefulUSD), fd.minN))
	default:
		sort.Float64s(usefulUSD)
		p90, verdict := usefulUSD[int(math.Ceil(0.9*float64(len(usefulUSD))))-1], "propose"
		if p90 > next {
			verdict = "hold"
		}
		fd.emit("ceiling", t, fmt.Sprintf("%s p90=%.4f verdict=%s", head, p90, verdict))
	}
}

// foldLabelAt reads the friend label and Jev's score at head from one
// disposition hash (fields <who>@<head>, values <verdict> <score> ...). A
// score is strconv.Atoi of the whole second token, so 9/10 has none; a
// scoreless friend field is dropped and counted, and a scoreless non-APPROVE
// leaves the card unlabeled. Differing pairs are a conflict labeled by the
// lowest score (fail closed); two verdicts at that score are a tie.
func foldLabelAt(d map[string]string, head string) foldLabel {
	type read struct {
		who, verdict string
		score        int
	}
	var reads []read
	l, bareNo := foldLabel{state: "unlabeled"}, false
	for field, value := range d {
		at := strings.LastIndexByte(field, '@')
		if at < 0 || !headMatch(field[at+1:], head) {
			continue
		}
		who, parts := field[:at], strings.Fields(value)
		score, err := -1, errors.New("no score")
		if len(parts) >= 2 {
			score, err = strconv.Atoi(parts[1])
		}
		switch {
		case who == "jev":
			l.jev, l.jevOK = score, err == nil
		case err != nil:
			l.unscored++
			bareNo = bareNo || len(parts) == 0 || parts[0] != "APPROVE"
		default:
			reads = append(reads, read{who, parts[0], score})
		}
	}
	if bareNo || len(reads) == 0 {
		return l
	}
	low := reads[0] // the lowest score, then the bytewise-least who
	for _, r := range reads[1:] {
		if r.score < low.score || r.score == low.score && r.who < low.who {
			low = r
		}
	}
	l.state, l.score, l.who = "labeled", low.score, low.who
	for _, r := range reads {
		l.conflict = l.conflict || r.score != low.score || r.verdict != low.verdict
		if r.score == low.score && r.verdict != low.verdict {
			l.state, l.score, l.who = "tie", 0, ""
		}
	}
	return l
}

// headMatch compares a full and an abbreviated sha, at least 7 hex digits.
func headMatch(a, b string) bool {
	if a == "" || b == "" || a == b {
		return a != "" && a == b
	}
	return len(a) >= 7 && len(b) >= 7 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

func foldUseful(cs []*foldCard) (k int) {
	for _, cd := range cs {
		k += b2i(cd.useful)
	}
	return k
}

func perUseful(cs []*foldCard, useful int) string {
	var usd float64
	for _, cd := range cs {
		if !cd.priced {
			return "-"
		}
		usd += cd.usd
	}
	if useful == 0 {
		return "-"
	}
	return fmt.Sprintf("%.4f", usd/float64(useful))
}

// wilson is the Wilson score interval at z=1.96.
func wilson(k, n int) (float64, float64) {
	const z = 1.96
	p, nf := float64(k)/float64(n), float64(n)
	d := 1 + z*z/nf
	c, h := (p+z*z/(2*nf))/d, z*math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))/d
	return math.Max(0, c-h), math.Min(1, c+h)
}

func f3(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }

// rate is k/n, or - when n is zero (never 0, never NaN).
func rate(k, n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", k, n)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
