package member

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
	"time"
)

func TestFinishScopeUsesStagedBaseAndPushedHead(t *testing.T) {
	t.Parallel()
	base, head := strings.Repeat("1", 40), strings.Repeat("2", 40)
	p := Packet{Card: "p.w1", Gen: 3, Epoch: 7}
	for _, fail := range []string{"", "merge-base", "diff", "ls-tree"} {
		t.Run(fail, func(t *testing.T) {
			calls := []string{}
			run := func(_ context.Context, dir string, args ...string) (string, string, error) {
				require.Equal(t, "fixture", dir)
				// gitPlain prefixes configuration; examine the command after that prefix.
				joined := strings.Join(args, " ")
				calls = append(calls, joined)
				require.NotContains(t, joined, "HEAD")
				require.Contains(t, joined, base)
				if fail != "" && strings.Contains(joined, fail) {
					return "", "fixture failure", errors.New("unreadable")
				}
				if strings.Contains(joined, "ls-tree") {
					return "src/a.c\n", "", nil
				}
				require.Contains(t, joined, head)
				if strings.Contains(joined, "diff") {
					return "diff --git a/src/a.c b/src/a.c\n", "", nil
				}
				return "", "", nil
			}
			wire, err := collectScope(context.Background(), p, "fixture", base, head, run)
			if fail != "" {
				require.Error(t, err)
				require.Empty(t, wire)
				return
			}
			require.NoError(t, err)
			require.Len(t, calls, 3)
			var e FinishScopeEvidence
			require.NoError(t, json.Unmarshal([]byte(wire), &e))
			require.Equal(t, p.Card, e.Card)
			require.Equal(t, p.Gen, e.Gen)
			require.Equal(t, p.Epoch, e.Epoch)
			require.Equal(t, base, e.Base)
			require.Equal(t, head, e.Head)
			require.Equal(t, []string{"src/a.c"}, e.Tracked)
		})
	}
}

func TestFinishScopeCompactsBodiesButKeepsRenameAndCatalogFacts(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/private.c b/other.c\nrename from private.c\nrename to other.c\n@@ -1 +1 @@\n-secret source body\n+new source body\ndiff --git a/internal/docs/catalog.go b/internal/docs/catalog.go\n@@ -1 +1 @@\n-old catalog row\n+new catalog row\n"
	compact, err := scopeFileDiff(raw)
	require.NoError(t, err)
	require.NotContains(t, compact, "source body")
	require.Contains(t, compact, "rename from private.c\nrename to other.c")
	require.Contains(t, compact, "-old catalog row\n+new catalog row")
	_, err = scopeFileDiff("unreadable output")
	require.Error(t, err)
}

// The server parser at nova-sprint d55e6102 reads these seven JSON fields.
func fixtureFinishScope(p Packet, head string) (string, error) {
	b, err := json.Marshal(FinishScopeEvidence{Card: p.Card, Gen: p.Gen, Epoch: p.Epoch, Base: strings.Repeat("1", 40), Head: head, Tracked: []string{}})
	return string(b), err
}

