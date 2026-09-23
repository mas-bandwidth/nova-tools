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
}

var memberRE = regexp.MustCompile(`^(bench|friend):[A-Za-z0-9][A-Za-z0-9._-]*$`)

func keyPaused(sprint string) string         { return "s:" + sprint + ":paused" }
func keyParked(sprint, member string) string { return "s:" + sprint + ":parked:" + member }
func keyDrainImported(sprint string) string  { return "s:" + sprint + ":drain:imported" }

// resumeScript takes the member out of the paused set and moves each parked
// card that is still queued into the pool. A parked label whose card is gone
// or no longer queued (landed, cancelled) leaves the parked set and is not
// pooled.
// keys: paused, parked, pool, log; args: member, sprint
var resumeScript = redis.NewScript(`
local paused, parked, pool, log = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local member, sprint = ARGV[1], ARGV[2]
local was = redis.call('SREM', paused, member)
local moved, dropped = 0, 0
local labels = redis.call('SMEMBERS', parked)
for _, label in ipairs(labels) do
  redis.call('SREM', parked, label)
  local card = 's:' .. sprint .. ':card:' .. label
  if string.match(label, '^[A-Za-z0-9][A-Za-z0-9._-]*$') ~= nil and redis.call('HGET', card, 'state') == 'queued' then
    local priority = redis.call('HGET', card, 'priority')
    if type(priority) ~= 'string' or priority == '' then
      priority = '0'
    end
    redis.call('ZADD', pool, priority, label)
    local t = redis.call('TIME')
    redis.call('XADD', log, '*',
      'kind', 'card', 'id', label, 'from', 'queued', 'to', 'queued',
      'place', 'pool', 'actor', 'resume', 'reason', 'resume ' .. member, 'at', t[1])
    moved = moved + 1
  else
    dropped = dropped + 1
  end
end
return {was, moved, dropped}
`)

// receiptScript writes the one import receipt for a card. The fence is the
// label in s:<S>:drain:imported: a re-run after a crash between the receipt
// and the file removal finds it and writes nothing.
// keys: imported, log; args: label, payload_sha, file, place
var receiptScript = redis.NewScript(`
if redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2]) == 0 then
  return 0
end
local t = redis.call('TIME')
redis.call('XADD', KEYS[2], '*',
  'kind', 'card', 'id', ARGV[1], 'actor', 'drain-import', 'reason', 'import',
  'file', ARGV[3], 'place', ARGV[4], 'payload_sha', ARGV[2], 'at', t[1])
return 1
`)

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

// Drain runs resume, import, and release for one sprint. See DrainOptions.
func Drain(ctx context.Context, client *redis.Client, sprint string, opts DrainOptions) VerbResult {
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
		vals, err := resumeScript.Run(ctx, client,
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
		wrote, err := receiptScript.Run(ctx, client,
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
