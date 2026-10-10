package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A record an older epoch left stays in its table's change log after a clear,
// so the work table's read set names it at the active epoch, where the table
// refuses to read it (MEMBEREPOCH). The sprint's state reads it as the store
// holds it and never refuses; a backup's dump takes it whatever its epoch, and
// the dump restores the same state. Epoch 0's records carry no epoch in their
// ids (the refused read set's one-by-one path), epoch 1's do (the stored-id
// path).
func TestTheStateAndTheDumpHoldTheRecordsOfAnOlderEpoch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1) // epoch 0
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s2", Count: 1})) // epoch 1
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s3", Count: 1})) // epoch 2
	es, err := h.m.Epoch(h.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), es.N)

	work := h.st.Names.Table(sprint.Work)
	ids, err := h.m.RecordIDs(h.ctx, work)
	require.NoError(t, err)
	var plain, one, two []string
	for _, id := range ids {
		switch e, ok := storedEpoch(id); {
		case !ok:
			plain = append(plain, id)
		case e == 1:
			one = append(one, id)
		case e == 2:
			two = append(two, id)
		}
	}
	require.NotEmpty(t, plain, "epoch 0's records are in the change log: %v", ids)
	require.NotEmpty(t, one, "epoch 1's records are in the change log: %v", ids)
	require.NotEmpty(t, two, "epoch 2's records are in the change log: %v", ids)
	_, err = h.m.AtEpoch(2, false).ReadSet(h.ctx, work, ids)
	require.Equal(t, "MEMBEREPOCH", refusalCode(err), "the read set at the active epoch refuses an older record: %v", err)

	want, err := ReadState(h.ctx, h.m, h.st.Names)
	require.NoError(t, err, "the state of a store holding older records is read, never refused")
	for _, id := range append(append(plain, one...), two...) {
		require.Contains(t, want.Parts, "record work "+id, "every record the change log names is a part")
	}
	for _, id := range append(plain, one...) {
		require.Contains(t, want.Parts["record work "+id], `"older"`, "an older record is read as the store holds it")
	}
	for _, id := range two {
		require.NotContains(t, want.Parts["record work "+id], `"older"`, "a record of the active epoch is read at it")
	}

	dump, err := MemDump(h.m, es.N)
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, k := range dump {
		keys[k.Key] = true
		if e, ok := KeyEpoch(k.Key); ok && e != es.N {
			require.True(t, IsRecordKey(h.st.Names, k.Key), "only a record of another epoch is taken: %s", k.Key)
		}
	}
	for _, id := range one {
		var found bool
		for k := range keys {
			found = found || strings.HasSuffix(k, ":"+id)
		}
		require.True(t, found, "the dump takes epoch 1's record %s", id)
	}
	restored, err := MemRestore(dump)
	require.NoError(t, err)
	got, err := ReadState(h.ctx, restored, h.st.Names)
	require.NoError(t, err)
	require.Empty(t, want.Diff(got), "the dump restores the state, older records and all")
}

// BackupKey takes a sprint key of the backup's epoch or of none, and a record
// of any epoch; it leaves out every other key of another epoch.
func TestBackupKeyTakesRecordsOfEveryEpoch(t *testing.T) {
	t.Parallel()
	n := sprint.Names{Prefix: "r-"}
	for _, c := range []struct {
		key  string
		want bool
	}{
		{n.RecordKey(sprint.Work, "quack-0997f2317aa9-a-001~7"), true},
		{n.RecordKey(sprint.Merge, "s1-1~3"), true},
		{n.RecordKey(sprint.Work, "s1-1~15"), true},
		{n.RecordKey(sprint.Work, "s1-1"), true},
		{n.KeyAt("fence", 7), false},
		{n.KeyAt("fence", 15), true},
		{"table:" + n.Table(sprint.Work) + ":7:epoch", false},
		{"table:" + n.Table(sprint.Work) + ":15:epoch", true},
		{n.MemberPrefix(sprint.Work), true}, // a sprint key with no epoch, shared
		{"other:sprint:w:x~7", false},
	} {
		require.Equal(t, c.want, BackupKey(n, c.key, 15), c.key)
	}
}
