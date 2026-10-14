package notice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/redis/go-redis/v9"
)

// callBound bounds one store call the Core makes.
const callBound = 5 * time.Second

// Redis is the Store over the hook record and the bus store a hook verb opened.
type Redis struct {
	Hooks hook.Store
	Bus   bus.Store // nil reads no group: the group never moves
}

func bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, callBound)
}

// Active reads the challenge hash and epoch in one call.
func (r Redis) Active(ctx context.Context, name string) (string, int64, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	got, err := r.Hooks.C.HMGet(ctx, hook.ChallengeKey(name), "sum", "epoch").Result()
	if err != nil {
		return "", 0, err
	}
	sum, _ := got[0].(string)
	raw, _ := got[1].(string)
	if sum == "" {
		return "", 0, nil
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", 0, errors.New("active hook challenge has a malformed epoch")
	}
	return sum, epoch, nil
}

// Answered reads every id's answer field in one call.
func (r Redis) Answered(ctx context.Context, name string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := bound(ctx)
	defer cancel()
	values, err := r.Hooks.C.HMGet(ctx, bus.AnswersOf(name), ids...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range values {
		if v == nil {
			continue
		}
		raw, _ := v.(string)
		if stamp, err := strconv.ParseInt(raw, 10, 64); err != nil || stamp <= 0 {
			return nil, errors.New("bus answer wants a valid store timestamp")
		}
		out[ids[i]] = true
	}
	return out, nil
}

// Activity reads the model's acts in one pipeline and the group's cursor.
func (r Redis) Activity(ctx context.Context, name string) (Activity, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	var at, proof *redis.StringCmd
	var answers, owed *redis.IntCmd
	_, err := r.Hooks.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		at = p.HGet(ctx, hook.RecordKey(name), "answered_at")
		proof = p.Get(ctx, hook.ProofKey(name, hook.Bus))
		answers = p.HLen(ctx, bus.AnswersOf(name))
		owed = p.HLen(ctx, bus.OwedOf(name))
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return Activity{}, err
	}
	a := Activity{AnsweredAt: at.Val(), Proof: proof.Val(), Answers: answers.Val(), Owed: owed.Val()}
	if r.Bus != nil {
		if a.Group, _, err = r.Bus.Group(ctx, bus.StreamOf(name), name); err != nil {
			return Activity{}, err
		}
	}
	return a, nil
}

// Now is the store's TIME.
func (r Redis) Now(ctx context.Context) (time.Time, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	return r.Hooks.C.Time(ctx).Result()
}

// Unprove drops the bus push's proof for epoch.
func (r Redis) Unprove(ctx context.Context, name string, role hook.Role, epoch int64) error {
	return r.Hooks.Unprove(ctx, name, role, hook.Bus, epoch)
}

// Disk is the Files a running hook writes: a private directory, a bounded
// file, created without replacing and rewritten in place by rename.
type Disk struct{}

// Save writes one saved notice.
func (Disk) Save(path string, data []byte, replace bool) error {
	if len(data) > MaxSaved {
		return fmt.Errorf("saved notice wants at most %d bytes", MaxSaved)
	}
	if err := PrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if replace {
		return atomicfile.Write(path, data, 0o600)
	}
	return atomicfile.Write(path, data, 0o600, atomicfile.NoReplace())
}

// PrivateDir holds a notice directory to the grammar a notice may name, to
// current-user ownership at mode 0700, and to a canonical path.
func PrivateDir(dir string) error {
	if err := codexdesktop.ValidateDirectory(dir); err != nil {
		return err
	}
	if err := codexdesktop.ValidatePrivateDirectory(dir); err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(dir); err != nil || real != dir {
		return errors.New("event directory wants a canonical path without symlink ancestors")
	}
	return nil
}

// MakeDir creates a shim's private notice directory at mode 0700 and answers
// its canonical path, the one a notice names.
func MakeDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return real, PrivateDir(real)
}
