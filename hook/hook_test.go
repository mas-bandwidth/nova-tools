package hook

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// itoa is an epoch as the record spells it.
func itoa(e int64) string { return strconv.FormatInt(e, 10) }

// stampOf is a lease or proof value under epoch.
func stampOf(epoch int64) string {
	return `{"epoch":` + itoa(epoch) + `,"at":1,"by":"test"}`
}

// reads is one PushRead per push of Pushes: live and proven name the pushes
// whose lease and proof stand under epoch.
func reads(epoch int64, live, proven []Push) []PushRead {
	out := make([]PushRead, len(Pushes))
	for i, p := range Pushes {
		for _, l := range live {
			if l == p {
				out[i].Live, out[i].LiveLeft = stampOf(epoch), LeaseTTL
			}
		}
		for _, pr := range proven {
			if pr == p {
				out[i].Proof, out[i].ProofLeft = stampOf(epoch), ProofTTL
			}
		}
	}
	return out
}

func hooked(role Role, epoch int64) map[string]string {
	return map[string]string{"role": string(role), "state": "hooked", "epoch": itoa(epoch), "armed": "1791000000000"}
}

// TestJudgeIsTheStateMachine pins every state of the record (tla/Hook.tla's
// states): none, unhooked, expired, partial, live and proven, and that a lease
// or proof of another epoch counts for nothing.
func TestJudgeIsTheStateMachine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		rec   map[string]string
		reads []PushRead
		want  string
	}{
		{"no record is none", nil, reads(1, nil, nil), StateNone},
		{"an unhooked record is unhooked", map[string]string{"role": "friend", "state": "unhooked", "epoch": "4", "unhooked": "1791000000000"}, reads(4, []Push{Bus, Work}, []Push{Bus, Work}), StateUnhooked},
		{"hooked with no lease is expired", hooked(Friend, 2), reads(2, nil, nil), StateExpired},
		{"one of two leases is partial", hooked(Friend, 2), reads(2, []Push{Bus}, []Push{Bus}), StatePartial},
		{"every lease and no proof is live", hooked(Coordinator, 3), reads(3, []Push{Bus, Sprint}, nil), StateLive},
		{"every lease and one proof is live", hooked(Coordinator, 3), reads(3, []Push{Bus, Sprint}, []Push{Bus}), StateLive},
		{"every lease and proof is proven", hooked(Coordinator, 3), reads(3, []Push{Bus, Sprint}, []Push{Bus, Sprint}), StateProven},
		{"a proof with no lease is no push", hooked(Comms, 1), reads(1, nil, []Push{Bus}), StateExpired},
		{"leases of another epoch count for nothing", hooked(Friend, 5), reads(4, []Push{Bus, Work}, []Push{Bus, Work}), StateExpired},
		{"a proof of another epoch counts for nothing", hooked(Member, 5), append(reads(5, []Push{Work}, nil)[:2], PushRead{Live: stampOf(5), Proof: stampOf(4)}), StateLive},
		{"pushes the role does not need do not count", hooked(Member, 1), reads(1, []Push{Bus, Sprint}, []Push{Bus, Sprint}), StateExpired},
		{"a role nobody knows is expired", map[string]string{"role": "stranger", "state": "hooked", "epoch": "1"}, reads(1, Pushes, Pushes), StateExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, Judge("ada", c.rec, c.reads).State)
		})
	}
}

