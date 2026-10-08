package sprint

import "strings"

// The lander's stop text, beside the push policy (land_push.go). This is not
// the server's land loop (cmd/nova-sprint/landloop.go): these are the words and
// the choice the command uses when a stream stops, and when the next land
// resumes one by itself.

// LandResumes says the next land resumes a stream stopped for this cause and
// retries the push (SPEC-SPRINT.md, land). A gate (base, red) or a
// protected base stays for nova-sprint resume.
func LandResumes(cause string) bool {
	return cause == "push" || cause == "rejected"
}

// StopJudgment is the one judgment pushed to the seat when a stream stops,
// on the push path, at the moment it stops.
func StopJudgment(stream, reason string) string {
	return "stream " + stream + " stopped: " + reason + "; run: nova-sprint resume --stream " + stream
}

// StoppedRow is the batch note that names why the stream stopped.
func StoppedRow(reason string) string {
	return "stopped: " + reason
}

// ChooseGate is a tree-gate failure the next pass cannot change: the stream
// stops, the reason is named, and land does not resume it.
func ChooseGate(why string) PushChoice {
	reason := strings.TrimSpace(why)
	if reason == "" {
		reason = "the tree gate failed"
	}
	return PushChoice{Kind: kindStop, Cause: "red", Reason: reason, Row: StoppedRow(reason)}
}

// ChooseProtected is a land-protected refusal: the reason is the lander's own
// words, unchanged, and land does not resume it. The command consults it where
// it already refuses the batch before any git.
func ChooseProtected(why string) PushChoice {
	return PushChoice{Kind: kindStop, Cause: "protected", Reason: why, Row: StoppedRow(why)}
}
