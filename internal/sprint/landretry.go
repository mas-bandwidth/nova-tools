package sprint

import "time"

// A stream stopped by a rejected push resumes by itself, because a refusal of the
// remote is often transient (a rule on the branch that clears, a base that moves):
// the land loop resumes it once LandRetryAfter has passed since the stop, at most
// LandRetries times in a row; the next stop after the last retry stays for the
// coordinator, its one judgment open (docs/SPEC-SPRINT.md, the stream lifecycle).
const (
	LandRetryAfter = time.Minute
	LandRetries    = 3
)

// RejectedRetryDue says a stream's control row, stopped by a rejected push, is due
// its next retry: tries retries have run since the stream last moved, and the stop
// is LandRetryAfter old at now. It is false for any other state or cause, for a stop
// whose time cannot be read, and once LandRetries have run.
func RejectedRetryDue(ctl *Card, tries int, now time.Time) bool {
	if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "rejected" || tries >= LandRetries {
		return false
	}
	since, err := time.Parse(time.RFC3339, ctl.F("since"))
	return err == nil && now.Sub(since) >= LandRetryAfter
}
