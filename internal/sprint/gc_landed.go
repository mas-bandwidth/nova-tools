package sprint

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// GC's class landed (docs/SPEC-SPRINT.md, "gc"; docs/SPEC-FRIEND.md, the prune
// pass). A job directory whose card is landed or dropped is removed whole after
// one hour, whatever its git state. On 2026-10-07 the AI volume filled because
// landed one-shot jobs were kept: after the branch was pruned they looked like
// commits on no remote or uncommitted paths, and the jobs class keeps those. The
// card is the job's id (<card>~<epoch>, .g<gen> from the second generation; a
// name with no epoch is the card). An open card's job is left for the jobs
// class. When the job's own card has no record, a work card's primary decides.
// The same removal is PruneLanded, on one friend's jobs/.

// GCLandedName is the class a pass says for a landed or dropped card's job.
const GCLandedName = "landed"

// LandedGrace is how long a landed or dropped card's job stays: one hour.
const LandedGrace = time.Hour

const (
	fateLanded  = "landed"
	fateDropped = "dropped"
	fateOpen    = "open"
)

// CardStanding is what gc needs of one card. Fate is landed, dropped, open, or
// empty when the store has no such card. At is when it landed or was dropped.
type CardStanding struct {
	Fate string
	At   time.Time
}

// GCLanded is one pass of the landed class over every working directory's jobs/.
// cards is nil when the sprint's cards were not read: the pass removes nothing
// and says so. Dry reads and removes nothing.
func GCLanded(r GCReq, cards func(string) *Card) GCResult {
	p := newLandedPass(r)
	p.begin(GCLandedName)
	if cards == nil {
		p.say("GC NOTE class=%s why=%s", GCLandedName, "the sprint's cards were not read; no landed job was removed")
		p.volume()
		return p.res
	}
	if real, why := p.knownRoot(r.AIRoot); real == "" && why == "" {
		p.r.AIRoot, p.aiWhy = gcLinkedAIRoot(r.Home)
	}
	for _, root := range []string{p.r.AIRoot, r.BenchRoot, r.LandRoot} {
		if real, _ := p.knownRoot(root); real != "" {
			p.roots = append(p.roots, real)
		}
	}
	for _, w := range p.workDirs() {
		part := PruneLanded(filepath.Join(w, "jobs"), p.r.Now, p.r.Dry, cards)
		p.takeLanded(part)
	}
	p.volume()
	return p.res
}

// PruneLanded removes landed or dropped jobs in one friend's jobs/ (the prune
// pass, and one directory of GCLanded). The working directory that holds jobs/
// is the scratch root. An open card's job stays.
func PruneLanded(jobsDir string, now time.Time, dry bool, cards func(string) *Card) GCResult {
	p := newLandedPass(GCReq{Now: now, Dry: dry})
	p.begin(GCLandedName)
	if cards == nil {
		p.say("GC NOTE class=%s why=%s", GCLandedName, "the sprint's cards were not read; no landed job was removed")
		return p.res
	}
	parent := filepath.Dir(jobsDir)
	if real, why := p.knownRoot(parent); real != "" {
		p.roots = append(p.roots, real)
	} else if why != "" {
		p.refuse(jobsDir, why)
		return p.res
	}
	p.landedIn(jobsDir, cards)
	return p.res
}

func newLandedPass(r GCReq) *gcPass {
	return &gcPass{r: r, res: GCResult{Volume: -1, Dry: r.Dry}}
}

// takeLanded folds one directory's pass into this one, so a machine's jobs are
// one class line.
func (p *gcPass) takeLanded(part GCResult) {
	p.res.Detail = append(p.res.Detail, part.Detail...)
	p.res.Freed += part.Freed
	p.res.Failed += part.Failed
	if len(part.Classes) == 0 || p.class == nil {
		return
	}
	c := part.Classes[0]
	p.class.Count += c.Count
	p.class.Bytes += c.Bytes
	p.class.Kept += c.Kept
	p.class.Refused += c.Refused
	p.class.Failed += c.Failed
}

