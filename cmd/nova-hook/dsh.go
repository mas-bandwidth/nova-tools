package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/redis/go-redis/v9"
)

const maxDshEvent = 64 << 10

type dshRoute struct {
	route dshdesktop.Route
	since time.Time
}

// dshConfig refuses partial routes before any hook record or lease is written.
// --harness deepseek (or its alias dsh) selects the route; the --dsh-* flags
// carry the harness-side setup (the private event directory and the hook
// config path). No gateway and no cookie: delivery rides the harness's own
// hook bridge.
func (w world) dshConfig(c *tool.Call) (*dshRoute, error) {
	harness := c.Str("harness")
	r := &dshRoute{
		route: dshdesktop.Route{
			EventsDir: c.Str("dsh-events-dir"),
			HooksFile: c.Str("dsh-hooks-file"),
		},
	}
	rawSince := c.Str("dsh-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("dsh cutover wants --dsh-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if harness == "" && r.route.EventsDir == "" && r.route.HooksFile == "" && rawSince == "" {
		return nil, nil
	}
	if harness != "" && harness != "deepseek" && harness != "dsh" {
		return nil, errors.New("--harness wants deepseek (or dsh); other harnesses keep their own route")
	}
	if r.route.EventsDir == "" || r.route.HooksFile == "" {
		return nil, errors.New("dsh delivery wants --dsh-events-dir and --dsh-hooks-file together")
	}
	if c.Bool("once") {
		return nil, errors.New("dsh delivery wants a running hook client; omit --once")
	}
	if w.dshValidate == nil || w.dshSave == nil || w.dshDeliver == nil {
		return nil, errors.New("dsh delivery wants a configured local native receiver")
	}
	if c.DryRun() {
		if _, err := dshdesktop.PendingPath(r.route.EventsDir); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.dshValidate(r.route); err != nil {
		return nil, err
	}
	return r, nil
}

// push delivers one bus event through the route; it satisfies nativeRoute.
func (r *dshRoute) push(w world, c *tool.Call, s *session, role hook.Role, epoch int64, id string, m *bus.Message) *tool.Out {
	return w.dshPush(c, s, r, role, epoch, id, m)
}

// savedDshEvent is a bounded external-data envelope, not liveness state. The
// bus remains event truth; the model reads this file and answers on the bus.
type savedDshEvent struct {
	LocalReceiver struct {
		Identity    string `json:"identity"`
		Redis       string `json:"redis"`
		Proof       string `json:"proof"`
		Acknowledge string `json:"acknowledge"`
	} `json:"local_receiver"`
	SavedAt   time.Time    `json:"saved_at"`
	HookEpoch int64        `json:"hook_epoch"`
	HookRole  hook.Role    `json:"hook_role"`
	EventID   string       `json:"event_id"`
	Message   *bus.Message `json:"external_message,omitempty"`
}

// dshPush records one bus event and surfaces its notice through the harness's
// hook bridge: a challenge uses the ChallengeNotice, an ordinary message the
// BusNotice. The hook delivers it; the model reads the saved event and answers.
func (w world) dshPush(c *tool.Call, s *session, r *dshRoute, role hook.Role, epoch int64, id string, m *bus.Message) *tool.Out {
	if m != nil {
		answered, err := dshAnswered(s, id)
		if err != nil {
			return dshDeliveryFailure(s, role, epoch, id, fmt.Errorf("read model answer: %w", err))
		}
		if answered {
			line(c, tool.Done().As("PUSH").Fact("push", "dsh").Fact("id", id).Fact("acknowledgment", "model-answer").Note("existing bus --re answer reconciles this event; no native mutation attempted"))
			return nil
		}
	}
	notice := dshdesktop.BusNotice
	if isChallenge(m) {
		notice = dshdesktop.ChallengeNotice
	}
	e := savedDshEvent{EventID: id, Message: m, HookEpoch: epoch, HookRole: role}
	return w.dshSend(c, s, r, role, epoch, e, notice)
}

func isChallenge(m *bus.Message) bool {
	return m != nil && strings.HasPrefix(m.Subject, "HOOK CHALLENGE ")
}

func (w world) dshSend(c *tool.Call, s *session, r *dshRoute, role hook.Role, epoch int64, e savedDshEvent, notice string) *tool.Out {
	id := e.EventID
	ctx, clockCancel := call()
	savedAt, clockErr := s.hooks.C.Time(ctx).Result()
	clockCancel()
	if clockErr != nil {
		return dshDeliveryFailure(s, role, epoch, id, clockErr)
	}
	e.SavedAt = savedAt
	e.LocalReceiver.Identity = s.as
	e.LocalReceiver.Redis = s.addr
	e.LocalReceiver.Proof = "For a current hook challenge only, run nova-hook prove --as <identity> --redis <redis> --nonce <nonce>. Only the model may prove."
	e.LocalReceiver.Acknowledge = "For an ordinary bus message, send a model answer with nova-bus send --as <identity> --redis <redis> --to <sender> --re <message ID> --subject ACK --body <answer>."

	data, err := json.Marshal(e)
	if err == nil && len(data) > maxDshEvent {
		err = fmt.Errorf("external event wants at most %d bytes", maxDshEvent)
	}
	if err == nil {
		err = w.dshSave(r.route.EventsDir, id, data)
	}
	text := ""
	if err == nil {
		text, err = w.dshDeliver(r.route, id, notice)
	}
	if err == nil {
		line(c, tool.Done().As("PUSH").Fact("push", "dsh").Fact("id", id).Fact("native", "pending").Fact("notice", tool.Text(text)).Note("the harness hook surfaces this notice; awaiting model proof or bus answer"))
		return nil
	}
	return dshDeliveryFailure(s, role, epoch, id, err)
}

func dshDeliveryFailure(s *session, role hook.Role, epoch int64, id string, err error) *tool.Out {
	ctx, cancel := call()
	dropErr := s.hooks.Unprove(ctx, s.as, role, hook.Bus, epoch)
	cancel()
	o := tool.Fail("dsh local delivery failed: "+err.Error()).Fact("id", id)
	o.Remedy = "inspect the saved event and model bus answer before restarting the native route; never blindly retry"
	if dropErr != nil {
		o.Note("the route proof could not be revoked: " + dropErr.Error() + "; this client stops renewing its lease")
	}
	return o
}

func validateDshRoute(r dshdesktop.Route) error {
	if err := dshdesktop.ValidateRoute(r); err != nil {
		return err
	}
	return privateDshDirectory(r.EventsDir)
}

func privateDshDirectory(directory string) error {
	if err := dshdesktop.ValidateDirectory(directory); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(directory)
	if err != nil || real != directory {
		return errors.New("event directory wants a canonical path without symlink ancestors")
	}
	return nil
}

func saveDshEvent(directory, id string, data []byte) error {
	if len(data) > maxDshEvent {
		return errors.New("saved event exceeds 64 KiB")
	}
	if err := privateDshDirectory(directory); err != nil {
		return err
	}
	path, err := dshdesktop.SavedPath(directory, id)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0600, atomicfile.NoReplace())
}

// dshAnswered reads one existing bus-owned answer field. It never scans the
// history hash, writes a receipt, or infers model proof from native acceptance.
func dshAnswered(s *session, id string) (bool, error) {
	ctx, cancel := call()
	defer cancel()
	value, err := s.hooks.C.HGet(ctx, bus.AnswersOf(s.as), id).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stamp, err := strconv.ParseInt(value, 10, 64)
	if err != nil || stamp <= 0 {
		return false, errors.New("bus answer wants a valid store timestamp")
	}
	return true, nil
}
