package sprint

import (
	"fmt"
	"os/exec"
	"strings"
)

// The base-gate rule (docs/SPEC-SPRINT.md section 8, land-base-gate-stops-stream):
// A base tree gate failure stops all landing with ONE judgment naming the failing test.
// Each land pass re-checks the base tip, and when it passes, every stream stopped only
// by the red base resumes by rule (RuleBaseGate), recorded in the log.

// FieldBaseGatePassed is set on a stream when the base gate passes again, allowing
// RuleAnswers to answer the judgment.
const FieldBaseGatePassed = "base_gate_passed"

// BaseRedStopReq is the request to stop landing on a base whose tree gate failed.
type BaseRedStopReq struct {
	Base    string   // base branch name, e.g. "main"
	Streams []string // optional specific streams to stop; empty means all streams landing on this base
	Why     string   // the failure string naming the failing test
	Who     string   // actor stopping the streams
}

// BaseRedResumeReq is the request to resume streams stopped by a red base once the base passes.
type BaseRedResumeReq struct {
	Base string // base branch name, e.g. "main"
	Who  string // actor resuming the streams
}

// LandPassReq is a land pass re-check of a base tip and the resulting actions.
type LandPassReq struct {
	RepoDir string                           // git repository directory
	Base    string                           // base branch name, e.g. "main"
	Gate    func(dir, baseSha string) string // gate function; returns "" if green, else failure string naming the failing test
	Streams []string                         // optional specific streams to land/stop
	Who     string                           // actor
}

// BaseRedStop stops all landing on a red base with ONE judgment naming the failing test.
func BaseRedStop(s *Snapshot, r BaseRedStopReq) Plan {
	var p Plan
	p.on(s)
	base := r.Base
	if base == "" {
		base = "main"
	}
	who := r.Who
	if who == "" {
		who = s.Actor
	}
	what := r.Why
	if what == "" {
		what = "the base " + base + " fails its tree gate"
	}

	var streams []string
	if len(r.Streams) > 0 {
		streams = r.Streams
	} else {
		for _, st := range s.Streams() {
			ctl := s.StreamCtl(st)
			if ctl == nil || ctl.F("state") == StreamStopped {
				continue
			}
			stBase := ctl.F(FieldBaseGateBase)
			if stBase == "" {
				for _, c := range s.Merge.Cell(st, Queued) {
					if b := c.F("base"); b != "" {
						stBase = b
						break
					}
				}
			}
			if stBase == "" {
				stBase = "main"
			}
			if stBase == base {
				streams = append(streams, st)
			}
		}
	}
	if len(streams) == 0 {
		return p
	}

	// Check if an open judgment of NBaseRed already exists for this base
	var existingNote *Note
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NBaseRed {
			if strings.Contains(o.Note.What, base) || o.Note.Stream == streams[0] {
				n := o.Note
				existingNote = &n
				break
			}
		}
	}

	var j Note
	if existingNote != nil {
		j = *existingNote
	} else {
		var subjects []string
		for _, st := range streams {
			subjects = append(subjects, StreamSubject(st))
		}
		j = judgment(NBaseRed, streams[0], s.Now, 0, subjects...)
		j.StreamLevel = false
		j.Who = who
		j.What = cutText(what, MaxCardTextBytes)
	}

	now := stamp(s.Now)
	for i, st := range streams {
		ctl := s.StreamCtl(st)
		if ctl == nil {
			continue
		}
		set := map[string]string{
			"state":           StreamStopped,
			"since":           now,
			"cause":           "base",
			FieldBaseGateBase: base,
		}
		u := Unit{
			Key:    ctl.ID,
			Stream: st,
			Changes: []Change{
				change(Merge, setEntry(ctl, set, append([]string{"card", "other"}, baseGateCount...)...)),
			},
			Moved: fmt.Sprintf("stream %s stopped: the base fails its tree gate", st),
		}
		if existingNote == nil && i == 0 {
			u.Notes = append(u.Notes, j)
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// BaseRedResume resumes every stream stopped only by the red base once the base passes.
// Each resumed stream control card records the resumption by rule (RuleBaseGate).
func BaseRedResume(s *Snapshot, r BaseRedResumeReq) Plan {
	var p Plan
	p.on(s)
	if s.RuleOff(RuleBaseGate) {
		return p
	}
	who := r.Who
	if who == "" {
		who = s.Actor
	}
	base := r.Base
	if base == "" {
		base = "main"
	}
	did := "the base " + base + " passes its tree gate again"
	said := RuleSaid(RuleBaseGate, did)

	for _, st := range s.Streams() {
		ctl := s.StreamCtl(st)
		if ctl == nil {
			continue
		}
		if ctl.F("state") != StreamStopped || ctl.F("cause") != "base" {
			continue // only streams stopped by the red base
		}
		stBase := ctl.F(FieldBaseGateBase)
		if stBase != "" && r.Base != "" && stBase != r.Base {
			continue // stopped on a different base
		}
		q := Resume(s, ResumeReq{Stream: st, Did: said, Who: who})
		for _, u := range q.Units {
			for _, o := range u.Closes {
				if o.Note.Type == NBaseRed {
					u.Notes = append(u.Notes, decided(o, said, who, s.Now))
				}
			}
			p.Units = append(p.Units, u)
		}
		p.Refused = append(p.Refused, q.Refused...)
	}
	return p
}

// LandPass runs one land pass on the repository and base:
// It re-checks the base tip; if red, stops all landing with ONE judgment naming the failing test;
// if green, resumes every stream stopped only by the red base by rule, recorded in the log.
func LandPass(s *Snapshot, r LandPassReq) (Plan, error) {
	base := r.Base
	if base == "" {
		base = "main"
	}
	var baseSha string
	var err error
	if r.RepoDir != "" {
		baseSha, err = GitTip(r.RepoDir, base)
		if err != nil {
			return Plan{}, err
		}
	}
	why := ""
	if r.Gate != nil {
		why = r.Gate(r.RepoDir, baseSha)
	}
	if why != "" {
		return BaseRedStop(s, BaseRedStopReq{
			Base:    base,
			Streams: r.Streams,
			Why:     why,
			Who:     r.Who,
		}), nil
	}
	return BaseRedResume(s, BaseRedResumeReq{
		Base: base,
		Who:  r.Who,
	}), nil
}

// GitTip returns the commit SHA of the base tip in dir.
func GitTip(dir, base string) (string, error) {
	if base != "" {
		if out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "refs/remotes/origin/"+base+"^{commit}").Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		if out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", base+"^{commit}").Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %w", base, dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
