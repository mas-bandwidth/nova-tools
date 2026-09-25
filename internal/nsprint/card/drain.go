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
	Control   string   // control-<id> run to tear down (drainControl); alone
}

var memberRE = regexp.MustCompile(`^(bench|friend):[A-Za-z0-9][A-Za-z0-9._-]*$`)

func keyPaused(sprint string) string         { return "s:" + sprint + ":paused" }
func keyParked(sprint, member string) string { return "s:" + sprint + ":parked:" + member }
func keyDrainImported(sprint string) string  { return "s:" + sprint + ":drain:imported" }

// controlRE is a control id: the name a control run gives its sprint, and
// the prefix (id or id-<suffix>) of every bench, friend and machine it makes.
var controlRE = regexp.MustCompile(`^control-[a-z0-9-]{1,32}$`)

func keyControl(control string) string { return "control:" + control + ":keys" }

// RegisterKeys records, in one call, keys a control run created that the
// store's own index sets do not reach (a probe key, a scratch hash), under
// the control's registry control:<id>:keys, so drain --control removes them
// with the rest of the run (#3442).
func RegisterKeys(ctx context.Context, client *redis.Client, control string, keys ...string) error {
	if !controlRE.MatchString(control) {
		return fmt.Errorf("register: control id %q is not control-<id>", control)
	}
	members := make([]any, 0, len(keys))
	for _, k := range keys {
		if k != "" {
			members = append(members, k)
		}
	}
	if len(members) == 0 {
		return nil
	}
	return client.SAdd(ctx, keyControl(control), members...).Err()
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

// Drain runs resume, import, and release for one sprint, or, with
// opts.Control, tears one control run down (drainControl). See DrainOptions.
func Drain(ctx context.Context, client *redis.Client, sprint string, opts DrainOptions) VerbResult {
	if opts.Control != "" {
		if sprint != "" || len(opts.Resume) > 0 || len(opts.QueueDirs) > 0 {
			return refused("--control tears a control run down and takes no --sprint, --resume or --queue-dir")
		}
		return drainControl(ctx, client, opts.Control)
	}
	if !sprintRE.MatchString(sprint) {
		return refused("sprint name must match [a-z0-9-]{1,40}")
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

// drainControl is `nova-sprint drain --control <id>` (#3442): one
// ns_control_teardown call (control.lua) removes every key the control run
// left, found through the index sets (sprints, benches, friends, the
// control sprint's card roster, each consumer's machine) and the control's
// registry, never by KEYS or SCAN. A second teardown removes nothing.
func drainControl(ctx context.Context, client *redis.Client, control string) VerbResult {
	if !controlRE.MatchString(control) {
		return refused("control id must be control-<id>, [a-z0-9-]{1,40}")
	}
	if err := ensure(ctx, client); err != nil {
		return refused(err.Error())
	}
	vals, err := client.FCall(ctx, "ns_control_teardown", []string{keyControl(control)}, control).StringSlice()
	if err != nil || len(vals) != 7 || vals[0] != "OK" {
		return refused(fmt.Sprintf("control %s teardown: %v %v", control, vals, err))
	}
	return VerbResult{Code: exitOK, Stdout: fmt.Sprintf(
		"DRAIN DONE control=%s sprints=%s cards=%s benches=%s friends=%s machines=%s removed=%s\n",
		oneline.Field(control), vals[1], vals[2], vals[3], vals[4], vals[5], vals[6])}
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
