package notice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// OpenCode is the opencode shim (docs/SPEC-OPENCODE-HOOK.md,
// tla/OpenCodeHookNotice.tla). It speaks to the running opencode server only
// through opencode's own CLI, `opencode api METHOD PATH`, which carries the
// server's credentials itself: no password, key or token ever passes through
// this process.
//
// Into a running turn and waking an idle session are one call: the server's
// documented session.synthetic operation (POST /api/session/<id>/synthetic)
// with delivery "steer" and resume true. Steer admits the text at the running
// turn's next step boundary; resume schedules a turn when the session is idle,
// which is its wake. The text is Notice.Text: the saved file's path, never a
// subject or a body.
//
// Consumption is the shim's own turn boundary, read in three requests: the
// synthetic item has left the session inbox (it entered a turn), the session is
// not among the server's active drains, and the session's idle marker
// (Session.Info time.idle) is at or after the item's admission, both on the
// server's own clock.
type OpenCode struct {
	Session, Notices string
	// API is one request to the running server: CLIForOpenCode, or a test's
	// fake server.
	API OpenCodeAPI
	// Discover, when set, is the friend's current session (LatestOpenCodeSession over
	// a directory): the route follows the friend to a new session. It is nil
	// when the session is pinned.
	Discover func(ctx context.Context) (string, error)

	item    string // the inbox id of the last synthetic admitted
	created int64  // its admission time on the server clock, ms
}

// OpenCodeAPI is one request to the running opencode server. A status the
// server refused is an *OpenCodeStatus error.
type OpenCodeAPI func(ctx context.Context, method, path string, body []byte) ([]byte, error)

// OpenCodeStatus is a request the server answered with a non-2xx status: it
// was not admitted.
type OpenCodeStatus struct{ Code int }

func (s *OpenCodeStatus) Error() string {
	return "opencode server answered HTTP " + strconv.Itoa(s.Code)
}

// Name is the harness word.
func (o *OpenCode) Name() string { return "opencode" }

// Dir is the private notice directory.
func (o *OpenCode) Dir() string { return o.Notices }

// openCodeInbox is the part of Session.Inbox.Info the shim reads.
type openCodeInbox struct {
	ID   string `json:"id"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

// Push admits one synthetic message naming the saved file. A re-push of a
// notice whose item is still in the inbox is that item: it has not reached a
// turn yet, and the file it names holds the newest challenge. A refused
// request is a certain failure; one whose answer cannot be read may have been
// admitted, so its outcome is unknown.
func (o *OpenCode) Push(ctx context.Context, n Notice) (string, error) {
	if o.item != "" {
		pending, err := o.inbox(ctx)
		if err == nil && pending[o.item] {
			return o.item, nil
		}
	}
	body, err := json.Marshal(map[string]any{"text": n.Text(), "description": "nova-hook notice", "delivery": "steer", "resume": true})
	if err != nil {
		return "", err
	}
	raw, err := o.API(ctx, "POST", "/api/session/"+o.Session+"/synthetic", body)
	if err != nil {
		var status *OpenCodeStatus
		if errors.As(err, &status) || errors.Is(err, ErrOpenCodeNotRun) {
			return "", fmt.Errorf("opencode synthetic: %w", err)
		}
		return "", fmt.Errorf("%w: opencode synthetic: %v", codexdesktop.ErrOutcomeUnknown, err)
	}
	var got struct {
		Data openCodeInbox `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Data.ID == "" {
		return "", fmt.Errorf("%w: opencode synthetic answered no inbox item", codexdesktop.ErrOutcomeUnknown)
	}
	o.item, o.created = got.Data.ID, got.Data.Time.Created
	return got.Data.ID, nil
}

// Consumed is a finished turn since the push: the item left the inbox, the
// session is not running, and its idle marker is at or after the admission.
func (o *OpenCode) Consumed(ctx context.Context, _ Notice) (bool, error) {
	if o.item == "" {
		return false, nil
	}
	pending, err := o.inbox(ctx)
	if err != nil || pending[o.item] {
		return false, err
	}
	raw, err := o.API(ctx, "GET", "/api/session/active", nil)
	if err != nil {
		return false, err
	}
	var active struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &active); err != nil {
		return false, errors.New("opencode session.active answered no map")
	}
	if _, running := active.Data[o.Session]; running {
		return false, nil
	}
	raw, err = o.API(ctx, "GET", "/api/session/"+o.Session, nil)
	if err != nil {
		return false, err
	}
	var info struct {
		Data struct {
			Time struct {
				Idle *int64 `json:"idle"`
			} `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return false, errors.New("opencode session.get answered no session")
	}
	idle := info.Data.Time.Idle
	return idle != nil && *idle >= o.created, nil
}

// inbox is the ids of the session's undelivered inbox items.
func (o *OpenCode) inbox(ctx context.Context) (map[string]bool, error) {
	raw, err := o.API(ctx, "GET", "/api/session/"+o.Session+"/inbox", nil)
	if err != nil {
		return nil, err
	}
	var got struct {
		Data []openCodeInbox `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, errors.New("opencode session.inbox.list answered no list")
	}
	out := map[string]bool{}
	for _, it := range got.Data {
		out[it.ID] = true
	}
	return out, nil
}