// TestTheGatePassesOnlyProven pins the gate: only a proven record whose role
// carries the needed pushes passes; every other state is a refusal whose
// remedy is the hook command, runnable as printed.
func TestTheGatePassesOnlyProven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		st       Status
		want     Want
		pass     bool
		remedy   string
		whyHas   string
		linesHas string
	}{
		{"proven passes", Judge("ada", hooked(Coordinator, 1), reads(1, []Push{Bus, Sprint}, []Push{Bus, Sprint})), Want{Name: "ada", Need: []Push{Bus}}, true, "", "", ""},
		{"none with no role known looks first and offers comms first", Judge("ada", nil, nil), Want{Name: "ada", Need: []Push{Bus}}, false,
			"nova-hook status --as ada", "no hook record", "hook in as comms (on the bus, taking no sprint work): nova-hook hook --as ada --role comms"},
		{"none with the caller's role names it", Judge("bob", nil, nil), Want{Name: "bob", Need: []Push{Bus, Work}, Role: Friend}, false,
			"nova-hook hook --as bob --role friend", "never hooked", "the work push of a friend is held by: the friend daemon's lanes"},
		{"unhooked", Judge("ada", map[string]string{"role": "comms", "state": "unhooked", "epoch": "2", "unhooked": "1791000000000"}, nil), Want{Name: "ada", Need: []Push{Bus}, Role: Comms}, false,
			"nova-hook hook --as ada --role comms", "unhooked at", "unhook: nova-hook unhook --as ada"},
		{"partial names the push that is down", Judge("ada", hooked(Coordinator, 1), reads(1, []Push{Bus}, []Push{Bus})), Want{Name: "ada", Need: []Push{Bus}}, false,
			"nova-hook hook --as ada --role coordinator", "bus=proven sprint=down", "the sprint push of a coordinator is held by: nova-sprint hook --as ada"},
		{"live and unproven", Judge("ada", hooked(Comms, 1), reads(1, []Push{Bus}, nil)), Want{Name: "ada", Need: []Push{Bus}}, false,
			"nova-hook hook --as ada --role comms", "live as comms (bus=armed)", "the bus push of a comms is held by: the friend daemon"},
		{"a coordinator's bus is the session's hook", Judge("ada", hooked(Coordinator, 1), reads(1, []Push{Bus, Sprint}, []Push{Sprint})), Want{Name: "ada", Need: []Push{Bus}}, false,
			"nova-hook hook --as ada --role coordinator", "bus=armed sprint=proven", "nova-hook prove --as ada --nonce <nonce>"},
		{"a role without the push", Judge("m1", hooked(Member, 1), reads(1, []Push{Work}, []Push{Work})), Want{Name: "m1", Need: []Push{Bus}}, false,
			"nova-hook status --as m1", "carries no bus push", "hook in as friend (a friend working the sprint): nova-hook hook --as m1 --role friend"},
		{"a role the act is not for", Judge("ada", hooked(Friend, 1), reads(1, []Push{Bus, Work}, []Push{Bus, Work})), Want{Name: "ada", Need: []Push{Bus}, Roles: []Role{Coordinator, Comms}, Role: Coordinator}, false,
			"nova-hook hook --as ada --role coordinator", "is hooked in as friend, and this act is only for coordinator or comms", "until then: --role comms, nova-hook hook --as ada --role comms"},
		{"a role the act is for passes", Judge("ada", hooked(Comms, 1), reads(1, []Push{Bus}, []Push{Bus})), Want{Name: "ada", Need: []Push{Bus}, Roles: []Role{Coordinator, Comms}, Role: Coordinator}, true, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := c.st.Gate(c.want)
			if c.pass {
				assert.Nil(t, r)
				return
			}
			if !assert.NotNil(t, r) {
				return
			}
			assert.Equal(t, c.remedy, r.Hook)
			assert.Contains(t, r.Why, c.whyHas)
			assert.Contains(t, strings.Join(r.Lines, "\n"), c.linesHas)
			assert.Contains(t, r.Error(), "; run: "+c.remedy)
		})
	}
}

// TestABreadcrumbWithNoRoleNeverGuesses pins the breadcrumb when no role is
// known (a bus verb run as a name never hooked in): it never names the
// coordinator as the remedy; the remedy is to look, and the roles that carry
// the push are offered comms first, then friend, then coordinator.
func TestABreadcrumbWithNoRoleNeverGuesses(t *testing.T) {
	t.Parallel()
	hook, lines := Breadcrumb("ada", "", []Push{Bus}, Judge("ada", nil, nil))
	assert.Equal(t, "nova-hook status --as ada", hook)
	assert.Equal(t, []string{
		"hook in as comms (on the bus, taking no sprint work): nova-hook hook --as ada --role comms",
		"hook in as friend (a friend working the sprint): nova-hook hook --as ada --role friend",
		"hook in as coordinator (the seat): nova-hook hook --as ada --role coordinator",
		"unhook: nova-hook unhook --as ada",
	}, lines)
	_, lines = Breadcrumb("m1", "", []Push{Work}, Judge("m1", nil, nil))
	assert.Equal(t, "hook in as friend (a friend working the sprint): nova-hook hook --as m1 --role friend", lines[0])
	assert.Contains(t, lines[1], "--role member")
}

