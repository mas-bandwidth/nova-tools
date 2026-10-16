package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSprintCoordinatorSelfNoticesHaveExactKindsAndSubjects(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		from    string
		kind    string
		subject string
		want    bool
	}{
		{"new one", "ada", bus.KindRequest, "nova-sprint: 1 new for the coordinator", true},
		{"new many", "ada", bus.KindRequest, "nova-sprint: 12 new for the coordinator", true},
		{"judgments", "ada", bus.KindRequest, "nova-sprint: judgments waiting on the coordinator", true},
		{"decisions", "ada", bus.KindBlocker, "nova-sprint: decisions owed", true},
		{"late tick", "ada", bus.KindBlocker, "nova-sprint: the machine's tick runs late", true},
		{"delivery restored", "ada", bus.KindStatus, "nova-sprint: coordinator delivery restored", true},
		{"ordinary request", "ada", bus.KindRequest, "note to self", false},
		{"ordinary blocker", "ada", bus.KindBlocker, "note to self", false},
		{"ordinary status", "ada", bus.KindStatus, "note to self", false},
		{"wrong kind", "ada", bus.KindReport, "nova-sprint: decisions owed", false},
		{"wrong subject kind", "ada", bus.KindBlocker, "nova-sprint: judgments waiting on the coordinator", false},
		{"late tick wrong kind", "ada", bus.KindStatus, "nova-sprint: the machine's tick runs late", false},
		{"restored wrong kind", "ada", bus.KindBlocker, "nova-sprint: coordinator delivery restored", false},
		{"zero count", "ada", bus.KindRequest, "nova-sprint: 0 new for the coordinator", false},
		{"noncanonical count", "ada", bus.KindRequest, "nova-sprint: 01 new for the coordinator", false},
		{"another recipient", "ada", bus.KindRequest, "nova-sprint: 1 new for bob", false},
		{"another sender", "bob", bus.KindRequest, "nova-sprint: 1 new for the coordinator", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, sprintSent(bus.Message{From: c.from, Kind: c.kind, Subject: c.subject}, "ada"))
		})
	}
}

// TestThePlainHookShowsSprintCoordinatorSelfNotices pins the stream path used
// without a harness adapter. A regular message to self remains invisible.
func TestThePlainHookShowsSprintCoordinatorSelfNotices(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		r.cancel = cancel
		return ctx, cancel
	}
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	var pushes []string
	out := &streamOut{}
	out.each = func(line string) {
		if strings.HasPrefix(line, "HOOK ARMED") {
			for _, m := range []bus.Message{
				{From: "ada", To: []string{"ada"}, Kind: bus.KindRequest, Subject: "nova-sprint: 1 new for the coordinator", Body: "x"},
				{From: "ada", To: []string{"ada"}, Kind: bus.KindRequest, Subject: "nova-sprint: judgments waiting on the coordinator", Body: "x"},
				{From: "ada", To: []string{"ada"}, Kind: bus.KindBlocker, Subject: "nova-sprint: decisions owed", Body: "x"},
				{From: "ada", To: []string{"ada"}, Kind: bus.KindBlocker, Subject: "nova-sprint: the machine's tick runs late", Body: "x"},
				{From: "ada", To: []string{"ada"}, Kind: bus.KindStatus, Subject: "nova-sprint: coordinator delivery restored", Body: "x"},
				{From: "ada", To: []string{"ada"}, Kind: bus.KindRequest, Subject: "note to self", Body: "x"},
			} {
				_, err := b.Send(context.Background(), m)
				require.NoError(t, err)
			}
		}
		if strings.HasPrefix(line, "HOOK PUSH") {
			pushes = append(pushes, line)
			if len(pushes) == 5 {
				r.cancel()
			}
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", "coordinator"}, strings.NewReader(""), out, io.Discard, w)
	assert.Zero(t, code, out.all.String())
	assert.Len(t, pushes, 5, out.all.String())
	for _, subject := range []string{
		"nova-sprint: 1 new for the coordinator",
		"nova-sprint: judgments waiting on the coordinator",
		"nova-sprint: decisions owed",
		"nova-sprint: the machine's tick runs late",
		"nova-sprint: coordinator delivery restored",
	} {
		assert.Contains(t, out.all.String(), `subject="`+subject+`"`)
	}
	assert.NotContains(t, out.all.String(), "note to self")
}
