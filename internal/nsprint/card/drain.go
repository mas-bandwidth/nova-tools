package card

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Drain is `nova-sprint drain --sprint <S>` (#3035, #2756 section 11 row 1):
// parked work goes back into the pool in one call, in this order.
//
//  1. resume: every member named in Resume leaves s:<S>:paused, and every
//     card in its parked set s:<S>:parked:<member> that is still queued goes
//     back into s:<S>:pool at its own priority.
//  2. import: every regular, non-hidden file in each retired fillloop queue
//     dir is pushed create-only through Push (card lint, 4.3). A card that
//     lints and is stored, or is already stored with the same payload, gets
//     exactly one receipt (a drain-import entry on s:<S>:log, fenced by
//     s:<S>:drain:imported) and its file is removed. A card that fails lint
//     or conflicts is REFUSED and its file stays where it is.
//  3. release: card release over the waiting set.
//
// One OK or REFUSED line per item, then one DRAIN DONE line. Exit 0 when
// nothing was refused, 2 when anything was. A second drain over the same
// state moves nothing and writes nothing.
//
// Pause state: #2940 (control surface) has not landed. Until it does, the
// minimal pause state lives here and #2940 must adopt these keys:
//
//	s:<S>:paused                set of members, "bench:<b>" or "friend:<f>"
//	s:<S>:parked:<member>       set of card labels held out of the pool while
//	                            that member is paused
type DrainOptions struct {
	Resume    []string // members to resume: bench:<b> or friend:<f>
	QueueDirs []string // retired fillloop queue dirs to import once
	Control   string   // control ID to clean up
}

var memberRE = regexp.MustCompile(`^(bench|friend):[A-Za-z0-9][A-Za-z0-9._-]*$`)

func keyPaused(sprint string) string         { return "s:" + sprint + ":paused" }
func keyParked(sprint, member string) string { return "s:" + sprint + ":parked:" + member }
func keyDrainImported(sprint string) string  { return "s:" + sprint + ":drain:imported" }

// RegisterKey registers a key created during a control run under its control id.
func RegisterKey(ctx context.Context, client *redis.Client, control, key string) error {
	if control == "" || key == "" {
		return nil
	}
	return client.SAdd(ctx, "control:"+control+":keys", key).Err()
}

// Drain's two atomic steps are Functions of the nova_sprint library
// (card_pool.lua), called with FCALL, never EVAL/EVALSHA (#3419): the card
// package sends no ad-hoc script on any path.
//
//	ns_card_resume          keys: paused, parked, pool, log; args: member, sprint
//	ns_card_import_receipt  keys: imported, log; args: label, payload_sha, file, place

type drainTally struct {
	out                                 strings.Builder
	released, resumed, imported, refuse int
	receipts                            int
}

func (d *drainTally) ok(sprint, item, rest string) {
	fmt.Fprintf(&d.out, "DRAIN OK sprint=%s item=%s %s\n", oneline.Field(sprint), item, rest)
}

func (d *drainTally) refused(sprint, item, rest, reason string) {
	d.refuse++
	if rest != "" {
		rest += " "
	}
	fmt.Fprintf(&d.out, "DRAIN REFUSED sprint=%s item=%s %sreason=%s\n",
		oneline.Field(sprint), item, rest, oneline.Field(strings.TrimSpace(reason)))
}

// Drain runs resume, import, and release for one sprint, or cleans up a control. See DrainOptions.
func Drain(ctx context.Context, client *redis.Client, sprint string, opts DrainOptions) VerbResult {
	if sprint == "" && opts.Control == "" {
		return refused("needs sprint or control")
	}
	if sprint != "" && !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
	}
	if opts.Control != "" {
		if !sprintRE.MatchString(opts.Control) {
			return refused("control name must match [a-z0-9-]{1,40}")
		}
		if err := ensure(ctx, client); err != nil {
			return refused(err.Error())
		}
		var d drainTally
		keysSet := "control:" + opts.Control + ":keys"
		regKeys, _ := client.SMembers(ctx, keysSet).Result()
		seenKeys := map[string]bool{}
		for _, k := range regKeys {
			seenKeys[k] = true
		}
		patterns := []string{
			"s:" + opts.Control + ":*",
			"*:" + opts.Control + ":*",
			"bench:*" + opts.Control + "*",
			"machine:*" + opts.Control + "*",
			"friend:*" + opts.Control + "*",
			"*" + opts.Control + "*",
		}
		for _, pat := range patterns {
			var cursor uint64
			for {
				page, next, err := client.Scan(ctx, cursor, pat, 1000).Result()
				if err != nil {
					break
				}
				for _, k := range page {
					seenKeys[k] = true
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		benches, _ := client.SMembers(ctx, "benches").Result()
		for _, b := range benches {
			if strings.Contains(b, opts.Control) {
				seenKeys["bench:"+b] = true
				seenKeys["bench:"+b+":desired"] = true
				seenKeys["bench:"+b+":working"] = true
				seenKeys["bench:"+b+":queue"] = true
				seenKeys["bench:"+b+":done"] = true
				seenKeys["bench:"+b+":beat"] = true
				_ = client.SRem(ctx, "benches", b).Err()
			}
		}
		friends, _ := client.SMembers(ctx, "friends").Result()
		for _, f := range friends {
			if strings.Contains(f, opts.Control) {
				seenKeys["friend:"+f] = true
				seenKeys["friend:"+f+":desired"] = true
				seenKeys["friend:"+f+":beat"] = true
				seenKeys["friend:"+f+":queue"] = true
				_ = client.SRem(ctx, "friends", f).Err()
			}
		}
		seenKeys[keysSet] = true

		var keysDel []string
		for k := range seenKeys {
			keysDel = append(keysDel, k)
		}
		var removed int
		if len(keysDel) > 0 {
			res, err := client.Del(ctx, keysDel...).Result()
			if err == nil {
				removed = int(res)
			}
		}
		fmt.Fprintf(&d.out, "DRAIN DONE control=%s removed=%d refused=%d\n",
			oneline.Field(opts.Control), removed, d.refuse)
		code := exitOK
		if d.refuse > 0 {
			code = exitRefused
		}
		return VerbResult{Code: code, Stdout: d.out.String()}
	}
	for _, m := range opts.Resume {
		if !memberRE.MatchString(m) {
			return refused(fmt.Sprintf("resume %s: a member is bench:<b> or friend:<f>", m))
		}
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	var d drainTally

	for _, member := range opts.Resume {
		vals, err := client.FCall(ctx, "ns_card_resume",
			[]string{keyPaused(sprint), keyParked(sprint, member), keyPool(sprint), keyLog(sprint)},
			member, sprint).Int64Slice()
		if err != nil || len(vals) != 3 {
			d.refused(sprint, "resume", "member="+oneline.Field(member), fmt.Sprintf("resume: %v", err))
			continue
		}
		d.resumed += int(vals[1])
		rest := fmt.Sprintf("member=%s moved=%d was_paused=%d", oneline.Field(member), vals[1], vals[0])
		if vals[2] > 0 {
			rest += fmt.Sprintf(" dropped=%d", vals[2])
		}
		d.ok(sprint, "resume", rest)
	}

	for _, dir := range opts.QueueDirs {
		importDir(ctx, client, sprint, dir, &d)
	}

	rel := Release(ctx, client, sprint)
	if rel.Code != 0 {
		d.refused(sprint, "release", "", rel.Stderr)
	} else {
		var moved, waiting int
		line := strings.TrimSpace(rel.Stdout)
		if i := strings.Index(line, " moved="); i >= 0 {
			_, _ = fmt.Sscanf(line[i+1:], "moved=%d waiting=%d", &moved, &waiting)
		}
		d.released = moved
		d.ok(sprint, "release", fmt.Sprintf("moved=%d waiting=%d", moved, waiting))
	}

	fmt.Fprintf(&d.out, "DRAIN DONE sprint=%s released=%d resumed=%d imported=%d refused=%d receipts=%d\n",
		oneline.Field(sprint), d.released, d.resumed, d.imported, d.refuse, d.receipts)
	code := exitOK
	if d.refuse > 0 {
		code = exitRefused
	}
	return VerbResult{Code: code, Stdout: d.out.String()}
}

// importDir imports each card file in one retired queue dir, in name order.
func importDir(ctx context.Context, client *redis.Client, sprint, dir string, d *drainTally) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		d.refused(sprint, "queue-dir", "dir="+oneline.Field(dir), err.Error())
		return
	}
	var names []string
	for _, e := range ents {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		file := "file=" + oneline.Field(name)
		body, err := os.ReadFile(path)
		if err != nil {
			d.refused(sprint, "import", file, err.Error())
			continue
		}
		res := Push(ctx, client, sprint, body)
		if res.Code != exitOK {
			d.refused(sprint, "import", file, res.Stderr)
			continue
		}
		label, place := pushedLabelPlace(res.Stdout)
		if label == "" {
			d.refused(sprint, "import", file, "card push reply "+res.Stdout)
			continue
		}
		sum := sha256.Sum256(body)
		wrote, err := client.FCall(ctx, "ns_card_import_receipt",
			[]string{keyDrainImported(sprint), keyLog(sprint)},
			label, hex.EncodeToString(sum[:]), name, place).Int64()
		if err != nil {
			d.refused(sprint, "import", file+" label="+oneline.Field(label), "receipt: "+err.Error())
			continue
		}
		if err := os.Remove(path); err != nil {
			d.refused(sprint, "import", file+" label="+oneline.Field(label), "stored, file not removed: "+err.Error())
			continue
		}
		d.imported++
		d.receipts += int(wrote)
		d.ok(sprint, "import", fmt.Sprintf("%s label=%s place=%s receipt=%d",
			file, oneline.Field(label), oneline.Field(place), wrote))
	}
}

// pushedLabelPlace reads "CARD PUSH sprint=S label=L place=P".
func pushedLabelPlace(line string) (label, place string) {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "label="); ok {
			label = v
		}
		if v, ok := strings.CutPrefix(f, "place="); ok {
			place = v
		}
	}
	return label, place
}
