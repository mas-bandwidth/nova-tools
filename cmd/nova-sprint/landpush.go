package main

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// pushFail is a stream's refused pushes under the push-retry rule: how many, the last
// words of the remote, and when the push is tried again.
type pushFail struct {
	n    int
	base string
	why  string
	next time.Time
}

// pushRefused is the push-retry rule's answer to a push refused again after its rebuild
// (docs/SPEC-SPRINT.md section 8, answered by rule; a stream stopped by a transient
// refusal, a protection hook's GH006, stayed stopped 40 minutes until a person resumed
// it): the batch is refused without a stop, with the remote's words and when it is pushed
// again, after sprint.PushRetries[0], then after [1], the next landing on the stream in
// between refused before any git; the third refusal stops (stop), the one judgment.
func (l *lander) pushRefused(stream, base string, err error) (why string, stop bool) {
	if l.pushFails == nil {
		l.pushFails = map[string]*pushFail{}
	}
	f := l.pushFails[stream]
	if f == nil {
		f = &pushFail{}
		l.pushFails[stream] = f
	}
	f.n, f.base, f.why = f.n+1, base, firstLine("", err)
	if f.n > len(sprint.PushRetries) {
		delete(l.pushFails, stream)
		return "", true
	}
	f.next = l.clock().Add(sprint.PushRetries[f.n-1])
	return f.said(), false
}

// said is a refusal as a refused landing says it: the remote's words, and when the push is
// tried again.
func (f *pushFail) said() string {
	return fmt.Sprintf("the push to %s was rejected again after a rebuild: %s; pushed again at %s (refusal %d of %d)",
		f.base, oneline.Cap(f.why, 300), f.next.UTC().Format("15:04:05 MST"), f.n, len(sprint.PushRetries)+1)
}