// TestEveryHolderLineRunsAsPrinted pins that no breadcrumb line carries an
// elision a reader would have to fill in: each names the service and the line
// that restarts it on this machine, and the sprint push's line says where its
// holder ships and what to run until it does.
func TestEveryHolderLineRunsAsPrinted(t *testing.T) {
	t.Parallel()
	for _, r := range Roles {
		for _, p := range PushesOf(r) {
			line := HeldBy("bob", r, p)
			assert.NotContains(t, line, "...", "%s %s: %s", r, p, line)
		}
	}
	assert.Contains(t, HeldBy("bob", Friend, Bus), "restart it: "+RestartCommand(runtime.GOOS, "com.nova.friend-bob"))
	assert.Contains(t, HeldBy("bob", Friend, Work), "restart it: "+RestartCommand(runtime.GOOS, "com.nova.friend-bob"))
	assert.Contains(t, HeldBy("stella", Comms, Bus), "com.nova.friend-notifications-stella")
	assert.Equal(t, "launchctl kickstart -k gui/$(id -u)/com.nova.friend-bob", RestartCommand("darwin", "com.nova.friend-bob"))
	assert.Equal(t, "systemctl --user restart com.nova.friend-bob", RestartCommand("linux", "com.nova.friend-bob"))
	assert.Contains(t, HeldBy("ada", Coordinator, Sprint), "nova-sprint hook ships with nova-sprint PR #93; until then: --role comms, nova-hook hook --as ada --role comms")
}

// TestAProofOutlivesTheNextChallengesBound pins ProofTTL: a proof answered at
// the end of one challenge's bound stands through the whole bound of the next.
func TestAProofOutlivesTheNextChallengesBound(t *testing.T) {
	t.Parallel()
	answered := AnswerBound                        // the latest a challenge set at 0 is answered
	nextAnsweredBy := ChallengeEvery + AnswerBound // the latest the next one is answered
	assert.Greater(t, answered+ProofTTL, nextAnsweredBy)
	assert.Equal(t, ChallengeEvery+2*AnswerBound, ProofTTL)
}

// TestRolesAndPushes pins each role's pushes (docs/SPEC-HOOK.md, roles and
// pushes) and that a role nobody knows is refused with the ones there are.
func TestRolesAndPushes(t *testing.T) {
	t.Parallel()
	want := map[Role][]Push{Coordinator: {Bus, Sprint}, Friend: {Bus, Work}, Comms: {Bus}, Member: {Work}, Reader: {Work}}
	for r, ps := range want {
		t.Run(string(r), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, ps, PushesOf(r))
			got, err := ParseRole(string(r))
			assert.NoError(t, err)
			assert.Equal(t, r, got)
		})
	}
	t.Run("a stranger role", func(t *testing.T) {
		t.Parallel()
		_, err := ParseRole("seat")
		assert.ErrorContains(t, err, "coordinator, friend, comms, member, reader")
	})
}

// TestStatusWordsAndTimes pins the one line status prints and the record's
// times, read in ms of the store's clock.
func TestStatusWordsAndTimes(t *testing.T) {
	t.Parallel()
	st := Judge("ada", hooked(Friend, 7), reads(7, []Push{Bus}, nil))
	assert.Equal(t, "state=partial role=friend epoch=7 bus=armed work=down", st.Words())
	assert.Equal(t, time.UnixMilli(1791000000000).UTC(), st.Armed)
	assert.Equal(t, "state=none", Judge("ada", nil, nil).Words())
	assert.True(t, msTime("x").IsZero())
}
