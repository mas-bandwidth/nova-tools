package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerReviewLoopbackWorkerSyntaxCannotRemoveEpochGuard(t *testing.T) {
	t.Parallel()
	for name, argv := range map[string][]string{
		"reordered as":   {"take", "--limit", "1", "--as", "m1", "--json"},
		"explicit actor": {"take", "--as", "m1", "--actor", "m1", "--limit", "1", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newServerRig(t, twoLanes()...)
			before := r.queue("m1")
			res := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, true).Results
			require.Len(t, res, 1)
			assert.NotEqual(t, 0, res[0].Code, "a worker without an epoch must not take: %s%s", res[0].Stdout, res[0].Stderr)
			assert.Equal(t, before, r.queue("m1"), "refusing the missing epoch changes no card")
		})
	}
}

func TestServerReviewForwardedCoordinatorDoesNotBorrowServerActor(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	before := r.queue("m1")
	var sent [][]string
	anonymous := coordinatorAt(t, r, "", &sent)
	code, out, errs := anonymous("clear", "--confirm", "sprint")
	assert.NotEqual(t, 0, code, "a missing caller actor must refuse, not use the server's boss: %s%s", out, errs)
	assert.Equal(t, before, r.queue("m1"))
}

func TestServerReviewHelpAsAValueStillForwardsTheWrite(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	code, out, errs := boss("add", "--stream", "help", "--count", "1")
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.Len(t, sent, 1, "help is the stream's name, not a help request")
}

func TestServerReviewPathRewritingDoesNotConsumeAnotherFlagsValue(t *testing.T) {
	t.Parallel()
	argv := []string{"add", "--brief", "--rules", "--stream", "s1", "--count", "1"}
	assert.Equal(t, argv, absolutePaths(append([]string(nil), argv...)), "the brief is the literal string --rules; --stream is not a rules path")
}

func TestServerReviewInboxCursorWriteIsForwarded(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	code, out, errs := boss("inbox", "--read")
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.Len(t, sent, 1, "moving the coordinator cursor is a write, even though plain inbox is a read")
}
