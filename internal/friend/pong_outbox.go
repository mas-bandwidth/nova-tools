package friend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

const NativePongDir = "native-pongs"

type NativePong struct {
	Friend, Session, Nonce, Capability string
	Queue, Working, Width              int
}

type nativePongGrant struct {
	Friend, Session, Nonce, Seat, Capability string
}

func validNativeNonce(nonce string) bool {
	if nonce == "" || len(nonce) > 64 {
		return false
	}
	for _, c := range nonce {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", c) {
			return false
		}
	}
	return true
}

func nativePongPath(dir, nonce string) string {
	return filepath.Join(dir, "outbox", NativePongDir, nonce+".json")
}

func nativePongGrantPath(broker, nonce string) string {
	return filepath.Join(broker, "native-pong-grants", nonce+".json")
}

func nativePongReceiptPath(broker, nonce string) string {
	return filepath.Join(broker, "native-pong-receipts", nonce+".json")
}

func nativePongToken(friend, nonce string) string {
	sum := sha256.Sum256([]byte(friend + "\x00" + nonce))
	return "native-pong:" + hex.EncodeToString(sum[:])
}

func (d *Daemon) nativePongCurrent(nonce string) bool {
	if d.m != nil && d.m.Challenge != Quiet && d.m.Nonce == nonce {
		return true
	}
	return d.NativePongCurrent != nil && d.NativePongCurrent(nonce)
}

// GrantNativePong places an unguessable challenge/session binding outside the
// wall. The native turn sees only the command with this capability; no daemon
// process can manufacture the native envelope it later needs to send a pong.
func (d *Daemon) GrantNativePong(session, nonce, seat string) (string, error) {
	broker, err := d.nativeBrokerRoot()
	if err != nil {
		return "", err
	}
	if d.Harness != "opencode" || !validNativeNonce(nonce) || !validNativeSession(session) || seat == "" || !d.nativePongCurrent(nonce) {
		return "", errors.New("native pong grant needs a current challenge, lane session and coordinator")
	}
	lanes, err := ReadLanes(d.StateDir)
	if err != nil || !nativeSessionPresent(lanes, session) {
		return "", errors.New("native pong grant needs a durable lane session")
	}
	path := nativePongGrantPath(broker, nonce)
	var old nativePongGrant
	if found, err := read(path, &old); err != nil {
		return "", errors.New("native pong grant cannot be read")
	} else if found && old.Friend == d.Friend && old.Session == session && old.Nonce == nonce && old.Seat == seat && len(old.Capability) == 64 && isLowerHex(old.Capability) {
		return old.Capability, nil
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("native pong grant lacks random bytes")
	}
	grant := nativePongGrant{Friend: d.Friend, Session: session, Nonce: nonce, Seat: seat, Capability: hex.EncodeToString(random[:])}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", errors.New("native pong grant directory cannot be created")
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return "", errors.New("native pong grant cannot be encoded")
	}
	if err := atomicfile.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", errors.New("native pong grant cannot be saved")
	}
	return grant.Capability, nil
}

func CheckNativePong(p NativePong) error {
	if bus.CheckName(p.Friend) != "" || !validNativeSession(p.Session) ||
		!validNativeNonce(p.Nonce) || len(p.Capability) != 64 || !isLowerHex(p.Capability) ||
		p.Queue < 0 || p.Working < 0 || p.Width < 0 || p.Queue > 1000000 || p.Working > 1000000 || p.Width > 1000000 {
		return errors.New("local pong needs a valid friend, session, current nonce, capability and bounded nonnegative counts")
	}
	return nil
}

// QueueNativePong writes only to the friend's owner-only outbox. It neither
// reads daemon control state nor opens a bus socket.
func QueueNativePong(dir string, p NativePong) error {
	if err := CheckNativePong(p); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("local pong needs a clean absolute friend directory")
	}
	if physical, err := filepath.EvalSymlinks(dir); err != nil || physical != dir {
		return errors.New("local pong needs the real friend directory")
	}
	outbox := filepath.Join(dir, "outbox")
	info, err := os.Lstat(outbox)
	if err != nil || !info.IsDir() {
		return errors.New("local pong needs this friend's real outbox directory")
	}
	spool := filepath.Join(outbox, NativePongDir)
	if err := os.Mkdir(spool, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return errors.New("local pong outbox cannot be created")
	}
	info, err = os.Lstat(spool)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("local pong outbox must be a real owner-only directory")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return errors.New("local pong envelope cannot be encoded")
	}
	if err := atomicfile.WriteFile(nativePongPath(dir, p.Nonce), append(raw, '\n'), 0o600, atomicfile.NoReplace()); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errors.New("this challenge already has a local pong queued")
		}
		return errors.New("local pong envelope cannot be saved")
	}
	return nil
}

func readNativePong(path string) (NativePong, bool, error) {
	var p NativePong
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return p, false, errors.New("local pong envelope is not a bounded regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, false, errors.New("local pong envelope cannot be read")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, false, errors.New("local pong envelope is invalid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, false, errors.New("local pong envelope has trailing data")
	}
	return p, true, nil
}