// Follow moves the route to the friend's current session (notice rule 6).
func (o *OpenCode) Follow(ctx context.Context) (from, to string, moved bool, err error) {
	if o.Discover == nil {
		return o.Session, o.Session, false, nil
	}
	current, err := o.Discover(ctx)
	if err != nil {
		return o.Session, o.Session, false, err
	}
	if current == "" || current == o.Session {
		return o.Session, o.Session, false, nil
	}
	if !ValidOpenCodeSession(current) {
		return o.Session, o.Session, false, errors.New("the current opencode session's id is invalid")
	}
	from, o.Session, o.item, o.created = o.Session, current, "", 0
	return from, current, true, nil
}

var openCodeSessionID = regexp.MustCompile(`^ses_[A-Za-z0-9]{1,64}$`)

// ValidOpenCodeSession reports whether id has opencode's session id shape: it
// is put in a request path, so nothing else is accepted.
func ValidOpenCodeSession(id string) bool { return openCodeSessionID.MatchString(id) }

// ProbeOpenCodeSession checks that the session exists on the running server.
func ProbeOpenCodeSession(ctx context.Context, api OpenCodeAPI, session string) error {
	if !ValidOpenCodeSession(session) {
		return errors.New("opencode session wants an id of the form ses_<letters and digits>")
	}
	raw, err := api(ctx, "GET", "/api/session/"+session, nil)
	if err != nil {
		return err
	}
	var info struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &info); err != nil || info.Data.ID != session {
		return errors.New("opencode session.get did not answer the session")
	}
	return nil
}

// LatestOpenCodeSession is the newest root session in directory that is not
// archived, by its update time on the server clock, or "".
func LatestOpenCodeSession(ctx context.Context, api OpenCodeAPI, directory string) (string, error) {
	q := url.Values{"directory": {directory}, "order": {"desc"}, "parentID": {"null"}, "limit": {"20"}}
	raw, err := api(ctx, "GET", "/api/session?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var got struct {
		Data []struct {
			ID       string `json:"id"`
			ParentID string `json:"parentID"`
			Time     struct {
				Updated  int64  `json:"updated"`
				Archived *int64 `json:"archived"`
			} `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", errors.New("opencode session.list answered no list")
	}
	best, at := "", int64(-1)
	for _, s := range got.Data {
		if s.ParentID != "" || s.Time.Archived != nil || !ValidOpenCodeSession(s.ID) {
			continue
		}
		if s.Time.Updated > at {
			best, at = s.ID, s.Time.Updated
		}
	}
	return best, nil
}

// openCodeHTTP is the status line `opencode api` writes to stderr.
var openCodeHTTP = regexp.MustCompile(`HTTP (\d{3})`)

// CLIForOpenCode is OpenCodeAPI through the installed CLI: `opencode api METHOD
// PATH [-d BODY]` against the background service. The CLI reads the service's
// credentials itself. Its stderr is never returned whole: only the HTTP status,
// or a fixed failure text, so nothing the CLI prints reaches a log.
func CLIForOpenCode(bin string) OpenCodeAPI {
	return func(ctx context.Context, method, path string, body []byte) ([]byte, error) {
		args := []string{"api", method, path}
		if body != nil {
			args = append(args, "-d", string(body))
		}
		cmd, cancel := subproc.Command(ctx, subproc.Tool, bin, args...)
		defer cancel()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil {
			return stdout.Bytes(), nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, ErrOpenCodeNotRun
		}
		if m := openCodeHTTP.FindStringSubmatch(stderr.String()); m != nil {
			code, _ := strconv.Atoi(m[1]) // ignored: three digits always parse
			return nil, &OpenCodeStatus{Code: code}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("opencode api " + strings.ToLower(method) + " failed: exit " + strconv.Itoa(exit.ExitCode()))
	}
}

// ErrOpenCodeNotRun is a CLI that could not be started: nothing reached the
// server.
var ErrOpenCodeNotRun = errors.New("the opencode CLI could not be run")
