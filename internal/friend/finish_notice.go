package friend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// finish-notices is the delivery debt for one-shot lane completions. A lane's
// durable Started entry is kept until its note is saved here. The bus token
// then makes a lost send response safe to retry after a daemon restart.
const FinishNoticesDir = "finish-notices"

// One daemon step spends at most this shared bus budget and tries at most
// this many notice files. The rotating cursor keeps other notes reachable.
const (
	FinishNoticePassBudget = 250 * time.Millisecond
	FinishNoticePassLimit  = 8
)

type finishNotice struct {
	Job         string      `json:"job"`
	Message     bus.Message `json:"message"`
	AttemptedAt time.Time   `json:"attempted_at,omitzero"`
	Sent        bool        `json:"sent,omitempty"`
	ID          string      `json:"id,omitempty"`
}

func (d *Daemon) finishNoticesDir() string {
	state := d.StateDir
	if state == "" {
		state = StateDirIn(d.Dir)
	}
	return filepath.Join(state, FinishNoticesDir)
}

func finishNoticePath(d *Daemon, job string) string {
	return filepath.Join(d.finishNoticesDir(), job+".json")
}

func finishNoticeToken(friend, job string) string {
	sum := sha256.Sum256([]byte(friend + "\x00" + job))
	return "finish-note:" + hex.EncodeToString(sum[:])
}

func sameFinishMessage(a, b bus.Message) bool {
	return a.From == b.From && slices.Equal(a.To, b.To) && slices.Equal(a.CC, b.CC) &&
		a.Subject == b.Subject && a.Re == b.Re && a.Kind == b.Kind &&
		a.Body == b.Body && a.Token == b.Token
}

// finishNote saves the exact message before a completed lane loses its
// Started entry. An existing entry wins on recovery: its recipient and bytes
// cannot drift and conflict with the bus's token fingerprint.
func (l *loop) finishNote(card Card, wall time.Duration, now time.Time) error {
	d := l.d
	if d.Rules == nil {
		return nil
	}
	job := filepath.Base(card.Outbox)
	if !validJob(job) {
		return fmt.Errorf("invalid finished job %q", job)
	}
	path := finishNoticePath(d, job)
	var prior finishNotice
	if found, err := read(path, &prior); err != nil {
		return err
	} else if found {
		if prior.Job != job || prior.Message.Token != finishNoticeToken(d.Friend, job) || prior.Message.From != d.Friend {
			return fmt.Errorf("%s: the saved finish note has another job or token", path)
		}
		return nil
	}
	to := l.coordinator()
	if to == "" {
		return fmt.Errorf("no coordinator to tell of completed job %s", job)
	}
	var ln *lane
	for _, candidate := range l.lanes.lanes {
		if candidate.card != nil && filepath.Base(candidate.card.Outbox) == job {
			ln = candidate
			break
		}
	}
	spent := LaneTokens{Tokens: cardcost.None()}
	if ln != nil {
		if cur, ok := l.tokens(ln.session); ok && ln.baseOK {
			spent = cur.Sub(ln.base)
		}
	}
	var rp RoutePrice
	if d.Route != nil {
		rp = d.Route()
	}
	if d.Tokens != nil {
		if err := PublishCost(card.Outbox, spent, rp, d.Model); err != nil {
			d.Record(fmt.Sprintf("%s cost: %s: %s", now.UTC().Format(time.RFC3339), card.ID, oneLine(err.Error(), 300)))
		}
	}
	verdict := ""
	if raw, err := os.ReadFile(card.Report()); err == nil {
		verdict, _ = reportLine(string(raw), "Verdict")
	}
	cost := CostOf(spent.Tokens, rp, d.Model)
	subject, body := FinishNote(d.Friend, job, verdict, cost, wall)
	note := finishNotice{Job: job, Message: bus.Message{From: d.Friend, To: []string{to}, Subject: subject, Body: subject + "\n" + body, Token: finishNoticeToken(d.Friend, job)}}
	raw, err := json.MarshalIndent(note, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(raw, '\n'), 0o644, atomicfile.NoReplace()); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if _, err := read(path, &prior); err != nil {
			return err
		}
		if prior.Job != job || !sameFinishMessage(prior.Message, note.Message) {
			return fmt.Errorf("%s: another finish note already owns this job", path)
		}
	}
	return nil
}

// sendFinishNotes retries a bounded batch under one bus budget per daemon
// step. Failed sends keep the same token. The cursor prevents a slow first
// note from starving later notes; healthy bursts drain several per step.
func (l *loop) sendFinishNotes(now time.Time) {
	dir := l.d.finishNoticesDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: cannot read pending notices: " + oneLine(err.Error(), 300))
		return
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	files := entries[:0]
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry)
		}
	}
	if len(files) == 0 {
		return
	}
	i := 0
	for i < len(files) && files[i].Name() <= l.finishCursor {
		i++
	}
	if i == len(files) {
		i = 0
	}
	ctx, cancel := context.WithTimeout(l.ctx, FinishNoticePassBudget)
	defer cancel()
	for n := 0; n < len(files) && n < FinishNoticePassLimit; n++ {
		entry := files[(i+n)%len(files)]
		l.finishCursor = entry.Name()
		l.sendFinishNotice(ctx, filepath.Join(dir, entry.Name()), entry.Name(), now)
		if ctx.Err() != nil {
			return
		}
	}
}

func (l *loop) sendFinishNotice(ctx context.Context, path, name string, now time.Time) {
	var note finishNotice
	if _, err := read(path, &note); err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + oneLine(err.Error(), 300))
		return
	}
	if note.Job+".json" != name || !validJob(note.Job) || note.Message.Token != finishNoticeToken(l.d.Friend, note.Job) {
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: invalid saved notice " + name)
		return
	}
	if !note.Sent {
		if l.b == nil {
			return
		}
		if !note.AttemptedAt.IsZero() && !now.Before(note.AttemptedAt.Add(bus.DefaultTokenLife)) {
			l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " remains owed; the idempotent send window elapsed, reconcile its token before another attempt")
			return
		}
		if note.AttemptedAt.IsZero() {
			// A failed read-only check cannot have sent anything. Keep the
			// retry window open through a long store or roster outage.
			if _, err := l.b.Check(ctx, note.Message); err != nil {
				l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " remains pending: " + oneLine(err.Error(), 300))
				return
			}
			if ctx.Err() != nil {
				return
			}
			note.AttemptedAt = now
			if err := write(path, note); err != nil {
				l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " remains owed; first-attempt marker cannot be written: " + oneLine(err.Error(), 300))
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		got, err := l.b.Send(ctx, note.Message)
		if err != nil {
			l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " remains pending: " + oneLine(err.Error(), 300))
			return
		}
		note.Sent, note.ID = true, got.ID
		if err := write(path, note); err != nil {
			l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " sent but receipt remains pending: " + oneLine(err.Error(), 300))
			return
		}
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " sent id=" + got.ID)
	}
	// Retain the sent marker while a crashed daemon's Started entry might
	// still be on disk. It prevents a restart from creating another send.
	if l.lanes != nil {
		if _, started := l.lanes.state.Started[note.Job]; started {
			return
		}
	}
	state, err := ReadLanes(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " receipt held; lane state cannot be read: " + oneLine(err.Error(), 300))
		return
	}
	if _, started := state.Started[note.Job]; started {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		l.d.Record(now.UTC().Format(time.RFC3339) + " finish notes: " + note.Job + " receipt cleanup failed: " + oneLine(err.Error(), 300))
	}
}
