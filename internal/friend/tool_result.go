package friend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/readregular"
)

// ToolResultLease bounds an armed root-cell wait. A stopped cell cannot renew
// it; no receipt, presence or permanent wake is inferred (ToolResultBinding).
const ToolResultLease = 30 * time.Second
const toolResultMax = 128 << 10

var toolResultName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var toolResultSession = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ToolResultScope names one actual Codex root and its independent reply route.
// Payload text is untrusted data; the scope is the explicit receiver binding.
type ToolResultScope struct {
	Session   string `json:"session"`
	Namespace string `json:"namespace"`
	Server    string `json:"server"`
}

func (s ToolResultScope) Validate() error {
	if !toolResultSession.MatchString(s.Session) || !toolResultName.MatchString(s.Namespace) {
		return errors.New("tool-result wants an exact Codex session UUID and a namespace of letters, digits, dots, underscores or hyphens")
	}
	host, port, err := net.SplitHostPort(s.Server)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || host == "" || strings.ContainsAny(s.Server, "@/?# \t\r\n") {
		return errors.New("tool-result wants a credential-free reply server host:port")
	}
	return nil
}

// ToolResultEvent is the immutable durable transport record. Its emission is
// not a completed native turn or a seat pong (ToolResultBinding, Deliver).
type ToolResultEvent struct {
	Scope   ToolResultScope `json:"scope"`
	ID      string          `json:"id"`
	Text    string          `json:"text"`
	Created time.Time       `json:"created"`
}
type toolResultBinding struct {
	Scope ToolResultScope `json:"scope"`
	Owner string          `json:"owner"`
	Until time.Time       `json:"until"`
}

// ToolResult is an explicitly chosen delivery route and an active root-cell
// receiver. It never invokes a Codex queue, resume, app socket or native child.
type ToolResult struct {
	Dir   string
	Scope ToolResultScope
	Owner string
	Now   func() time.Time
}

