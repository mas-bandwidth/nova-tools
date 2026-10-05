package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// readFriendsTable reads the friends table: from world.friendsTable if set (testing),
// from --table <file> if given, else from the sprint server via "where --json". There is
// no other source: a read that fails is an error, never a guess, because the bus's member
// list carries no held, down or never-wake and would wake every friend on it.
func (w world) readFriendsTable(ctx context.Context, c *tool.Call) ([]friend.FriendsTableRow, error) {
	if w.friendsTable != nil {
		return w.friendsTable(ctx)
	}
	tablePath := c.Str("table")
	if tablePath != "" {
		data, err := os.ReadFile(tablePath)
		if err != nil {
			return nil, fmt.Errorf("reading friends table: %w", err)
		}
		return friend.ParseFriendsTable(data)
	}
	server := c.Str("server")
	if server == "" {
		server = w.server()
	}
	do := w.sprint
	if do == nil {
		do = func(ctx context.Context, server string, argv []string) ([]sprintwire.Result, error) {
			return sprintwire.Client{Addr: server}.Do(ctx, argv)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := do(ctx, server, []string{"where", "--json"})
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s: %w", server, err)
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("the sprint server at %s answered %d results for where --json, want 1", server, len(res))
	}
	if res[0].Code != 0 {
		return nil, fmt.Errorf("the sprint server at %s refused where --json: %s", server, strings.TrimSpace(res[0].Stderr))
	}
	return friend.ParseFriendsTable([]byte(res[0].Stdout))
}

// wakePingLoop is the coordinator's wake ping loop:
// nova-friend ping --wake --every <d> --to-friends
func (w world) wakePingLoop(c *tool.Call) *tool.Out {
	me := c.Str("as")
	every := c.Dur("every")
	bound := c.Dur("bound")
	if bound <= 0 {
		bound = friend.Window
	}
	dry := c.DryRun()

	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	table, err := w.readFriendsTable(ctx, c)
	if err != nil {
		return tool.Refuse("the friends table cannot be read: " + err.Error())
	}

	filterTargets := func(tbl []friend.FriendsTableRow) []string {
		var targets []string
		for _, row := range tbl {
			if row.Name == me || row.Skipped() {
				continue
			}
			targets = append(targets, row.Name)
		}
		slices.Sort(targets)
		return targets
	}

	targets := filterTargets(table)

	if dry {
		return tool.Done().Fact("friends", strings.Join(targets, ",")).
			Fact("every", every).
			Fact("bound", bound).
			Fact("wake", true)
	}

	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()

	if w.signals != nil {
		var stop context.CancelFunc
		ctx, stop = w.signals(ctx)
		defer stop()
	}

	deafSet := map[string]bool{}
	passNum := 0

	fmt.Fprintf(c.Stdout, "PING OK friends=%s every=%s bound=%s\n", strings.Join(targets, ","), every, bound)

	for ctx.Err() == nil {
		passNum++
		passStart := w.now()

		if passNum > 1 {
			tbl, err := w.readFriendsTable(ctx, c)
			if err != nil {
				// A pass that cannot read the table pings no one: the last pass's
				// targets may since have been held, downed or marked never-wake.
				fmt.Fprintf(c.Stdout, "PING SKIP pass=%d the friends table cannot be read: %s; remedy: bring the sprint server back or pass --table <file>\n", passNum, err)
				if every <= 0 {
					break
				}
				w.sleep(ctx, every)
				continue
			}
			targets = filterTargets(tbl)
		}

		if len(targets) == 0 {
			if every <= 0 {
				break
			}
			w.sleep(ctx, every)
			continue
		}

		expected := map[string]string{}
		for _, target := range targets {
			nonce := w.random()
			if nonce == "" {
				nonce = fmt.Sprintf("w%05d", passNum)
			}
			expected[target] = nonce
			body := friend.WakePingText(me, passStart, nonce)
			msg := bus.Message{
				From:    me,
				To:      []string{target},
				Subject: friend.PingPrefix + nonce,
				Body:    body + "\n",
			}
			_, _ = b.Send(ctx, msg) // ignored: a send that fails is a ping not answered, and the target is reported deaf
		}

		answered := map[string]bool{}
		deadline := passStart.Add(bound)
		for w.now().Before(deadline) && len(answered) < len(targets) && ctx.Err() == nil {
			es, err := b.Store.Range(ctx, bus.StreamOf(me), "-", "+", 0)
			if err == nil {
				for _, e := range es {
					m := e.Message()
					nonce, _, _, _, ok := friend.ParsePong(strings.TrimSpace(m.Body))
					if ok && expected[m.From] == nonce {
						answered[m.From] = true
					}
				}
			}
			if len(answered) == len(targets) {
				break
			}
			w.sleep(ctx, WaitPongEvery)
		}

		var currentDeaf []string
		for _, target := range targets {
			if !answered[target] {
				currentDeaf = append(currentDeaf, target)
			}
		}
		slices.Sort(currentDeaf)

		changed := false
		for _, d := range currentDeaf {
			if !deafSet[d] {
				changed = true
				break
			}
		}
		if !changed {
			for d := range deafSet {
				if answered[d] {
					changed = true
					break
				}
			}
		}

		deafSet = map[string]bool{}
		for _, d := range currentDeaf {
			deafSet[d] = true
		}

		if changed && len(currentDeaf) > 0 {
			deafList := strings.Join(currentDeaf, ", ")
			note := bus.Message{
				From:    me,
				To:      []string{me},
				Subject: "friend deaf: " + deafList,
				Body:    "deaf: " + deafList + "\n",
			}
			_, _ = b.Send(ctx, note) // ignored: notifying coordinator of deaf friends is best-effort
			fmt.Fprintf(c.Stdout, "PING DEAF friends=%s at=%s\n", deafList, w.now().UTC().Format(time.RFC3339))
		}

		if every <= 0 {
			break
		}

		elapsed := w.now().Sub(passStart)
		if elapsed < every {
			w.sleep(ctx, every-elapsed)
		}
	}

	if ctx.Err() != nil {
		fmt.Fprintln(c.Stdout, "PING STOP interrupted")
	}
	return tool.Exit(0)
}

func (w world) pingInstall(c *tool.Call) *tool.Out {
	goos := w.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	exe := "/opt/nova/bin/nova-friend"
	if w.binary != nil {
		if p, err := w.binary(); err == nil && p != "" {
			exe = p
		}
	} else if p, err := os.Executable(); err == nil {
		exe = p
	}
	dir := c.Str("dir")
	if dir == "" {
		home := w.home
		if goos == "linux" {
			if x := w.getenv("XDG_CONFIG_HOME"); x != "" {
				dir = filepath.Join(x, "systemd", "user")
			} else {
				dir = filepath.Join(home, ".config", "systemd", "user")
			}
		} else {
			dir = filepath.Join(home, "Library", "LaunchAgents")
		}
	}
	logf := c.Str("log")
	if logf == "" && goos == "darwin" {
		logf = filepath.Join(w.home, "Library", "Logs", "nova-friend-wake-ping.log")
	}
	u := friend.WakePingUnit{
		OS:     goos,
		Exe:    exe,
		As:     c.Str("as"),
		Every:  c.Dur("every"),
		Bound:  c.Dur("bound"),
		Redis:  c.Str("redis"),
		Server: c.Str("server"),
		Log:    logf,
	}
	if u.Redis == "" {
		u.Redis = w.redis()
	}
	if u.Server == "" {
		u.Server = w.server()
	}
	text, err := u.Text()
	if err != nil {
		return tool.Refuse(err.Error() + "; nothing was written")
	}
	path := filepath.Join(dir, friend.WakePingUnitFile(goos))
	if c.DryRun() {
		return tool.Done().Fact("unit", path).Fact("written", false).Fact("dry_run", true).Fact("text", tool.Text(text))
	}

	load := w.seatLoad
	if load == nil {
		load = loadWakePingUnit
	}
	in := friend.WakePingInstaller{
		Dir:    dir,
		Load:   func(p string) error { return load(goos, "load", p) },
		Unload: func(p string) error { return load(goos, "unload", p) },
	}
	res, err := in.Install(u)
	if err != nil {
		return tool.Fail(err.Error())
	}
	return tool.Done().Fact("unit", res.Path).Fact("written", res.Changed).Fact("loaded", true)
}

func (w world) pingUninstall(c *tool.Call) *tool.Out {
	goos := w.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	dir := c.Str("dir")
	if dir == "" {
		home := w.home
		if goos == "linux" {
			if x := w.getenv("XDG_CONFIG_HOME"); x != "" {
				dir = filepath.Join(x, "systemd", "user")
			} else {
				dir = filepath.Join(home, ".config", "systemd", "user")
			}
		} else {
			dir = filepath.Join(home, "Library", "LaunchAgents")
		}
	}
	file := friend.WakePingUnitFile(goos)
	if file == "" {
		return tool.Refuse("ping uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and " + goos + " has neither")
	}
	path := filepath.Join(dir, file)
	if c.DryRun() {
		_, serr := os.Stat(path)
		return tool.Done().Fact("unit", path).Fact("present", serr == nil).Fact("dry_run", true)
	}
	load := w.seatLoad
	if load == nil {
		load = loadWakePingUnit
	}
	in := friend.WakePingInstaller{
		Dir:    dir,
		Load:   func(p string) error { return load(goos, "load", p) },
		Unload: func(p string) error { return load(goos, "unload", p) },
	}
	res, err := in.Uninstall(goos)
	if err != nil {
		return tool.Fail(err.Error())
	}
	if !res.Changed {
		return tool.Done().Fact("unit", res.Path).Fact("removed", false)
	}
	return tool.Done().Fact("unit", res.Path).Fact("removed", true)
}

func loadWakePingUnit(goos, op, path string) error {
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		return errors.New("NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine")
	}
	ctx := context.Background()
	run := func(name string, args ...string) error {
		cmd, cancel := subproc.Command(ctx, subproc.Tool, name, args...)
		defer cancel()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if goos == "linux" {
		if op == "unload" {
			return run("systemctl", "--user", "disable", "--now", friend.WakePingService)
		}
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := run("systemctl", "--user", "enable", friend.WakePingService); err != nil {
			return err
		}
		return run("systemctl", "--user", "restart", friend.WakePingService)
	}
	service := "gui/" + strconv.Itoa(os.Getuid()) + "/" + friend.WakePingLabel
	loaded := run("launchctl", "print", service) == nil
	if loaded {
		if err := run("launchctl", "bootout", service); err != nil {
			return err
		}
	}
	if op == "unload" {
		return nil
	}
	return run("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), path)
}
