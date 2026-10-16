package main

import (
	"encoding/json"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// widthSprint is a fake sprint whose queue answers carry the width nova-config names
// for the worker, as the server's queue --json does: a worker has no width of its own
// (docs/SPEC-SWARM.md, the width), so a test gives it one only through the poll. A
// queue answer that already names a width keeps it.
type widthSprint struct {
	member.Sprint
	width int
}

// answerWidthSprint is widthSprint over a member.AnswerSprint.
type answerWidthSprint struct {
	widthSprint
	answer member.AnswerSprint
}

// rowWidth is s answering width with every queue.
func rowWidth(s member.Sprint, width int) member.Sprint {
	w := widthSprint{Sprint: s, width: width}
	if a, ok := s.(member.AnswerSprint); ok {
		return &answerWidthSprint{widthSprint: w, answer: a}
	}
	return &w
}

func (s *widthSprint) Run(args ...string) (int, []byte) {
	code, out := s.Sprint.Run(args...)
	return code, withWidth(args, code, out, s.width)
}

func (s *answerWidthSprint) RunAnswer(args ...string) (int, []byte, sprintwire.Answer) {
	code, out, answer := s.answer.RunAnswer(args...)
	return code, withWidth(args, code, out, s.width), answer
}

// withWidth is a queue answer with "width" set to width when it names none (or 0).
func withWidth(args []string, code int, out []byte, width int) []byte {
	if len(args) == 0 || args[0] != "queue" || code != 0 || width == 0 {
		return out
	}
	var q map[string]json.RawMessage
	if json.Unmarshal(out, &q) != nil {
		return out
	}
	if w, ok := q["width"]; ok && string(w) != "0" {
		return out
	}
	q["width"] = json.RawMessage(fmt.Sprint(width))
	b, err := json.Marshal(q)
	if err != nil {
		return out
	}
	return b
}