func TestMachineFinishCarriesEvidenceAndFailsClosed(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"queue", "local"} {
		for _, outcome := range []string{"bound", "error", "empty"} {
			t.Run(path+"/"+outcome, func(t *testing.T) {
				t.Parallel()
				p := pk("scope.w1")
				sp := newScript()
				called := false
				collector := func(got Packet, head string) (string, error) {
					called = true
					assert.Equal(t, p, got)
					assert.Equal(t, fullSha, head)
					if outcome == "error" {
						return "", errors.New("git unavailable")
					}
					if outcome == "empty" {
						return "", nil
					}
					return fixtureFinishScope(got, head)
				}
				m := New(Config{As: "m", ScopeCollect: collector}, sp, nil, &fakePusher{def: Push{Sha: fullSha}}, io.Discard)
				c := &fakeChild{done: true, res: Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha}}
				m.running[p.Card] = launch{packet: p, child: c, gen: p.Gen, epoch: p.Epoch, branch: p.Branch}
				if path == "local" {
					m.endEndedLocal()
				} else {
					m.endEnded([]string{p.Card}, map[string]queueCard{p.Card: working(p.Card, p.Gen, &p)})
				}
				m.WaitLong()
				require.True(t, called)
				m.collect()
				l := m.running[p.Card]
				require.NotNil(t, l.push)
				m.reportOne(p.Card, l, time.Unix(0, 0), nil)
				m.WaitLong()
				lines := sp.lines("finish")
				require.Len(t, lines, 1)
				if outcome == "bound" {
					wire, err := fixtureFinishScope(p, fullSha)
					require.NoError(t, err)
					assert.Contains(t, lines[0], "--scope "+wire)
					assert.NotContains(t, lines[0], "--failed")
					var golden struct {
						Card    string   `json:"card"`
						Gen     int      `json:"gen"`
						Epoch   uint64   `json:"epoch"`
						Base    string   `json:"base"`
						Head    string   `json:"head"`
						Diff    string   `json:"diff"`
						Tracked []string `json:"tracked"`
					}
					require.NoError(t, json.Unmarshal([]byte(l.push.Scope), &golden))
					assert.Equal(t, p.Card, golden.Card)
					assert.Equal(t, p.Gen, golden.Gen)
					assert.Equal(t, p.Epoch, golden.Epoch)
					assert.Equal(t, fullSha, golden.Head)
					assert.Len(t, golden.Base, 40)
					assert.NotNil(t, golden.Tracked)
				} else {
					assert.Contains(t, lines[0], "--failed")
					assert.Contains(t, lines[0], "finish scope evidence: ")
					assert.NotContains(t, lines[0], "--scope")
				}
			})
		}
	}
}

func TestFinishScopeRejectsShortCommitsAndMissingClone(t *testing.T) {
	t.Parallel()
	for _, ids := range [][2]string{{"123", fullSha}, {fullSha, "123"}} {
		_, err := collectScope(context.Background(), Packet{}, "fixture", ids[0], ids[1], func(context.Context, string, ...string) (string, string, error) {
			t.Error("short commit reached git")
			return "", "", nil
		})
		assert.Error(t, err)
	}
	m := New(Config{StepClone: func(Packet) string { return "" }}, nil, nil, nil, io.Discard)
	pu := Push{Sha: fullSha}
	m.collectFinishScope(Packet{}, &pu)
	assert.Contains(t, pu.ScopeError, "no staged checkout")
}

func TestFinishScopeFailurePrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		r      Result
		prefix string
	}{
		{"provider", Result{End: EndProvider, Provider: "unavailable"}, "finish scope evidence: "},
		{"no result", Result{End: EndNoResult}, "finish scope evidence: "},
		{"budget", Result{End: EndBudget, Budget: "wall exhausted"}, "finish scope evidence: "},
		{"deadline", Result{End: EndDeadline}, "finish scope evidence: "},
		{"staging", Result{End: EndStaging, Staging: "unavailable"}, EndStaging + ": "},
		{"unverifiable", Result{End: EndUnverifiable}, EndStaging + ": "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fin, why := Judge(tc.r, Push{Sha: fullSha, ScopeError: "git unavailable"})
			assert.Equal(t, FinishFailed, fin)
			assert.True(t, strings.HasPrefix(why, tc.prefix), why)
		})
	}
}

func fixtureScopeArg(p Packet, head string) string {
	wire, _ := fixtureFinishScope(p, head)
	return " --scope " + wire
}

func TestScopeCollectionLeavesUnrelatedEndPrefixes(t *testing.T) {
	t.Parallel()
	for _, end := range []string{EndBudget, EndDeadline} {
		fin, why := Judge(Result{End: end}, Push{})
		assert.Equal(t, FinishFailed, fin)
		assert.True(t, strings.HasPrefix(why, end+": "), why)
	}
}

func TestReadersBypassFinishScopeCollection(t *testing.T) {
	t.Parallel()
	for _, local := range []bool{false, true} {
		p := pk("read.w1")
		p.Kind = "read"
		m := New(Config{As: "r", Reader: true, ScopeCollect: func(Packet, string) (string, error) { t.Error("reader collected work scope"); return "", nil }}, newScript(), nil, nil, io.Discard)
		c := &fakeChild{done: true, res: Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha}}
		m.running[p.Card] = launch{packet: p, child: c, gen: p.Gen, epoch: p.Epoch, branch: p.Branch}
		if local {
			m.endEndedLocal()
		} else {
			m.endEnded([]string{p.Card}, map[string]queueCard{p.Card: working(p.Card, p.Gen, &p)})
		}
		m.WaitLong()
		m.collect()
		assert.Nil(t, m.running[p.Card].push)
	}
}
