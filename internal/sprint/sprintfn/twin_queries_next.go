package sprintfn

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The read of {p}next@e (1.3.1): the next global score, the next generated id
// and gate number of each stream, and the stream set's version. Section 1.0's
// one read function lists {p}next@e among what ns_sprint_read returns, and the
// addendum's sprint-key kinds have none for it; IT19 (add, rank) reads the
// counter it guards (U2, COUNTER), so the kind is added here, in Go and in
// sprint_queries.lua, beside the others: one HMGET of the fields named.

// NextField says a name is a field of {p}next@e: score, streams, id:<s> or
// gate:<s> with s a stream's name (1.3.1).
func NextField(f string) bool {
	if f == "score" || f == "streams" {
		return true
	}
	for _, p := range []string{"id:", "gate:"} {
		if s, ok := strings.CutPrefix(f, p); ok {
			return sprint.ValidID(s)
		}
	}
	return false
}

// NextResult is the fields of {p}next@e asked that the hash holds, each an
// exact decimal; a field it does not hold is left out.
type NextResult struct {
	Kind   string            `json:"kind"`
	Fields map[string]string `json:"fields"`
}

// ResultKind is the kind of the query that returned the answer.
func (r NextResult) ResultKind() string { return r.Kind }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r NextResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }
