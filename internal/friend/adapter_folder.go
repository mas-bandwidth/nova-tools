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
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// Folder is an explicit delivery route for a real Codex session whose watcher
// reads Dir. Writing a file is acceptance by the folder, never a session pong.
// The native proof still requires the session's own nonce-bearing bus reply.
type Folder struct {
	Dir, Session, Friend, WorkDir string
}

var folderNonce = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// CheckFolderRoute keeps the harness and session identity independent of the
// delivery route. The target must already be watched; the daemon never makes it.
func CheckFolderRoute(harness, session, adapter, deliveryDir string) error {
	if adapter == "" && deliveryDir == "" {
		return nil
	}
	if adapter != "folder" || harness != "codex" || session == "" || deliveryDir == "" {
		return fmt.Errorf("--adapter folder wants --harness codex, --session <real session>, and --delivery-dir <existing watched folder>")
	}
	if !filepath.IsAbs(deliveryDir) {
		return fmt.Errorf("--delivery-dir %q wants an absolute path", deliveryDir)
	}
	fi, err := os.Stat(deliveryDir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("--delivery-dir %q is not an existing directory watched by the session", deliveryDir)
	}
	return nil
}

// SelectDeliverer chooses the explicit folder route, or the harness's
// existing adapter. Default Codex queue and exec-resume behavior is unchanged.
func SelectDeliverer(friendName, harness, dir, session, adapter, deliveryDir string, run Exec, out io.Writer) (Deliverer, error) {
	if err := CheckFolderRoute(harness, session, adapter, deliveryDir); err != nil {
		return nil, err
	}
	if adapter == "folder" {
		return &Folder{Dir: deliveryDir, Session: session, Friend: friendName, WorkDir: dir}, nil
	}
	return NewDeliverer(harness, dir, session, run, out)
}

func (f *Folder) Deliver(ctx context.Context, text string) (exit int, retErr error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	fi, err := os.Stat(f.Dir)
	if err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("the folder adapter target %q is not a directory", f.Dir)
	}
	lock, err := filelock.Lock(filepath.Join(f.Dir, ".friend-push.lock"), "nova-friend folder delivery", 5*time.Second)
	if err != nil {
		return 0, fmt.Errorf("the folder adapter cannot reserve %q: %w", f.Dir, err)
	}
	publishedPath := ""
	defer func() {
		retErr = folderReleaseResult(retErr, lock.Unlock(), publishedPath, f.Dir, func(path string, err error) {
			slog.Error("folder delivery was written but its lock did not release cleanly; delivery stays acknowledged; inspect lock health", "path", path, "error", err)
		})
	}()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	name := "FRIEND-PUSH-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	name += "-" + hex.EncodeToString(random[:]) + ".md"
	if kind, nonce, only := PongRequest(text); only {
		if !folderNonce.MatchString(nonce) {
			return 0, fmt.Errorf("the folder adapter refused an invalid pong nonce %q", nonce)
		}
		switch kind {
		case PongCheck:
			name = "FRIEND-CHECK-" + nonce + ".md"
		case PongWake:
			name = "FRIEND-WAKE-" + nonce + ".md"
		}
	}
	kind, nonce, only := PongRequest(text)
	if !only {
		kind, nonce = "turn", ""
	} else if kind == PongCheck {
		kind = "check"
	}
	meta := struct {
		Friend      string `json:"friend"`
		Harness     string `json:"harness"`
		Session     string `json:"session"`
		WorkDir     string `json:"work_dir"`
		DeliveryDir string `json:"delivery_dir"`
		Kind        string `json:"kind"`
		Nonce       string `json:"nonce,omitempty"`
		SHA256      string `json:"sha256"`
	}{f.Friend, "codex", f.Session, f.WorkDir, f.Dir, kind, nonce, fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}
	data, err := json.Marshal(meta)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(f.Dir, name)
	if prior, err := os.ReadFile(path); err == nil {
		if string(prior) != text {
			return 0, fmt.Errorf("the folder adapter refused to replace unacknowledged %s with different text", name)
		}
		priorMeta, err := os.ReadFile(path + ".meta.json")
		if err != nil || string(priorMeta) != string(data)+"\n" {
			return 0, fmt.Errorf("the folder adapter refused to reuse %s without matching session metadata", name)
		}
		publishedPath = path
		TurnAccepted(ctx) // published: the session has the turn
		return 0, nil     // one unacknowledged request of this kind, nonce and session
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if err := folderAtomic(f.Dir, name+".meta.json", string(data)+"\n"); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := folderAtomic(f.Dir, name, text); err != nil {
		return 0, err
	}
	publishedPath = path
	TurnAccepted(ctx) // published: the session has the turn
	return 0, nil
}

// A published file has already delivered the turn. A lock cleanup fault must
// be reported, but cannot ask the daemon to retry a random-named turn.
func folderReleaseResult(deliveryErr, unlockErr error, publishedPath, dir string, report func(string, error)) error {
	if unlockErr == nil {
		return deliveryErr
	}
	if publishedPath != "" {
		report(publishedPath, unlockErr)
		return deliveryErr
	}
	return errors.Join(deliveryErr, fmt.Errorf("the folder adapter cannot release %q: %w", dir, unlockErr))
}

func folderAtomic(dir, name, text string) (retErr error) {
	tmp, err := os.CreateTemp(dir, ".friend-push-*")
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			if err := os.Remove(tmp.Name()); err != nil && !os.IsNotExist(err) {
				retErr = errors.Join(retErr, fmt.Errorf("remove the unfinished folder delivery %q: %w", tmp.Name(), err))
			}
		}
	}()
	if _, err = tmp.WriteString(text); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
