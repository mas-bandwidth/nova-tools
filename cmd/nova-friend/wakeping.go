package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// coordinatorFriends is the friends table and the seat's holder as the sprint
// server's coordinator view says them (GET /api/view/coordinator?all=1: each row
// k=f:<friend> with its st, up, down or held, and seat).
func coordinatorFriends(ctx context.Context, server string) ([]friend.WakeRow, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server+"/api/view/coordinator?all=1", nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("the sprint server at %s did not answer: %w", server, err)
	}
	defer resp.Body.Close() // ignored: a read-only body
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxView+1))
	switch {
	case err != nil:
		return nil, "", fmt.Errorf("the sprint server at %s: its view was cut: %w", server, err)
	case len(raw) > maxView:
		return nil, "", fmt.Errorf("the sprint server at %s: its view is over %d bytes", server, maxView)
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("the sprint server at %s refused the view (%s): %s", server, resp.Status, oneline.Cap(strings.TrimSpace(string(raw)), 300))
	}
	var v struct {
		Seat string `json:"seat"`
		Rows []struct {
			K  string `json:"k"`
			St string `json:"st"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, "", fmt.Errorf("the sprint server at %s: its view is not JSON: %w", server, err)
	}
	var rows []friend.WakeRow
	for _, r := range v.Rows {
		if name, ok := strings.CutPrefix(r.K, "f:"); ok {
			rows = append(rows, friend.WakeRow{Name: name, Status: r.St})
		}
	}
	return rows, v.Seat, nil
}

// wakeFlagProblems is ping's flag rule: --to or --to-friends, and the wake loop's
// flags only with --to-friends, which is a wake check.
func wakeFlagProblems(c *tool.Call) {
	toFriends := c.Bool("to-friends")
	switch {
	case !toFriends && strings.TrimSpace(c.Str("to")) == "":
		c.Problem("--to is required; it wants the friend to ping, or --to-friends every friend the friends table holds up; refusing to guess")
	case toFriends && c.Str("to") != "":
		c.Problem("--to and --to-friends name the same thing twice; give one")
	case toFriends && !c.Bool("wake"):
		c.Problem("--to-friends is the wake loop and wants --wake; a ping only the daemon answers has no value, and nova-friend serve is the connection's loop")
	}
	if !toFriends {
		for _, n := range []string{"every", "within", "never-wake"} {
			if c.Given(n) {
				c.Problem("--" + n + " is the wake loop's and wants --to-friends")
			}
		}
	}
	if c.Given("every") && c.Dur("every") <= 0 {
		c.Problem("--every wants a positive duration")
	}
	if c.Given("within") && c.Dur("within") <= 0 {
		c.Problem("--within wants a positive duration")
	}
}

// wakeFriends is ping --wake --to-friends (docs/SPEC-FRIEND.md, "The wake ping
// loop"): each pass reads the friends table, wake-pings the friends it holds up,
// waits within for each session's pong and tells the coordinator once per change
// of who is deaf. Without --every it is one pass.
func (w world) wakeFriends(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	me, server, every, within := c.Str("as"), c.Str("server"), c.Dur("every"), c.Dur("within")
	never := commaList(c.Str("never-wake"))
	say := func(line string) { fmt.Fprintln(c.Stdout, "WAKE "+line) }
	rows, seat, err := w.friends(context.Background(), server)
	if err != nil {
		return tool.Refuse("the friends table cannot be read: " + err.Error())
	}
	targets := friend.WakeTargets(me, rows, never)
	if c.DryRun() {
		return tool.Done().Fact("friends", strings.Join(targets, ",")).Fact("every", every).Fact("within", within)
	}
	ctx, stop := w.signals(context.Background())
	defer stop()
	say(fmt.Sprintf("OK every=%s within=%s never_wake=%s", every, within, strings.Join(never, ",")))
	var changes friend.DeafChange
	for pass := 1; ctx.Err() == nil; pass++ {
		began := w.now()
		if pass > 1 { // the table is read again each pass: a friend held or down since is skipped
			if rows, seat, err = w.friends(ctx, server); err != nil {
				say("NOTE " + began.UTC().Format(time.RFC3339) + " the friends table cannot be read: " + oneline.Escape(err.Error()) + "; trying again next pass")
				w.sleep(ctx, every)
				continue
			}
			targets = friend.WakeTargets(me, rows, never)
		}
		pinged, deaf, err := w.wakePass(ctx, b, me, targets, within)
		if err != nil {
			say("NOTE " + began.UTC().Format(time.RFC3339) + " " + oneline.Escape(err.Error()) + "; trying again next pass")
		} else {
			say(fmt.Sprintf("PASS n=%d pinged=%d answered=%d deaf=%s at=%s", pass, pinged, pinged-len(deaf), dash(strings.Join(deaf, ",")), began.UTC().Format(time.RFC3339)))
			if names := changes.Report(deaf); len(names) > 0 {
				to := seat
				if to == "" {
					to = me
				}
				joined := strings.Join(names, ",")
				_, serr := b.Send(ctx, bus.Message{From: me, To: []string{to}, Kind: bus.KindBlocker, Subject: "wake: deaf: " + joined,
					Body: "wake: deaf: " + joined + "\nEach was sent a wake ping and its session did not answer within " + within.String() + " (its daemon may have). Look at the session: nova-friend check --as " + me + " " + names[0] + "\n"})
				if serr != nil {
					say("NOTE " + oneline.Escape("the deaf note to "+to+" was not sent: "+serr.Error()))
				}
				say("DEAF friends=" + joined + " at=" + began.UTC().Format(time.RFC3339))
			}
		}
		if every <= 0 {
			break
		}
		w.sleep(ctx, max(0, every-w.now().Sub(began)))
	}
	say("STOP")
	return tool.Exit(0)
}

// wakePass sends a wake ping, a fresh nonce each, to every target and waits up to
// within for the session's pong of each (a daemon-pong never answers one); it
// answers how many were pinged and the sorted names whose session did not answer.
func (w world) wakePass(ctx context.Context, b *bus.Bus, me string, targets []string, within time.Duration) (int, []string, error) {
	if len(targets) == 0 {
		return 0, nil, nil
	}
	_, storeNow, err := b.Store.Roster(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("the store did not answer: %w", err)
	}
	floor := bus.IDAt(storeNow)
	start := w.now()
	waiting := map[string]string{} // friend -> nonce
	for _, to := range targets {
		nonce := w.random()
		ping := bus.Message{From: me, To: []string{to}, Subject: friend.PingPrefix + nonce, Body: friend.WakePingText(me, start, nonce) + "\n"}
		if _, err := b.Send(ctx, ping); err != nil {
			return 0, nil, fmt.Errorf("the wake ping to %s was not sent: %w", to, err)
		}
		waiting[to] = nonce
	}
	for len(waiting) > 0 && ctx.Err() == nil {
		w.sleep(ctx, WaitPongEvery)
		got, err := b.Log(ctx, floor)
		if err != nil {
			return len(targets), nil, fmt.Errorf("the store did not answer: %w", err)
		}
		for _, e := range got {
			m := e.Message()
			if nonce, ok := waiting[m.From]; ok { // the friend's own stream, never the body
				if n, _, _, _, isPong := friend.ParsePong(strings.TrimSpace(m.Body)); isPong && n == nonce && m.Subject != friend.DaemonPongSubject {
					delete(waiting, m.From)
				}
			}
		}
		if w.now().Sub(start) >= within {
			break
		}
	}
	if ctx.Err() != nil { // interrupted: no one is called deaf for a wait that was cut
		return len(targets), nil, nil
	}
	deaf := make([]string, 0, len(waiting))
	for n := range waiting {
		deaf = append(deaf, n)
	}
	slices.Sort(deaf)
	return len(targets), deaf, nil
}

// wakeAgent is the wake loop's launchd agent, from ping-install's flags.
func (w world) wakeAgent(c *tool.Call) (friend.Agent, error) {
	bin, err := w.binary()
	if err != nil {
		return friend.Agent{}, fmt.Errorf("this binary's path: %v", err)
	}
	as := c.Str("as")
	log := c.Str("launchd-log")
	if log == "" {
		log = filepath.Join(w.home, "Library", "Logs", "nova-friend-wake-ping-"+as+".log")
	}
	cmd := []string{"ping", "--as", as, "--wake", "--to-friends", "--every", c.Dur("every").String(), "--within", c.Dur("within").String(), "--server", c.Str("server"), "--redis", c.Str("redis")}
	if n := c.Str("never-wake"); n != "" {
		cmd = append(cmd, "--never-wake", n)
	}
	return friend.Agent{Friend: "wake-ping-" + as, Binary: bin, Copy: w.copy, Home: w.home, Path: w.getenv("PATH"), LaunchdLog: log, Command: cmd}, nil
}

func (w world) pingInstall(c *tool.Call) *tool.Out {
	dry := c.DryRun()
	c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+"), written into the agent")
	if c.Dur("every") <= 0 {
		c.Problem("--every wants a positive duration")
	}
	if o := c.Refused(); o != nil {
		return o
	}
	a, err := w.wakeAgent(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	if dry {
		placed, copy, err := friend.PlanBinary(a.Binary, a.Home)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		a.Binary = placed
		o := tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Fact("launchd_log", a.LaunchdLog)
		if copy {
			o.Item("plan", "command", tool.Text("copy "+a.Binary+" "+placed))
		}
		return o.Item("plan", "command", tool.Text("write "+a.PlistPath())).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).
			Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootstrap gui/%d %s", w.uid, a.PlistPath()))).
			Note("the agent runs: " + strings.Join(a.Args(), " "))
	}
	a.Sleep = func(d time.Duration) { w.sleep(context.Background(), d) }
	path, ran, err := friend.Install(context.Background(), a, w.uid, w.launchctl, func(p string, data []byte) error {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}, func() { w.sleep(context.Background(), time.Second) })
	if err != nil {
		return tool.Refuse(err.Error())
	}
	o := tool.Done().Fact("label", a.Label()).Fact("plist", path).Fact("launchd_log", a.LaunchdLog)
	for _, r := range ran {
		o.Item("ran", "command", tool.Text(r))
	}
	return o
}

func (w world) pingUninstall(c *tool.Call) *tool.Out {
	a := friend.Agent{Friend: "wake-ping-" + c.Str("as"), Home: w.home}
	if c.DryRun() {
		return tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath()).Item("plan", "command", tool.Text(fmt.Sprintf("launchctl bootout gui/%d/%s", w.uid, a.Label()))).Item("plan", "command", tool.Text("rm "+a.PlistPath()))
	}
	ran, err := friend.Uninstall(context.Background(), a, w.uid, w.launchctl, os.Remove)
	o := tool.Done().Fact("label", a.Label()).Fact("plist", a.PlistPath())
	for _, r := range ran {
		o.Item("ran", "command", tool.Text(r))
	}
	if err != nil {
		return tool.Fail(err.Error())
	}
	return o
}