func (r ToolResult) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// realToolResultPath refuses links before any transport read or write.
func realToolResultPath(path string, directory bool) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || (directory && !fi.IsDir()) || (!directory && !fi.Mode().IsRegular()) {
		return fmt.Errorf("tool-result path %s is not a real %s", path, map[bool]string{true: "directory", false: "file"}[directory])
	}
	return nil
}
func (r ToolResult) lock(create bool) (*filelock.FileLock, error) {
	if err := r.Scope.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(r.Dir) {
		return nil, errors.New("tool-result wants an absolute owned directory")
	}
	for dir := filepath.Clean(r.Dir); ; dir = filepath.Dir(dir) {
		if err := realToolResultPath(dir, true); err != nil {
			return nil, err
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if create {
		if err := os.MkdirAll(r.Dir, 0o700); err != nil {
			return nil, err
		}
	}
	if fi, err := os.Stat(r.Dir); err != nil || !fi.IsDir() {
		return nil, errors.New("tool-result directory is absent; run the root tool-result wait first")
	}
	for _, name := range []string{".tool-result.lock", "binding.json"} {
		if err := realToolResultPath(filepath.Join(r.Dir, name), false); err != nil {
			return nil, err
		}
	}
	return filelock.Lock(filepath.Join(r.Dir, ".tool-result.lock"), "tool-result transport", 200*time.Millisecond)
}
func toolResultRead(path string, value any) (bool, error) {
	if err := realToolResultPath(path, false); err != nil {
		return false, err
	}
	raw, err := readregular.Read(path, toolResultMax)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return false, errors.New("tool-result state is malformed JSON; preserve the file and inspect it")
	}
	return true, nil
}
func toolResultWrite(path string, value any) error {
	if err := realToolResultPath(path, false); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > toolResultMax {
		return errors.New("tool-result record exceeds 128 KiB; split the payload")
	}
	if err = atomicfile.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return toolResultSync(filepath.Dir(path))
}
func toolResultSync(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close() // ignored: only the directory sync writes anything
	return f.Sync()
}
func (r ToolResult) binding() (toolResultBinding, bool, error) {
	var b toolResultBinding
	ok, err := toolResultRead(filepath.Join(r.Dir, "binding.json"), &b)
	if err == nil && ok {
		if err = b.Scope.Validate(); err == nil && !toolResultName.MatchString(b.Owner) {
			err = errors.New("tool-result binding has no valid owner")
		}
	}
	return b, ok, err
}
func (r ToolResult) live(b toolResultBinding) bool {
	delta := b.Until.Sub(r.now())
	return b.Scope == r.Scope && delta > 0 && delta <= ToolResultLease
}
func toolResultID(scope ToolResultScope, text string) string {
	sum := sha256.Sum256([]byte(scope.Session + "\x00" + scope.Namespace + "\x00" + scope.Server + "\x00" + text))
	return hex.EncodeToString(sum[:])
}
func (r ToolResult) event(id string) (ToolResultEvent, bool, error) {
	var event ToolResultEvent
	if len(id) != 64 {
		return event, false, errors.New("tool-result wants a 64-character event id")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return event, false, errors.New("tool-result event id is not hexadecimal")
	}
	for _, suffix := range []string{".pending.json", ".event.json", ".emitted.json"} {
		ok, err := toolResultRead(filepath.Join(r.Dir, id+suffix), &event)
		if err != nil {
			return event, false, err
		}
		if ok {
			if event.Scope != r.Scope || event.ID != id || event.ID != toolResultID(event.Scope, event.Text) {
				return event, false, errors.New("tool-result event scope or digest does not match its binding")
			}
			return event, true, nil
		}
	}
	return event, false, nil
}

// Retain stages a judgment in an already explicitly bound owned directory.
// It is neither delivery nor a lease renewal; Wait exposes it only after Arm
// (ToolResultBinding, Stage). This preserves inbox notes across receiver gaps.
func (r ToolResult) Retain(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := r.lock(false)
	if err != nil {
		return err
	}
	defer lock.Unlock() // ignored: the durable staging result remains returned
	b, ok, err := r.binding()
	if err != nil {
		return err
	}
	if !ok || b.Scope != r.Scope {
		return errors.New("tool-result pending judgment wants its previously bound exact scope; run the root wait first")
	}
	if text == "" || len(text) > 64<<10 {
		return errors.New("tool-result wants a nonempty payload of at most 64 KiB")
	}
	id := toolResultID(r.Scope, text)
	if _, ok, err := r.event(id); err != nil || ok {
		return err
	}
	return toolResultWrite(filepath.Join(r.Dir, id+".pending.json"), ToolResultEvent{Scope: r.Scope, ID: id, Text: text, Created: r.now()})
}

// Deliver only publishes when the actual receiver has armed a matching live
// lease. Absent, expired or wrong-scope receivers defer without any CLI call.
func (r ToolResult) Deliver(ctx context.Context, text string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	lock, err := r.lock(false)
	if err != nil {
		return 0, Deferred{Reason: err.Error()}
	}
	defer lock.Unlock() // ignored: the durable write below is the delivery result
	b, ok, err := r.binding()
	if err != nil {
		return 0, err
	}
	if !ok || !r.live(b) {
		return 0, Deferred{Reason: "tool-result receiver is absent, expired or bound to another session/namespace/server; keep pending until the root tool cell waits"}
	}
	if text == "" || len(text) > 64<<10 {
		return 0, errors.New("tool-result wants a nonempty payload of at most 64 KiB")
	}
	id := toolResultID(r.Scope, text)
	if _, ok, err := r.event(id); err != nil || ok {
		return 0, err
	}
	event := ToolResultEvent{Scope: r.Scope, ID: id, Text: text, Created: r.now()}
	return 0, toolResultWrite(filepath.Join(r.Dir, id+".event.json"), event)
}

// Wait renews the lease once from the root tool call, then returns one durable
// pending event. A timeout is empty, never proof. Interrupted waits retain data
// (ToolResultBinding, Arm and Output).
func (r ToolResult) Wait(ctx context.Context, timeout time.Duration) (*ToolResultEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !toolResultName.MatchString(r.Owner) || timeout < 0 || timeout > 20*time.Second {
		return nil, errors.New("tool-result wait wants a receiver owner and a timeout from 0s to 20s")
	}
	lock, err := r.lock(true)
	if err != nil {
		return nil, err
	}
	b, ok, err := r.binding()
	if err == nil && ok && r.now().Before(b.Until) && (b.Scope != r.Scope || b.Owner != r.Owner) {
		err = errors.New("tool-result directory already has a different live receiver; use its owner or another private directory")
	}
	// Install the kernel watch before publishing Arm or scanning durable files.
	// An event published between those operations is therefore never missed.
	var watch toolResultWatch
	if err == nil {
		watch, err = newToolResultWatch(r.Dir)
	}
	if watch != nil {
		defer watch.Close() // ignored: closing the observer cannot consume records
	}
	if err == nil {
		err = toolResultWrite(filepath.Join(r.Dir, "binding.json"), toolResultBinding{Scope: r.Scope, Owner: r.Owner, Until: r.now().Add(ToolResultLease)})
	}
	lock.Unlock() // ignored: publication errors remain returned
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		lock, err := r.lock(false)
		if err != nil {
			return nil, err
		}
		event, err := r.next()
		lock.Unlock() // ignored: read errors remain returned
		if err != nil || event != nil {
			return event, err
		}
		changed, err := watch.Wait(ctx, time.Until(deadline))
		if err != nil || !changed {
			return nil, err
		}
	}
}
func (r ToolResult) next() (*ToolResultEvent, error) {
	binding, ok, err := r.binding()
	if err != nil {
		return nil, err
	}
	if !ok || !r.live(binding) || binding.Owner != r.Owner {
		return nil, errors.New("tool-result wait lost its receiver lease; pending events preserved")
	}
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	var first string
	var oldest time.Time
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".event.json") && !strings.HasSuffix(entry.Name(), ".pending.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if first == "" || info.ModTime().Before(oldest) {
			first = entry.Name()
			oldest = info.ModTime()
		}
	}
	if first == "" {
		return nil, nil
	}
	id := strings.TrimSuffix(strings.TrimSuffix(first, ".event.json"), ".pending.json")
	event, ok, err := r.event(id)
	if err != nil || !ok {
		return nil, err
	}
	return &event, nil
}

