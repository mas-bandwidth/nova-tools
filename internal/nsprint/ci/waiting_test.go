package ci_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

// waitOn parks one review task on ci:<repo>:<h> the way a producer does
// (#3382, from #3128): the task hash in state waiting-ci with its queue in
// `to`, the sprint's waiting-ci index, and the member <sprint>/<id> in the
// head's waiting set ci:<repo>:<h>:waiting. No open queue holds it.
func (f *fixture) waitOn(sprint, id, to, h string) {
	f.t.Helper()
	key := "s:" + sprint + ":task:" + id
	if err := f.client.HSet(f.ctx, key,
		"kind", "review", "repo", repo, "pr", strconv.Itoa(pr), "head", h,
		"title", "read "+id, "effects", "none", "owner", "", "priority", "5",
		"state", "waiting-ci", "attempt", "0", "token", "0", "payload_sha", "sha-"+id,
		"reason", "", "to", to).Err(); err != nil {
		f.t.Fatal(err)
	}
	f.client.SAdd(f.ctx, "s:"+sprint+":idx:task:waiting-ci", id)
	f.client.SAdd(f.ctx, ci.RecordKey(repo, h)+":waiting", sprint+"/"+id)
}

func (f *fixture) task(sprint, id string) map[string]string {
	f.t.Helper()
	m, err := f.client.HGetAll(f.ctx, "s:"+sprint+":task:"+id).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) member(key, id string) bool {
	f.t.Helper()
	ok, err := f.client.SIsMember(f.ctx, key, id).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return ok
}

func (f *fixture) queued(key, id string) bool {
	f.t.Helper()
	err := f.client.ZScore(f.ctx, key, id).Err()
	return err == nil
}

func (f *fixture) ciFailItems() []string {
	f.t.Helper()
	var out []string
	for k := range f.unresolved() {
		if strings.Contains(k, ":ci-fail:") {
			out = append(out, k)
		}
	}
	return out
}

func (f *fixture) taskReceipts(sprint, id, from, to string) int {
	f.t.Helper()
	msgs, err := f.client.XRange(f.ctx, "s:"+sprint+":log", "-", "+").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Values["kind"] == "task" && m.Values["id"] == id && m.Values["from"] == from && m.Values["to"] == to {
			n++
		}
	}
	return n
}

