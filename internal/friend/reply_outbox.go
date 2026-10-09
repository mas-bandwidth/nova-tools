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
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// NativeReplyDir is a file handoff from a sandboxed OpenCode turn to its
// daemon. The native verb never opens the bus or chooses a recipient.
const NativeReplyDir = "native-replies"

const (
	nativeReplyMaxFile = bus.MaxBody + 4096
	nativeReplyPass    = 8
	nativeReplyBudget  = 250 * time.Millisecond
)

type NativeReply struct {
	Friend     string `json:"friend"`
	Session    string `json:"session"`
	Re         string `json:"re"`
	Capability string `json:"capability"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	// The native file has no transport state: only the daemon writes receipts.
}

type nativeReplyReceipt struct {
	AttemptedAt time.Time `json:"attempted_at,omitzero"`
	SentAt      time.Time `json:"sent_at,omitzero"`
	Sent        bool      `json:"sent,omitempty"`
	ID          string    `json:"id,omitempty"`
}

func nativeReplyReceiptPath(state, re string) string {
	return filepath.Join(state, "native-reply-receipts", re+".json")
}

func nativeReplyPath(dir, re string) string {
	return filepath.Join(dir, "outbox", NativeReplyDir, re+".json")
}

func nativeReplyGrantPath(state, re string) string {
	return filepath.Join(state, "native-reply-grants", re+".json")
}

func (d *Daemon) nativeBrokerRoot() (string, error) {
	root := d.NativeBrokerDir
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(d.Dir) {
		return "", errors.New("native broker needs an absolute private directory")
	}
	rel, err := filepath.Rel(d.Dir, root)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", errors.New("native broker directory must be outside the friend's working directory")
	}
	// The grant is the proof that a native turn may send. Never put it in a
	// symlinked tree (which could resolve back into the native writable work).
	work, err := filepath.EvalSymlinks(d.Dir)
	if err != nil {
		return "", errors.New("native broker cannot resolve friend directory")
	}
	// Provision a fresh profile without following an existing symlink in any
	// component. The broker itself is owner-only; the older state parent may
	// already be 0755, but the native wall cannot read outside its work root.
	parts := strings.Split(strings.TrimPrefix(root, string(filepath.Separator)), string(filepath.Separator))
	path := string(filepath.Separator)
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(path, 0o700); err != nil {
				return "", errors.New("native broker directory cannot be created")
			}
			info, err = os.Lstat(path)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("native broker path must contain only real directories")
		}
		if i == len(parts)-1 && info.Mode().Perm()&0o077 != 0 {
			return "", errors.New("native broker directory must be owner-only")
		}
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil || physical != root {
		return "", errors.New("native broker directory must be real")
	}
	rel, err = filepath.Rel(work, physical)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", errors.New("native broker directory resolves into friend work")
	}
	if d.NativeBrokerRoots != nil {
		if err := nativeBrokerOutsideRoots(root, d.NativeBrokerRoots()); err != nil {
			return "", err
		}
	}
	return root, nil
}

// ValidateNativeBrokerRoots refuses a lane launch when its exact wall snapshot
// would make even an older durable grant readable by the native child.
func (d *Daemon) ValidateNativeBrokerRoots(roots []string) error {
	root, err := d.nativeBrokerRoot()
	if err != nil {
		return err
	}
	return nativeBrokerOutsideRoots(root, roots)
}

func nativeBrokerOutsideRoots(root string, roots []string) error {
	for _, allowed := range roots {
		if allowed == "" {
			continue
		}
		if !filepath.IsAbs(allowed) || filepath.Clean(allowed) != allowed {
			return errors.New("native broker cannot verify a wall root")
		}
		for _, candidate := range []string{allowed, resolvedNativePath(allowed)} {
			if candidate == "" {
				continue
			}
			if withinNativeRoot(candidate, root) || withinNativeRoot(root, candidate) {
				return errors.New("native broker overlaps a wall-visible root")
			}
		}
	}
	return nil
}

func resolvedNativePath(path string) string {
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return physical
}

func withinNativeRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func nativeSessionPresent(lanes LaneState, session string) bool {
	for _, id := range lanes.Sessions {
		if id == session {
			return true
		}
	}
	return false
}

type nativeReplyGrant struct {
	Friend, Session, Re, Seat, Capability string
}

// GrantNativeReply is called only while constructing the native turn that
// actually carries a coordinator request. Its capability is included in that
// turn, but its grant stays in daemon-owned state outside the write spool.
func (d *Daemon) GrantNativeReply(session string, m bus.Message, seat string) (string, error) {
	broker, err := d.nativeBrokerRoot()
	if err != nil {
		return "", err
	}
	if d.Harness != "opencode" || !validNativeSession(session) || seat == "" ||
		m.Kind != bus.KindRequest || m.From != seat || !slices.Contains(m.To, d.Friend) || !validNativeReplyID(m.ID) {
		return "", errors.New("native reply grant needs a coordinator request in this session's turn")
	}
	lanes, err := ReadLanes(d.StateDir)
	if err != nil || !nativeSessionPresent(lanes, session) {
		return "", errors.New("native reply grant needs a durable lane session")
	}
	path := nativeReplyGrantPath(broker, m.ID)
	var prior nativeReplyGrant
	if found, err := read(path, &prior); err != nil {
		return "", errors.New("native reply grant cannot be read")
	} else if found && prior.Friend == d.Friend && prior.Session == session && prior.Re == m.ID && prior.Seat == seat && len(prior.Capability) == 64 && isLowerHex(prior.Capability) {
		return prior.Capability, nil
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("native reply grant lacks random bytes")
	}
	grant := nativeReplyGrant{Friend: d.Friend, Session: session, Re: m.ID, Seat: seat, Capability: hex.EncodeToString(nonce[:])}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", errors.New("native reply grant directory cannot be created")
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return "", errors.New("native reply grant cannot be encoded")
	}
	if err := atomicfile.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", errors.New("native reply grant cannot be saved")
	}
	return grant.Capability, nil
}

func nativeReplyToken(friend, re string) string {
	sum := sha256.Sum256([]byte(friend + "\x00" + re))
	return "native-reply:" + hex.EncodeToString(sum[:])
}

func validNativeReplyID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, ch := range id {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", ch) {
			return false
		}
	}
	return true
}

func validNativeSession(session string) bool {
	if session == "" || len(session) > 128 {
		return false
	}
	for _, c := range session {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", c) {
			return false
		}
	}
	return true
}

// CheckNativeReply validates only the native caller's envelope shape. The
// daemon alone checks the grant, lane and bus request before any send.
func CheckNativeReply(r NativeReply) error {
	if !validNativeReplyID(r.Re) || bus.CheckName(r.Friend) != "" || !validNativeSession(r.Session) ||
		len(r.Capability) != 64 || !isLowerHex(r.Capability) ||
		strings.TrimSpace(r.Subject) == "" || strings.ContainsAny(r.Subject, "\n\r") ||
		strings.TrimSpace(r.Body) == "" || len(r.Body) > bus.MaxBody {
		return errors.New("reply needs a valid friend, native session, request id, capability, one-line subject and bounded body")
	}
	return nil
}

// QueueNativeReply records one native action for one request. A second file
// for the same request is refused; only the daemon later verifies delivery
// and sends. The immutable private envelope is the source of the reply text.
func QueueNativeReply(dir string, r NativeReply) error {
	if err := CheckNativeReply(r); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("reply needs a clean absolute friend directory")
	}
	if physical, err := filepath.EvalSymlinks(dir); err != nil || physical != dir {
		return errors.New("reply needs the real friend directory")
	}
	outbox := filepath.Join(dir, "outbox")
	info, err := os.Lstat(outbox)
	if err != nil || !info.IsDir() {
		return errors.New("reply needs this friend's real outbox directory")
	}
	spool := filepath.Join(outbox, NativeReplyDir)
	if err := os.Mkdir(spool, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("reply outbox cannot be created: %w", err)
	}
	spoolInfo, err := os.Lstat(spool)
	if err != nil || !spoolInfo.IsDir() || spoolInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("reply outbox must be a real owner-only directory")
	}
	path := nativeReplyPath(dir, r.Re)
	if err := saveNativeReply(path, r, atomicfile.NoReplace()); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errors.New("this request already has a native reply queued")
		}
		return fmt.Errorf("reply envelope cannot be saved: %w", err)
	}
	return nil
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func saveNativeReply(path string, r NativeReply, options ...atomicfile.Option) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return errors.New("reply envelope cannot be encoded")
	}
	return atomicfile.WriteFile(path, append(raw, '\n'), 0o600, options...)
}

func readNativeReply(path string) (NativeReply, bool, error) {
	var r NativeReply
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > nativeReplyMaxFile {
		return r, false, errors.New("reply envelope is not a bounded regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, false, errors.New("reply envelope cannot be read")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, false, errors.New("reply envelope is invalid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return r, false, errors.New("reply envelope has trailing data")
	}
	return r, true, nil
}

// SendNativeReplies is a bounded daemon pass. It derives the only recipient
// from the original typed bus request, never from the native file. A stable
// bus token makes a lost send answer safe to retry until its token window ends.
func (d *Daemon) SendNativeReplies(ctx context.Context, b *bus.Bus, now time.Time, seat string, cursor *string) {
	if d.Harness != "opencode" || d.StateDir == "" || b == nil {
		return
	}
	if _, err := d.nativeBrokerRoot(); err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " native reply: broker unavailable: " + oneLine(err.Error(), 200))
		return
	}
	dir := filepath.Join(d.Dir, "outbox", NativeReplyDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// ignored: the native spool is absent until the first reply is queued.
		return
	}
	if err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " native reply: cannot read outbox: " + oneLine(err.Error(), 200))
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
		if ctx.Err() != nil {
			return
		}
		if err := d.sendNativeReply(ctx, b, filepath.Join(dir, entry.Name()), entry.Name(), now, seat); err != nil {
			d.Record(now.UTC().Format(time.RFC3339) + " native reply: " + entry.Name() + ": " + oneLine(err.Error(), 200))
		}
	}
}

func (d *Daemon) sendNativeReply(ctx context.Context, b *bus.Bus, path, name string, now time.Time, seat string) error {
	r, found, err := readNativeReply(path)
	if err != nil || !found {
		return errors.New("envelope cannot be read")
	}
	if r.Friend != d.Friend || !validNativeReplyID(r.Re) || name != r.Re+".json" || !validNativeSession(r.Session) ||
		len(r.Capability) != 64 || !isLowerHex(r.Capability) ||
		strings.TrimSpace(r.Subject) == "" || strings.ContainsAny(r.Subject, "\r\n") || strings.TrimSpace(r.Body) == "" || len(r.Body) > bus.MaxBody {
		return errors.New("envelope identity or content is invalid")
	}
	broker, err := d.nativeBrokerRoot()
	if err != nil {
		return err
	}
	var grant nativeReplyGrant
	if found, err := read(nativeReplyGrantPath(broker, r.Re), &grant); err != nil || !found ||
		grant.Friend != d.Friend || grant.Session != r.Session || grant.Re != r.Re ||
		grant.Capability != r.Capability || grant.Seat != seat {
		return errors.New("envelope has no matching native-turn grant")
	}
	lanes, err := ReadLanes(d.StateDir)
	if err != nil || !nativeSessionPresent(lanes, r.Session) {
		return errors.New("session is not a current native lane")
	}
	receiptPath := nativeReplyReceiptPath(broker, r.Re)
	var receipt nativeReplyReceipt
	if _, err := read(receiptPath, &receipt); err != nil {
		return errors.New("daemon reply receipt cannot be read")
	}
	if receipt.Sent {
		return nil
	}
	if seat == "" {
		return errors.New("coordinator seat is unknown")
	}
	// The private grant was issued from the delivered typed request above.
	// Message ULIDs are not Redis stream entry IDs, so Store.Get cannot look
	// them up. The bus stage below independently proves this ID was read.
	stages, _, err := b.Stages(ctx, d.Friend, r.Re)
	if err != nil || len(stages) != 1 || (stages[0].State != bus.Read && stages[0].State != bus.Acted) {
		return errors.New("original request has no native-turn read receipt")
	}
	m := bus.Message{From: d.Friend, To: []string{grant.Seat}, Re: r.Re, Subject: r.Subject, Body: r.Body, Token: nativeReplyToken(d.Friend, r.Re)}
	if !receipt.AttemptedAt.IsZero() && !now.Before(receipt.AttemptedAt.Add(bus.DefaultTokenLife)) {
		return errors.New("idempotent send window elapsed; reconcile before another attempt")
	}
	if receipt.AttemptedAt.IsZero() {
		if _, err := b.Check(ctx, m); err != nil {
			return errors.New("bus refused the reply check")
		}
		receipt.AttemptedAt = now
		if err := saveNativeReplyReceipt(receiptPath, receipt); err != nil {
			return errors.New("attempt marker cannot be saved")
		}
	}
	sent, err := b.Send(ctx, m)
	if err != nil {
		return errors.New("bus send did not answer")
	}
	receipt.Sent, receipt.ID = true, sent.ID
	if err := saveNativeReplyReceipt(receiptPath, receipt); err != nil {
		return errors.New("bus sent the reply but receipt cannot be saved")
	}
	d.Record(now.UTC().Format(time.RFC3339) + " native reply: sent re=" + r.Re + " id=" + sent.ID)
	return nil
}

func saveNativeReplyReceipt(path string, receipt nativeReplyReceipt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(raw, '\n'), 0o600)
}
