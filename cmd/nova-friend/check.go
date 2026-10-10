package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var callArgs sync.Map // *tool.Call -> []string

// check handles both health check and delivery conformance check.
func (w world) check(c *tool.Call) *tool.Out {
	if c.Bool("settings") {
		return w.settingsCheck(c)
	}
	if c.Str("harness") != "" {
		return w.deliveryCheckVerb(c)
	}
	return w.healthCheckVerb(c)
}

// deliveryCheckVerb is the delivery check against the live session (friend.Conformance).
func (w world) deliveryCheckVerb(c *tool.Call) *tool.Out {
	name, harness, dir := c.Str("as"), c.Str("harness"), c.Str("dir")
	state := w.stateDir(c, dir)
	if c.DryRun() {
		if _, err := friend.SelectDeliverer(name, harness, dir, c.Str("session"), c.Str("adapter"), c.Str("delivery-dir"), w.exec, nil); err != nil {
			return tool.Refuse(err.Error())
		}
		nonce := w.random()
		return tool.Done().Fact("harness", harness).Fact("dir", dir).Fact("within", c.Dur("within").String()).
			Item("plan", "command", tool.Text(w.pongCommand(name, nonce, state, c.Str("redis"), dir)+" --to "+oneline.ShellWord(w.checkTo(c.Str("to"), name, state)))).
			Note("nothing was delivered; the session would run the plan line and its pong would end the check")
	}
	c.Want("redis", "the bus store's Redis address, host:port (or "+RedisEnv+"), where the pong is read")
	if o := c.Refused(); o != nil {
		return o
	}
	res, remedy, _, refusal := w.deliveryCheck(c, name, harness, dir, c.Str("session"), state, c.Str("to"), c.Dur("within"))
	if refusal != "" {
		return tool.Refuse(refusal)
	}
	if res.Stage != "" {
		o := tool.Fail().As("FAIL").Fact("harness", harness).Fact("stage", res.Stage).Fact("why", tool.Text(res.Why))
		if remedy != "" {
			o.Note("remedy: " + remedy)
		}
		return o
	}
	return tool.Done().Fact("harness", harness).Fact("took", res.Took.String())
}

// settingsCheck compares the harness's settings with what install would
// write (docs/SPEC-FRIEND.md, Harness settings); it writes nothing.
func (w world) settingsCheck(c *tool.Call) *tool.Out {
	all, err := w.harnessSettings(c).Check()
	if err != nil {
		return tool.Refuse(err.Error())
	}
	drift := friend.Drift(all)
	o := tool.Done()
	if len(drift) > 0 {
		o = tool.Fail().As("DRIFT")
	}
	o.Fact("harness", c.Str("harness")).Fact("settings", len(all)).Fact("drift", len(drift))
	for _, s := range drift {
		o.Item("drift", "harness", s.Harness, "file", s.File, "name", s.Name, "want", tool.Text(s.Want), "have", tool.Text(s.Have))
	}
	if len(drift) > 0 {
		o.Note("install again writes them: nova-friend install --as " + c.Str("as") + " --harness " + c.Str("harness") + " --dir " + c.Str("dir"))
	}
	return o
}

// healthCheckVerb runs the coordinator friend health check.
func (w world) healthCheckVerb(c *tool.Call) *tool.Out {
	// Retrieve positional arguments
	var friends []string
	if v, ok := callArgs.LoadAndDelete(c); ok {
		friends = v.([]string)
	}

	// Read --shown if given
	var shownMap map[string]friend.ShownEntry
	if shownArg := c.Str("shown"); shownArg != "" {
		var data []byte
		var err error
		if shownArg == "-" {
			data, err = io.ReadAll(c.Stdin)
		} else {
			data, err = os.ReadFile(shownArg)
		}
		if err != nil {
			return tool.Refuse(fmt.Sprintf("--shown %q cannot be read: %v", shownArg, err))
		}
		shownMap, err = friend.ParseShown(data)
		if err != nil {
			return tool.Refuse(fmt.Sprintf("--shown %q is invalid JSON: %v", shownArg, err))
		}
	}

	// Open store if configured
	var st bus.Store
	if c.Str("redis") != "" {
		b, closeStore, o := w.bus(c)
		if o != nil {
			return o
		}
		defer closeStore()
		st = b.Store
	}

	seams := w.checkSeams(c, st)

	if len(friends) == 0 {
		if seams.ListFriends != nil {
			list, err := seams.ListFriends()
			if err != nil {
				return tool.Refuse("cannot list friends: " + err.Error())
			}
			friends = list
		}
	}

	since := c.Dur("since")
	if since <= 0 {
		since = 24 * time.Hour
	}

	var checks []friend.FriendCheck
	for _, f := range friends {
		shown := friend.LookupShown(shownMap, f)
		fc := friend.CheckFriend(c.Ctx, f, seams, since, shown)
		checks = append(checks, fc)
	}

	summary := friend.ComputeSummary(checks)
	report := friend.CheckReport{
		Friends: checks,
		Summary: summary,
	}

	if c.Bool("json") {
		b, err := json.Marshal(report)
		if err != nil {
			return tool.Refuse("cannot marshal check report JSON: " + err.Error())
		}
		fmt.Fprintln(c.Stdout, string(b))
	} else {
		for _, line := range report.Lines() {
			fmt.Fprintln(c.Stdout, line)
		}
	}

	if summary.Broken+summary.Deaf+summary.Silent+summary.Down+summary.Untrue > 0 {
		return tool.Exit(1)
	}
	return tool.Exit(0)
}