// TestCiEndReleasesWaitingTasksAndOwnsTheFailItem is nova-tools #3382 (the
// waiting-ci step of the closed #3128, rebuilt inside ns_ci_end per ci
// card): when a ci card ends and writes OK, every task waiting on
// ci:<repo>:<head> for that head, in any sprint, is opened to its queue in
// the same call; FAIL parks them; FLAKY and MISSING leave them waiting. The
// ci-fail item <pr>:<head>:ci-fail:<pkg> has exactly one writer, ns_ci_end,
// when the rerun budget is spent and the failure stands.
func TestCiEndReleasesWaitingTasksAndOwnsTheFailItem(t *testing.T) {
	const other = "3333333333333333333333333333333333333333"

	t.Run("OK releases every task waiting on that head in the same call", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		f.client.SAdd(f.ctx, "friends", "stella")
		f.waitOn(f.sprint, "read-a", "stella", head)
		f.waitOn(f.sprint, "read-b", "", head)
		f.waitOn("control-d2d2d2d2", "read-c", "stella", head)
		f.waitOn(f.sprint, "read-other", "stella", other)
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		if r := f.end(label, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.OK {
			t.Fatalf("end = %v; want OK", r)
		}
		for _, c := range []struct{ sprint, id, queue string }{
			{f.sprint, "read-a", "s:" + f.sprint + ":open:stella"},
			{f.sprint, "read-b", "s:" + f.sprint + ":ready"},
			{"control-d2d2d2d2", "read-c", "s:control-d2d2d2d2:open:stella"},
		} {
			task := f.task(c.sprint, c.id)
			if task["state"] != "open" || task["enqueued_at"] == "" {
				t.Errorf("%s/%s state=%q enqueued_at=%q; want open with the read clock started", c.sprint, c.id, task["state"], task["enqueued_at"])
			}
			if !f.queued(c.queue, c.id) {
				t.Errorf("%s/%s is not in %s", c.sprint, c.id, c.queue)
			}
			if f.member("s:"+c.sprint+":idx:task:waiting-ci", c.id) || !f.member("s:"+c.sprint+":idx:task:open", c.id) {
				t.Errorf("%s/%s index not moved waiting-ci -> open", c.sprint, c.id)
			}
			if n := f.taskReceipts(c.sprint, c.id, "waiting-ci", "open"); n != 1 {
				t.Errorf("%s/%s waiting-ci -> open receipts = %d; want 1", c.sprint, c.id, n)
			}
		}
		if n, _ := f.client.SCard(f.ctx, ci.RecordKey(repo, head)+":waiting").Result(); n != 0 {
			t.Errorf("waiting set for the OK head holds %d; want empty", n)
		}
		if task := f.task(f.sprint, "read-other"); task["state"] != "waiting-ci" || f.queued("s:"+f.sprint+":open:stella", "read-other") {
			t.Errorf("a task waiting on another head moved: %v", task)
		}
		if items := f.ciFailItems(); len(items) != 0 {
			t.Errorf("an OK head wrote ci-fail items %v", items)
		}
	})

	t.Run("FAIL parks the waiting tasks and ns_ci_end writes the one ci-fail item", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		f.waitOn(f.sprint, "read-a", "stella", head)
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		task := f.task(f.sprint, "read-a")
		if task["state"] != "parked" || task["reason"] != "ci-fail" {
			t.Fatalf("after FAIL task state=%q reason=%q; want parked ci-fail", task["state"], task["reason"])
		}
		if f.queued("s:"+f.sprint+":open:stella", "read-a") || f.member("s:"+f.sprint+":idx:task:open", "read-a") {
			t.Fatal("a task at a FAIL head was opened to a reader")
		}
		if !f.member("s:"+f.sprint+":idx:task:parked", "read-a") || f.member("s:"+f.sprint+":idx:task:waiting-ci", "read-a") {
			t.Fatal("parked task index not moved waiting-ci -> parked")
		}
		if items := f.ciFailItems(); len(items) != 0 {
			t.Fatalf("ci-fail items %v before the rerun budget is spent; want none", items)
		}

		if _, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint); err != nil {
			t.Fatal(err)
		}
		token, identity = f.deal(label, "ctl-b")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		if items := f.ciFailItems(); len(items) != 1 || !strings.HasPrefix(f.unresolved()[items[0]], "ci-fail ") {
			t.Fatalf("ci-fail items = %v; want exactly one, written by ns_ci_end", items)
		}
		if n := f.taskReceipts(f.sprint, "read-a", "waiting-ci", "parked"); n != 1 {
			t.Fatalf("park receipts = %d; want 1 (the second FAIL does not re-park)", n)
		}
		if task := f.task(f.sprint, "read-a"); task["state"] != "parked" {
			t.Fatalf("after the standing FAIL task state=%q; want parked", task["state"])
		}
	})

	t.Run("a spent budget of zero writes the ci-fail item on the first FAIL", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		f.client.HSet(f.ctx, "s:"+f.sprint+":policy", "ci_reruns", "0")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		if items := f.ciFailItems(); len(items) != 1 {
			t.Fatalf("ci-fail items with ci_reruns=0 = %v; want the one item on the first FAIL", items)
		}
	})

	t.Run("FLAKY keeps tasks off queues and HOLD writes no ci-fail item", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		f.waitOn(f.sprint, "read-a", "stella", head)
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		if _, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint); err != nil {
			t.Fatal(err)
		}
		token, identity = f.deal(label, "ctl-b")
		if r := f.end(label, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.Flaky {
			t.Fatalf("end = %v; want FLAKY", r)
		}
		if f.queued("s:"+f.sprint+":open:stella", "read-a") {
			t.Fatal("a task at a FLAKY head was opened to a reader")
		}
		if r, err := ci.Dispose(f.ctx, f.st, f.sprint, repo, head, "HOLD", "stella", "https://example.test/hold"); err != nil || r.Status != "HOLD" {
			t.Fatalf("dispose = %v, %v; want HOLD", r, err)
		}
		if items := f.ciFailItems(); len(items) != 0 {
			t.Fatalf("HOLD wrote ci-fail items %v; ns_ci_end is their one writer", items)
		}
		hold := 0
		for k := range f.unresolved() {
			if strings.Contains(k, ":flaky-hold:") {
				hold++
			}
		}
		if hold != 1 {
			t.Fatalf("flaky-hold items = %d; want the one HOLD fix item: %v", hold, f.unresolved())
		}
	})
}