func (d *Daemon) SendNativePongs(ctx context.Context, b *bus.Bus, now time.Time, seat string, cursor *string) {
	if d.Harness != "opencode" || d.StateDir == "" || b == nil {
		return
	}
	if _, err := d.nativeBrokerRoot(); err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " local pong: broker unavailable: " + oneLine(err.Error(), 200))
		return
	}
	dir := filepath.Join(d.Dir, "outbox", NativePongDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// ignored: the native spool is absent until the first local pong is queued.
		return
	}
	if err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " local pong: cannot read outbox: " + oneLine(err.Error(), 200))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, nativeReplyBudget)
	defer cancel()
	start := 0
	for start < len(entries) && entries[start].Name() <= *cursor {
		start++
	}
	if start == len(entries) {
		start = 0
	}
	for n := 0; n < len(entries) && n < nativeReplyPass; n++ {
		entry := entries[(start+n)%len(entries)]
		*cursor = entry.Name()
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			continue
		}
		if err := d.sendNativePong(ctx, b, filepath.Join(dir, entry.Name()), entry.Name(), now, seat); err != nil {
			d.Record(now.UTC().Format(time.RFC3339) + " local pong: " + entry.Name() + ": " + oneLine(err.Error(), 200))
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (d *Daemon) sendNativePong(ctx context.Context, b *bus.Bus, path, name string, now time.Time, seat string) error {
	p, found, err := readNativePong(path)
	if err != nil || !found || CheckNativePong(p) != nil || p.Friend != d.Friend || name != p.Nonce+".json" {
		return errors.New("local pong envelope is invalid")
	}
	broker, err := d.nativeBrokerRoot()
	if err != nil {
		return err
	}
	var grant nativePongGrant
	if found, err := read(nativePongGrantPath(broker, p.Nonce), &grant); err != nil || !found ||
		grant.Friend != d.Friend || grant.Session != p.Session || grant.Nonce != p.Nonce ||
		grant.Capability != p.Capability || grant.Seat != seat {
		return errors.New("local pong has no matching native challenge grant")
	}
	receiptPath := nativePongReceiptPath(broker, p.Nonce)
	var receipt nativeReplyReceipt
	if _, err := read(receiptPath, &receipt); err != nil {
		return errors.New("daemon pong receipt cannot be read")
	}
	if receipt.Sent {
		return d.reconcileNativePong(p, grant, receipt)
	}
	if !d.nativePongCurrent(p.Nonce) {
		return errors.New("local pong challenge is no longer current")
	}
	lanes, err := ReadLanes(d.StateDir)
	if err != nil || !nativeSessionPresent(lanes, p.Session) {
		return errors.New("local pong session is not a current native lane")
	}
	if !receipt.AttemptedAt.IsZero() && !now.Before(receipt.AttemptedAt.Add(bus.DefaultTokenLife)) {
		return errors.New("idempotent pong window elapsed; reconcile before another attempt")
	}
	m := bus.Message{From: d.Friend, To: []string{grant.Seat}, Subject: PongSubject,
		Body: PongLine(p.Nonce, p.Queue, p.Working, p.Width) + "\n", Token: nativePongToken(d.Friend, p.Nonce)}
	if receipt.AttemptedAt.IsZero() {
		if _, err := b.Check(ctx, m); err != nil {
			return errors.New("bus refused the local pong check")
		}
		receipt.AttemptedAt = now
		if err := saveNativeReplyReceipt(receiptPath, receipt); err != nil {
			return errors.New("local pong attempt marker cannot be saved")
		}
	}
	sent, err := b.Send(ctx, m)
	if err != nil {
		return errors.New("bus send did not answer local pong")
	}
	receipt.Sent, receipt.ID, receipt.SentAt = true, sent.ID, sent.At
	if err := saveNativeReplyReceipt(receiptPath, receipt); err != nil {
		return errors.New("bus sent local pong but receipt cannot be saved")
	}
	if err := d.reconcileNativePong(p, grant, receipt); err != nil {
		return errors.New("bus sent local pong but status file cannot be saved")
	}
	d.Record(fmt.Sprintf("%s local pong: sent nonce=%s id=%s", now.UTC().Format(time.RFC3339), p.Nonce, sent.ID))
	return nil
}

func (d *Daemon) reconcileNativePong(p NativePong, grant nativePongGrant, receipt nativeReplyReceipt) error {
	if !receipt.Sent || receipt.ID == "" || receipt.SentAt.IsZero() {
		return errors.New("sent local pong receipt lacks its bus timestamp")
	}
	old, found, err := ReadPong(d.StateDir)
	if err == nil && found && (old.At.After(receipt.SentAt) ||
		(old.At.Equal(receipt.SentAt) && old.Nonce == p.Nonce && old.To == grant.Seat &&
			old.Queue == p.Queue && old.Working == p.Working && old.Width == p.Width)) {
		return nil // never replace a newer native answer with an older retry
	}
	return WritePong(d.StateDir, Pong{Nonce: p.Nonce, At: receipt.SentAt, To: grant.Seat, Queue: p.Queue, Working: p.Working, Width: p.Width})
}
