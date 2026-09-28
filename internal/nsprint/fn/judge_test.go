package fn

import (
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestFromListFindsOnlyOurLibrary: FromList reads the nova_sprint entry out of
// a FUNCTION LIST reply and nothing else.
func TestFromListFindsOnlyOurLibrary(t *testing.T) {
	t.Parallel()
	if code, found := FromList(nil); found || code != "" {
		t.Fatalf("FromList(nil) = %q %v; want none", code, found)
	}
	libs := []redis.Library{{Name: "other", Code: "x"}, {Name: Library, Code: "ours"}}
	if code, found := FromList(libs); !found || code != "ours" {
		t.Fatalf("FromList = %q %v; want ours", code, found)
	}
	if _, found := FromList(libs[:1]); found {
		t.Fatal("FromList found nova_sprint in a reply that holds only another library")
	}
}

// TestJudgeIsFnChecksVerdict: missing, stale and current code, with ours true
// only for the exact embedded source (the one case ns_ping may be called).
func TestJudgeIsFnChecksVerdict(t *testing.T) {
	t.Parallel()
	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	want := Sum(source)

	st, ours, err := Judge("", false)
	if err != nil || ours || !st.Missing || st.Loaded != "" || st.Want != want || st.Ping != PingSkipped || st.OK() {
		t.Fatalf("missing: %+v ours=%v err=%v", st, ours, err)
	}
	st, ours, err = Judge("-- an older library", true)
	if err != nil || ours || st.Missing || st.Loaded != Sum("-- an older library") || st.Ping != PingSkipped || st.OK() {
		t.Fatalf("stale: %+v ours=%v err=%v", st, ours, err)
	}
	st, ours, err = Judge(source, true)
	if err != nil || !ours || st.Missing || st.Loaded != want || st.Ping != "" || st.OK() {
		t.Fatalf("current before ping: %+v ours=%v err=%v", st, ours, err)
	}
	st.Ping = PingReply("PONG", nil)
	if !st.OK() {
		t.Fatalf("current after PONG: %+v; want OK", st)
	}
}

func TestPingReplyIsTheReplyOrTheError(t *testing.T) {
	t.Parallel()
	if got := PingReply("PONG", nil); got != "PONG" {
		t.Fatalf("PingReply(PONG) = %q", got)
	}
	if got := PingReply(nil, errors.New("ERR Function not found")); got != "ERR Function not found" {
		t.Fatalf("PingReply(err) = %q", got)
	}
}