// landedIn removes the jobs in jobsDir whose card is landed or dropped and past
// the grace. clones is not consulted: the git state does not keep the directory.
func (p *gcPass) landedIn(jobsDir string, cards func(string) *Card) {
	es, err := os.ReadDir(jobsDir)
	// ignored: a jobs/ it cannot list has no landed job to take
	if err != nil {
		return
	}
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() || !landedJobName(name) {
			continue
		}
		id := cardIDOfJob(name)
		if id == "" {
			continue
		}
		st, decided := resolveStanding(id, cards)
		if st.Fate != fateLanded && st.Fate != fateDropped {
			continue
		}
		dir := filepath.Join(jobsDir, name)
		if !p.agedLanded(dir, st.At) {
			continue
		}
		p.remove(dir, jobsDir, "its card "+decided+" is "+st.Fate, false)
	}
}

// agedLanded says the card's landed or dropped time, or the directory's own
// time when the card names none, is at least LandedGrace before now.
func (p *gcPass) agedLanded(dir string, at time.Time) bool {
	if p.r.Now.IsZero() {
		return false
	}
	if at.IsZero() {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			return false
		}
		at = fi.ModTime()
	}
	return p.r.Now.Sub(at) >= LandedGrace
}

// landedJobName is a single path element gc may take. A friend's job is
// <card>~<epoch>, and NameOK refuses the tilde, so the jobs class never saw
// those directories.
func landedJobName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	return safepath.NameOK(name) || cardIDOfJob(name) != ""
}

// cardIDOfJob is the card a job directory names: the id in <id>~<epoch>[.g<gen>],
// or the name itself when it has no epoch marker.
func cardIDOfJob(job string) string {
	if id, _, _, ok := parseLandedJob(job); ok {
		return id
	}
	if safepath.NameOK(job) {
		return job
	}
	return ""
}

// parseLandedJob reads <id>~<epoch> with .g<gen> after it from the second
// generation. id is the card, a work card included.
func parseLandedJob(job string) (id string, epoch, gen int, ok bool) {
	id, rest, found := strings.Cut(job, "~")
	if !found || id == "" || strings.ContainsAny(id, `/\~`) {
		return "", 0, 0, false
	}
	gen = 1
	if e, g, dotted := strings.Cut(rest, ".g"); dotted {
		n, err := strconv.Atoi(g)
		if err != nil || n < 1 || strings.ContainsAny(g, `/\`) {
			return "", 0, 0, false
		}
		rest, gen = e, n
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || strings.ContainsAny(rest, `/\`) {
		return "", 0, 0, false
	}
	return id, n, gen, true
}

// resolveStanding is the job's card, or its primary when the job's own card has
// no record. The id is the card the fate was read from.
func resolveStanding(id string, cards func(string) *Card) (CardStanding, string) {
	if cards == nil || id == "" {
		return CardStanding{}, ""
	}
	own := standingOf(cards(id))
	if own.Fate == fateLanded || own.Fate == fateDropped || own.Fate == fateOpen {
		return own, id
	}
	if primary, _, ok := ParseWorkCard(id); ok && primary != id {
		prim := standingOf(cards(primary))
		if prim.Fate != "" {
			return prim, primary
		}
	}
	return own, id
}

func standingOf(c *Card) CardStanding {
	if c == nil {
		return CardStanding{}
	}
	if c.Placed() && c.Col == Landed {
		return CardStanding{Fate: fateLanded, At: parseStamp(c.F("landed"))}
	}
	if !c.Placed() && c.F("outcome") == "dropped" {
		return CardStanding{Fate: fateDropped, At: parseStamp(c.F("dropped_at"))}
	}
	return CardStanding{Fate: fateOpen}
}

// parseStamp reads a landed or dropped time. An empty or unreadable stamp is
// zero, and the directory's own time is the grace then.
func parseStamp(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return at
}

// AppendGC folds more into base: one summary, the classes of both, the bytes
// of both, and the fuller volume.
func AppendGC(base, more GCResult) GCResult {
	base.Classes = append(base.Classes, more.Classes...)
	base.Detail = append(base.Detail, more.Detail...)
	base.Freed += more.Freed
	base.Failed += more.Failed
	if more.Volume > base.Volume {
		base.Volume = more.Volume
	}
	return base
}
