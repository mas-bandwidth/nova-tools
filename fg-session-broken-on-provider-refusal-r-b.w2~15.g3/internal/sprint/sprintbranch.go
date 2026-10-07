package sprint

// Every stream lands on the sprint branch, and promotion alone reaches dev
// (docs/SPEC-SPRINT.md section 7, the sprint branch): a card cut on dev is merged by
// the lander straight onto it and ejects the merge queue's promotion run, so add admits
// a card based on dev only into the promotion stream, and the lander refuses a landing
// on dev outside it (ProtectedLandWhy, the same mark).

// DevBranch is the development branch, which promotion alone reaches.
const DevBranch = "dev"

// IsPromotionStream says the stream is the promotion stream: its control card carries
// the mark FieldLandProtected, which only the coordinator's `nova-sprint stream set <s>
// --land-protected <owner/name,...|any>` writes (Set) and `--land-protected default`
// takes off (docs/SPEC-SPRINT.md section 7, the sprint branch).
func IsPromotionStream(s *Snapshot, stream string) bool {
	return s.StreamCtl(stream).F(FieldLandProtected) != ""
}

// SprintBranchWhy is why add refuses card into stream, "" when it may: its BASE is dev
// and the stream is not the promotion stream (docs/SPEC-SPRINT.md section 7, the sprint
// branch). The remedy re-cuts the card on the sprint branch, or marks the stream.
func SprintBranchWhy(s *Snapshot, stream, base, card string) string {
	if base != DevBranch || IsPromotionStream(s, stream) {
		return ""
	}
	return "card " + card + " is cut on " + DevBranch + ", and stream " + stream + " is not the promotion stream: every stream lands on the sprint branch, and promotion alone reaches " + DevBranch +
		"; nothing was written; re-cut the card with BASE: <the sprint branch> (sprint/<name>, the branch its stream lands on), or, for the promotion stream, run: nova-sprint stream set " + stream + " --land-protected <owner/name,...|any>"
}
