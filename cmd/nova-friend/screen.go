package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func (w world) screen(c *tool.Call) *tool.Out {
	name := w.screenFriend
	if name == "" {
		return tool.Refuse("friend is required: nova-friend screen <friend>")
	}

	// The recorded --dir identifies the friend's window even when her status
	// carries no live session (internal/friend/state.go: SessionLive is the
	// mailbox's conversation, not the directory). It is read whether or not
	// --state-dir names the state directory, so an explicit --state-dir does
	// not lose the directory the window is found by.
	dir := ""
	plistPath := filepath.Join(w.home, "Library", "LaunchAgents", "com.nova.friend-"+name+".plist")
	if b, err := os.ReadFile(plistPath); err == nil {
		args := friend.PlistArgs(string(b))
		dir = argAfter(args, "--dir")
	}

	state := c.Str("state-dir")
	if state == "" {
		state = friend.FindStateDir(w.home, dir, name)
	} else if _, err := os.Stat(filepath.Join(state, name)); err == nil {
		state = filepath.Join(state, name)
	}

	bin, _ := w.binary()

	res, err := friend.Screen(c.Ctx, friend.ScreenOpts{
		Friend:       name,
		Lines:        c.Int("lines"),
		StateDir:     state,
		Dir:          dir,
		Home:         w.home,
		Binary:       bin,
		Now:          w.now,
		Exec:         w.exec,
		WindowReader: w.windowReader,
	})
	if err != nil {
		var sr friend.ScreenRefused
		if errors.As(err, &sr) {
			return &tool.Out{
				Status: tool.Refused,
				Exit:   1,
				Why:    []string{sr.Why},
				Remedy: sr.Remedy,
			}
		}
		return tool.Refuse(err.Error())
	}

	if c.Bool("json") {
		raw, err := json.Marshal(res)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		fmt.Fprintln(c.Stdout, string(raw))
		return tool.Exit(0)
	}

	fmt.Fprint(c.Stdout, res.FormatText())
	return tool.Exit(0)
}
