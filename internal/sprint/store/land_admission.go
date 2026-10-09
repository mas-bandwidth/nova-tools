package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// LandAdmissionPin is the exact attempt and brief a landing batch accepts.
type LandAdmissionPin struct {
	ID, Attempt, Head, BriefSHA256 string
}

// LandAdmissionReq binds a fresh batch to its ordered inputs and delivery gate.
// The base's fetched tip is an input of the admitted task, not another admission.
type LandAdmissionReq struct {
	Stream, Repo, Base, Check string
	Pins                      []LandAdmissionPin
}

// LandBriefSHA256 binds the route-bearing brief without copying it into a receipt.
func LandBriefSHA256(brief string) string {
	h := sha256.Sum256([]byte(brief))
	return hex.EncodeToString(h[:])
}

// LandAdmissionStep is SprintPause.tla Admit for one landing batch. Its note
// makes admission a durable fenced operation, including when no card moves.
// The caller starts external git only after Run returns a committed Result.Op.
func LandAdmissionStep(r LandAdmissionReq) Step {
	return Step{Verb: "land admission", Args: ArgsOf(r), StartsWork: true,
		Load: tables(sprint.Merge, sprint.Work), Named: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			refuse := func(why string) sprint.Plan {
				return sprint.Plan{Refused: []sprint.Refusal{{Key: r.Stream, Why: why + "; queued cards stay; run land again"}}}
			}
			if !s.Running {
				return refuse("the machine is STOPPED: new landings are held; run: nova-sprint start after settling STOP receipts")
			}
			if r.Stream == "" || len(r.Pins) == 0 {
				return refuse("landing admission needs a stream and a nonempty exact batch")
			}
			if ctl := s.StreamCtl(r.Stream); ctl != nil && ctl.F("state") == sprint.StreamStopped {
				return refuse("the stream is stopped; run: nova-sprint resume --stream " + r.Stream)
			}
			seen := map[string]bool{}
			ids := make([]string, 0, len(r.Pins))
			stuck := s.Merge.Cell(r.Stream, sprint.Stuck)
			for _, p := range r.Pins {
				mc, pr := s.Merge.Placed(p.ID), s.Work.Placed(p.ID)
				if seen[p.ID] || p.ID == "" || mc == nil || mc.Row != r.Stream || mc.Col != sprint.Queued || pr == nil || pr.Col != sprint.Merging {
					return refuse("the merge queue no longer holds the exact batch at " + p.ID)
				}
				if len(stuck) > 0 && (mc.Score > stuck[0].Score || mc.Score == stuck[0].Score && mc.ID >= stuck[0].ID) {
					return refuse("the merge queue holds a stuck card before " + p.ID)
				}
				if p.Head == "" || pr.F("head") != p.Head || pr.F("attempt") != p.Attempt || LandBriefSHA256(pr.F("brief")) != p.BriefSHA256 {
					return refuse(fmt.Sprintf("%s changed head, attempt or brief since the batch was read", p.ID))
				}
				seen[p.ID] = true
				ids = append(ids, p.ID)
			}
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "landing batch admitted",
				Stream: r.Stream, Primaries: ids, Count: len(ids), At: s.Now, What: "batch=" + ArgsOf(r)}}}
		}}
}
