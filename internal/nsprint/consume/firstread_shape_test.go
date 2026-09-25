package consume

import (
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprintcol"
)

// fqPush is `friend-queue push --to <to> --id <id> --kind <kind> --ref <ref>
// --title <title> --head <head>` (no --front, no --depends-on, a fresh id),
// command for command as the bash verb pipelines it: the reference shape a
// first read must equal (#3773).
func (f *firstReadFixture) fqPush(id, to, kind, ref, title, head string) {
	f.t.Helper()
	ix := "sprint:" + f.S + ":idx:" + to + ":"
	pipe := f.client.Pipeline()
	pipe.HSet(f.ctx, "task:"+id, "kind", kind, "ref", ref, "title", title, "owner", to, "state", "open",
		"head", head, "created_at", time.Now().UTC().Format("2006-01-02T15:04:05Z"), "front", "0")
	pipe.SAdd(f.ctx, "sprint:"+f.S+":tasks", id)
	pipe.SRem(f.ctx, ix+"waiting", id)
	pipe.SAdd(f.ctx, ix+"open", id)
	pipe.SRem(f.ctx, ix+"working", id)
	pipe.SRem(f.ctx, ix+"closed", id)
	pipe.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:" + to, Values: []any{"id", id}})
	if _, err := pipe.Exec(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

var fqCreatedAt = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// fqShape is a task's hash minus created_at (checked apart: the format, and
// within a minute of now) plus its index memberships and stream entry count.
func (f *firstReadFixture) fqShape(id, to string) map[string]string {
	f.t.Helper()
	h, err := f.client.HGetAll(f.ctx, "task:"+id).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	at := h["created_at"]
	if !fqCreatedAt.MatchString(at) {
		f.t.Fatalf("task:%s created_at = %q, want friend-queue's UTC seconds", id, at)
	}
	when, err := time.Parse("2006-01-02T15:04:05Z", at)
	if err != nil || time.Since(when).Abs() > time.Minute {
		f.t.Fatalf("task:%s created_at = %q (%v), want now", id, at, err)
	}
	delete(h, "created_at")
	for _, set := range []string{"sprint:" + f.S + ":tasks", "sprint:" + f.S + ":idx:" + to + ":open",
		"sprint:" + f.S + ":idx:" + to + ":waiting", "sprint:" + f.S + ":idx:" + to + ":working",
		"sprint:" + f.S + ":idx:" + to + ":closed"} {
		in, _ := f.client.SIsMember(f.ctx, set, id).Result()
		h["in "+strings.Replace(set, f.S, "<S>", 1)] = map[bool]string{true: "1", false: "0"}[in]
	}
	n := 0
	for _, q := range f.queue(to) {
		if q == id {
			n++
		}
	}
	h["entries in q:"+to] = strconv.Itoa(n)
	return h
}

// TestFirstReadIsFriendQueueShape (#3773, DONE-WHEN): with the card's stream
// in ws:names (the live 06:55Z condition that wrote state=ready), a first read
// is pushed exactly as `friend-queue push --kind read` pushes: the hash
// equals the reference push's minus created_at, the index sets and the one
// q:<f> entry are the same, it is in no ws: set, and the table's queue column
// (sprintcol, the friend row's live queue) counts it.
func TestFirstReadIsFriendQueueShape(t *testing.T) {
	f := newFirstReadFixture(t, "fr-shape")
	head := "cd4ad8d7aa11bb22cc33dd44ee55ff6600778899"
	if err := f.client.SAdd(f.ctx, "ws:names", "swarm").Err(); err != nil {
		t.Fatal(err)
	}
	f.up("rowan", "1", 0, 0) // the author
	f.up("emma", "1", 0, 0)
	f.cardPR("c-3750", "mas-bandwidth/nova-tools", 3750, head, "internal/x.go")
	f.once()

	id := "read-3750-cd4ad8d7"
	if got := f.queue("emma"); len(got) != 1 || got[0] != id {
		t.Fatalf("q:emma = %v, want [%s]", got, id)
	}
	ref := prURL(nil, "mas-bandwidth/nova-tools", "3750")
	title := "STREAM: swarm | read nova-tools#3750 at cd4ad8d7 (c-3750)"
	f.fqPush("fq-ref", "emma", "read", ref, title, head)

	got, want := f.fqShape(id, "emma"), f.fqShape("fq-ref", "emma")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first read shape differs from friend-queue push:\n got %v\nwant %v", got, want)
	}
	for _, st := range []string{"waiting", "ready", "working", "merging", "landed", "parked"} {
		if _, err := f.client.ZScore(f.ctx, "ws:swarm:"+st, id).Result(); err != redis.Nil {
			t.Fatalf("%s is in ws:swarm:%s; a friend queue task is in no ws: set", id, st)
		}
	}
	if n, _ := f.client.XLen(f.ctx, "ws:log").Result(); n != 0 {
		t.Fatalf("ws:log has %d entries, want none", n)
	}

	col, err := sprintcol.Open(f.client.Options().Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer col.Close()
	cell, err := sprintcol.Queue{Sprint: f.S}.Render(f.ctx, col, nil, "emma")
	if err != nil || cell.N != 2 {
		t.Fatalf("queue column for emma = %+v (%v), want 2 (the first read and the reference push)", cell, err)
	}
}

// TestFirstReadFriendQueueVerbs runs the real bin/friend-queue (rowan-tools)
// against the throwaway server when NOVA_FRIEND_QUEUE_BIN names it: the
// reference hash comes from its own push, and list, counts, take, done and
// cancel work on first reads (06:55Z: list said nothing queued, counts 0 0 0,
// cancel refused "not open (state=ready)"). Skipped without the variable: the
// verb lives in another repository.
func TestFirstReadFriendQueueVerbs(t *testing.T) {
	bin := os.Getenv("NOVA_FRIEND_QUEUE_BIN")
	if bin == "" {
		t.Skip("NOVA_FRIEND_QUEUE_BIN is not set (a rowan-tools bin/friend-queue)")
	}
	f := newFirstReadFixture(t, "fr-verbs")
	if err := f.client.Do(f.ctx, "ACL", "SETUSER", "bench", "on", ">pw", "~*", "+@all",
		"-@scripting", "-multi", "-exec", "-discard", "-watch").Err(); err != nil {
		t.Fatal(err)
	}
	host, port, _ := strings.Cut(f.client.Options().Addr, ":")
	fq := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("bash", append([]string{bin}, args...)...)
		cmd.Env = append(os.Environ(), "FRIEND_QUEUE_INNER=1", "NOVA_REDIS_BENCH_PASSWORD=pw",
			"NOVA_REDIS_HOST="+host, "NOVA_REDIS_PORT="+port, "FRIEND_QUEUE_SPRINT="+f.S)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("friend-queue %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	if err := f.client.SAdd(f.ctx, "ws:names", "swarm").Err(); err != nil {
		t.Fatal(err)
	}
	f.up("rowan", "1", 0, 0)
	f.up("emma", "1", 0, 0)
	heads := []string{"731c762daa11bb22cc33dd44ee55ff6600778899", "b5451ebbaa11bb22cc33dd44ee55ff6600778899"}
	f.cardPR("c-3747", "mas-bandwidth/nova-tools", 3747, heads[0], "internal/x.go")
	f.cardPR("c-3748", "mas-bandwidth/nova-tools", 3748, heads[1], "internal/y.go")
	f.once()
	read1, read2 := "read-3747-731c762d", "read-3748-b5451ebb"

	ref := prURL(nil, "mas-bandwidth/nova-tools", "3747")
	title := "STREAM: swarm | read nova-tools#3747 at 731c762d (c-3747)"
	fq("push", "--to", "emma", "--id", "fq-ref", "--kind", "read", "--ref", ref, "--title", title, "--head", heads[0])
	got, want := f.fqShape(read1, "emma"), f.fqShape("fq-ref", "emma")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first read shape differs from friend-queue push:\n got %v\nwant %v", got, want)
	}

	list := fq("list", "--as", "emma")
	for _, id := range []string{read1, read2} {
		found := false
		for _, line := range strings.Split(list, "\n") {
			if strings.HasPrefix(line, id+" read ") && strings.HasSuffix(line, " open") {
				found = true
			}
		}
		if !found {
			t.Fatalf("friend-queue list --as emma misses %s open:\n%s", id, list)
		}
	}
	if c := strings.TrimSpace(fq("counts", "--as", "emma")); c != "0 3 0" {
		t.Fatalf("friend-queue counts --as emma = %q, want 0 3 0", c)
	}
	if out := fq("take", "--as", "emma", "--id", read1); !strings.Contains(out, read1+" read ") || !strings.HasSuffix(strings.TrimSpace(out), "working") {
		t.Fatalf("take --id %s = %q, want it working", read1, out)
	}
	if st := f.client.HGet(f.ctx, "task:"+read1, "state").Val(); st != "working" {
		t.Fatalf("after take task:%s state = %q, want working", read1, st)
	}
	fq("done", "--as", "emma", "--id", read1, "--evidence", "SCORE 9 who=emma head=731c762d")
	if st := f.client.HGet(f.ctx, "task:"+read1, "state").Val(); st != "closed" {
		t.Fatalf("after done task:%s state = %q, want closed", read1, st)
	}
	if out := fq("cancel", "--as", "emma", "--id", read2, "--evidence", "PR closed unmerged"); !strings.Contains(out, read2+" cancelled") {
		t.Fatalf("cancel %s = %q, want cancelled", read2, out)
	}
	if c := strings.TrimSpace(fq("counts", "--as", "emma")); c != "0 1 2" {
		t.Fatalf("friend-queue counts --as emma after done and cancel = %q, want 0 1 2", c)
	}
}
