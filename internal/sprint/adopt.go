package sprint

// The hand adoption pipeline aeb316dea landed here (Adoption.Pass, AdoptSteps,
// AdoptStore, AnswerAdoption and the record types) is gone: the seat's adoption is
// one fleet play through the `adopt` verb (cmd/nova-sprint/adopt_play.go), and the
// pipeline was left in no verb table and no tick. Its drift facts are the ones
// DevDriftOf still reads (sync.go). This file stays so docs/SPRINT-COORDINATOR.md's
// "Adoption is a pipeline" section names a path that exists; that section is staged
// for the coordinator to retire with the pipeline.
