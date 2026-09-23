package report

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeForge struct {
	prs      map[int]string   // pr -> body
	comments map[int][]string // pr -> comment bodies
	nextID   int
	findPR   func(repo, branch string) (int, error)
}

func (f *fakeForge) FindPR(repo, branch string) (int, error) {
	if f.findPR != nil {
		return f.findPR(repo, branch)
	}
	return 0, nil
}

func (f *fakeForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	f.nextID++
	if f.prs == nil {
		f.prs = make(map[int]string)
	}
	f.prs[f.nextID] = body
	return f.nextID, nil
}

func (f *fakeForge) GetPRBody(repo string, pr int) (string, error) {
	return f.prs[pr], nil
}

func (f *fakeForge) UpdatePRBody(repo string, pr int, body string) error {
	f.prs[pr] = body
	return nil
}

func (f *fakeForge) ListComments(repo string, pr int) ([]string, error) {
	return f.comments[pr], nil
}

func (f *fakeForge) PostComment(repo string, pr int, body string) error {
	if f.comments == nil {
		f.comments = make(map[int][]string)
	}
	f.comments[pr] = append(f.comments[pr], body)
	return nil
}

func TestEnsurePRBodyCreatesNew(t *testing.T) {
	f := &fakeForge{}
	pr, changed, err := EnsurePRBody(f, "a/b", "dev", "rowan/test", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if pr != 1 || !changed {
		t.Fatalf("pr=%d changed=%v, want 1 true", pr, changed)
	}
	if f.prs[1] != "body" {
		t.Fatalf("body=%q, want %q", f.prs[1], "body")
	}
}

func TestEnsurePRBodyNoChange(t *testing.T) {
	f := &fakeForge{
		findPR: func(repo, branch string) (int, error) { return 42, nil },
		prs:    map[int]string{42: "body"},
	}
	pr, changed, err := EnsurePRBody(f, "a/b", "dev", "rowan/test", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if pr != 42 || changed {
		t.Fatalf("pr=%d changed=%v, want 42 false", pr, changed)
	}
}

func TestEnsurePRBodyUpdates(t *testing.T) {
	f := &fakeForge{
		findPR: func(repo, branch string) (int, error) { return 42, nil },
		prs:    map[int]string{42: "old-body"},
	}
	pr, changed, err := EnsurePRBody(f, "a/b", "dev", "rowan/test", "title", "new-body")
	if err != nil {
		t.Fatal(err)
	}
	if pr != 42 || !changed {
		t.Fatalf("pr=%d changed=%v, want 42 true", pr, changed)
	}
	if f.prs[42] != "new-body" {
		t.Fatalf("body=%q, want %q", f.prs[42], "new-body")
	}
}

func TestPostVerdictOnceFirstPost(t *testing.T) {
	f := &fakeForge{
		prs: map[int]string{1: "body"},
	}
	posted, err := PostVerdictOnce(f, "a/b", 1, "APPROVE", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected first post to succeed")
	}
	if len(f.comments[1]) != 1 {
		t.Fatalf("got %d comments, want 1", len(f.comments[1]))
	}
	if f.comments[1][0] != "PR1: APPROVE head=abc123" {
		t.Fatalf("comment=%q, want %q", f.comments[1][0], "PR1: APPROVE head=abc123")
	}
}

func TestPostVerdictOnceDuplicate(t *testing.T) {
	f := &fakeForge{
		prs:      map[int]string{1: "body"},
		comments: map[int][]string{1: {"PR1: APPROVE head=abc123"}},
	}
	posted, err := PostVerdictOnce(f, "a/b", 1, "APPROVE", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Fatal("expected duplicate post to be skipped")
	}
	if len(f.comments[1]) != 1 {
		t.Fatalf("got %d comments, want 1", len(f.comments[1]))
	}
}

func TestPostVerdictOnceHold(t *testing.T) {
	f := &fakeForge{
		prs: map[int]string{1: "body"},
	}
	posted, err := PostVerdictOnce(f, "a/b", 1, "HOLD", "def456")
	if err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected first HOLD post to succeed")
	}
	if f.comments[1][0] != "PR1: HOLD head=def456" {
		t.Fatalf("comment=%q, want %q", f.comments[1][0], "PR1: HOLD head=def456")
	}
}

func TestPostVerdictOnceInvalidVerdict(t *testing.T) {
	f := &fakeForge{}
	_, err := PostVerdictOnce(f, "a/b", 1, "MAYBE", "abc")
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
}

func TestPostVerdictOnceEmptyHead(t *testing.T) {
	f := &fakeForge{}
	_, err := PostVerdictOnce(f, "a/b", 1, "APPROVE", "")
	if err == nil {
		t.Fatal("expected error for empty head")
	}
}

func TestPostVerdictOnceZeroPR(t *testing.T) {
	f := &fakeForge{}
	_, err := PostVerdictOnce(f, "a/b", 0, "APPROVE", "abc")
	if err == nil {
		t.Fatal("expected error for zero PR")
	}
}

// slowForge widens the list-then-post window so an unserialized
// PostVerdictOnce would post the same verdict more than once.
type slowForge struct {
	mu       sync.Mutex
	comments []string
	head     string
}

func (f *slowForge) FindPR(repo, branch string) (int, error) { return 0, nil }
func (f *slowForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	return 0, nil
}
func (f *slowForge) GetPRBody(repo string, pr int) (string, error)       { return "", nil }
func (f *slowForge) UpdatePRBody(repo string, pr int, body string) error { return nil }
func (f *slowForge) ListComments(repo string, pr int) ([]string, error) {
	f.mu.Lock()
	out := append([]string(nil), f.comments...)
	f.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	return out, nil
}
func (f *slowForge) PostComment(repo string, pr int, body string) error {
	f.mu.Lock()
	f.comments = append(f.comments, body)
	f.mu.Unlock()
	return nil
}

type headForge struct {
	slowForge
}

func (f *headForge) PRHead(repo string, pr int) (string, error) { return f.head, nil }

func TestPostVerdictOnceConcurrent(t *testing.T) {
	f := &slowForge{}
	var wg sync.WaitGroup
	var posted int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := PostVerdictOnce(f, "a/b", 77, "APPROVE", "abc123")
			if err != nil {
				t.Error(err)
			}
			if ok {
				atomic.AddInt32(&posted, 1)
			}
		}()
	}
	wg.Wait()
	if posted != 1 || len(f.comments) != 1 {
		t.Fatalf("posted=%d comments=%d, want 1 and 1", posted, len(f.comments))
	}
}

func TestPostVerdictOnceStaleHead(t *testing.T) {
	f := &headForge{slowForge{head: "live456"}}
	ok, err := PostVerdictOnce(f, "a/b", 78, "APPROVE", "stale123")
	if err == nil || ok {
		t.Fatalf("ok=%v err=%v, want refusal on stale head", ok, err)
	}
	if len(f.comments) != 0 {
		t.Fatalf("comments=%d, want 0 on stale head", len(f.comments))
	}
	ok, err = PostVerdictOnce(f, "a/b", 78, "APPROVE", "live456")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want post at live head", ok, err)
	}
}