// Ack is only the root cell's buffered-output emission receipt. It keeps the
// original event and never answers a nonce or marks a native turn completed
// (ToolResultBinding, Ack).
func (r ToolResult) Ack(id string) error {
	lock, err := r.lock(false)
	if err != nil {
		return err
	}
	defer lock.Unlock() // ignored: the record/rename result remains returned
	b, ok, err := r.binding()
	if err != nil {
		return err
	}
	if !ok || !r.live(b) || b.Owner != r.Owner {
		return errors.New("tool-result acknowledgement wants the matching live receiver")
	}
	event, ok, err := r.event(id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("tool-result event is absent; nothing acknowledged")
	}
	receipt := struct {
		ID      string          `json:"id"`
		Scope   ToolResultScope `json:"scope"`
		Emitted time.Time       `json:"tool_output_emitted"`
		Proof   string          `json:"proof"`
	}{id, event.Scope, r.now(), "transport emission only; root pong remains independent"}
	source := filepath.Join(r.Dir, id+".event.json")
	if _, err := os.Lstat(source); errors.Is(err, fs.ErrNotExist) {
		source = filepath.Join(r.Dir, id+".pending.json")
	}
	if _, err := os.Lstat(source); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := toolResultWrite(filepath.Join(r.Dir, id+".receipt.json"), receipt); err != nil {
		return err
	}
	if err := os.Rename(source, filepath.Join(r.Dir, id+".emitted.json")); err != nil {
		return err
	}
	return toolResultSync(r.Dir)
}

// Release disarms only the matching receiver; pending and emitted records stay
// (ToolResultBinding, Stop).
func (r ToolResult) Release() error {
	lock, err := r.lock(false)
	if err != nil {
		return err
	}
	defer lock.Unlock() // ignored: the binding write result remains returned
	b, ok, err := r.binding()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if b.Scope != r.Scope || b.Owner != r.Owner {
		return errors.New("tool-result release refuses another receiver's binding")
	}
	b.Until = r.now()
	return toolResultWrite(filepath.Join(r.Dir, "binding.json"), b)
}