func (w world) checkSeams(c *tool.Call, st bus.Store) friend.CheckSeams {
	friendStateDir := func(name string) string {
		if s := c.Str("state-dir"); s != "" {
			if _, err := os.Stat(filepath.Join(s, name)); err == nil {
				return filepath.Join(s, name)
			}
			return s
		}
		// her plist's --state-dir, else under her --dir (this verb's, else her plist's) when
		// her daemon writes there, else the home directory's
		plist := friend.PlistArgs(w.readPlist(friend.Agent{Friend: name, Home: w.home}.PlistPath()))
		if s := argAfter(plist, "--state-dir"); s != "" {
			return s
		}
		dir := c.Str("dir")
		if dir == "" {
			dir = argAfter(plist, "--dir")
		}
		return friend.FindStateDir(w.home, dir, name)
	}

	return friend.CheckSeams{
		Now:       w.now,
		Home:      w.home,
		Launchctl: w.launchctl,
		ReadStatus: func(name string) (friend.Status, bool, error) {
			return friend.ReadStatus(friendStateDir(name))
		},
		ReadPresence: func(name string) (friend.PresenceStatus, bool, error) {
			return friend.ReadPresence(friendStateDir(name))
		},
		ReadPong: func(name string) (friend.Pong, bool, error) {
			return friend.ReadPong(friendStateDir(name))
		},
		ReadLog: func(name string) ([]string, error) {
			b, err := os.ReadFile(friend.LogPath(friendStateDir(name)))
			if err != nil {
				return nil, err
			}
			return strings.Split(string(b), "\n"), nil
		},
		BusLog: func(ctx context.Context, name string, since time.Time) (int, time.Time, error) {
			return friend.BusLogEntries(ctx, st, name, since)
		},
		ReadWork: func(_ string, dir string) (int, int, string, time.Time, error) {
			return friend.DefaultReadWork(dir)
		},
		ListFriends: func() ([]string, error) {
			if st != nil {
				fr, _, _, err := st.Members(c.Ctx)
				if err == nil && len(fr) > 0 {
					return fr, nil
				}
			}
			base := filepath.Join(w.home, ".nova-friend")
			entries, err := os.ReadDir(base)
			if err != nil {
				return nil, nil
			}
			var list []string
			for _, e := range entries {
				if e.IsDir() {
					list = append(list, e.Name())
				}
			}
			slices.Sort(list)
			return list, nil
		},
		HarnessDir: func(name string) (string, string, error) {
			harness := "unknown"
			dir := ""
			st, found, _ := friend.ReadStatus(friendStateDir(name))
			if found {
				if st.Harness != "" {
					harness = st.Harness
				}
			}
			if d := c.Str("dir"); d != "" {
				dir = d
			}
			if dir == "" {
				// Check launchd plist if available
				plistPath := filepath.Join(w.home, "Library", "LaunchAgents", "com.nova.friend-"+name+".plist")
				if b, err := os.ReadFile(plistPath); err == nil {
					content := string(b)
					if idx := strings.Index(content, "<string>--dir</string>"); idx != -1 {
						rest := content[idx:]
						if start := strings.Index(rest, "<string>"); start != -1 {
							rest = rest[start+8:]
							if end := strings.Index(rest, "</string>"); end != -1 {
								dir = rest[:end]
							}
						}
					}
					if harness == "unknown" {
						if idx := strings.Index(content, "<string>--harness</string>"); idx != -1 {
							rest := content[idx:]
							if start := strings.Index(rest, "<string>"); start != -1 {
								rest = rest[start+8:]
								if end := strings.Index(rest, "</string>"); end != -1 {
									harness = rest[:end]
								}
							}
						}
					}
				}
			}
			return harness, dir, nil
		},
	}
}

// argAfter is the value after flag in args, empty when it is not there.
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
