package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A friend's working directory is her nova-config row's dir (config.FriendDir; the
// owner, 2026-10-05: "All friends should be updated to point to their real
// directories. I'd like the symlinks to go away"). friend sync carries it onto the
// friends table (store.FriendSpec.Dir), and sync, reconcile (the verb and the run
// loop's tick), clean and the seat's inbox read it there or from the row. A row with
// no dir is <root>/<name>-working, as every friend was before the field, and the
// first time a run of the tool falls back for a friend it says so once, a NOTE line.

// friendDirNoted is the friends each app has said the fallback for, so the note is
// said once a run (a loop's passes and the server's ticks included).
var friendDirNoted sync.Map // friendDirKey -> true

type friendDirKey struct {
	a      *app
	friend string
}

// friendDir is the friend's working directory: dir, her row's, when it is set; else
// <root>/<friend>-working, with the note said on note the first time this app falls
// back for her. A nil note says nothing.
func (a *app) friendDir(friend, dir, root string, note io.Writer) string {
	if dir != "" {
		return dir
	}
	fallback := filepath.Join(root, friend+"-working")
	if _, said := friendDirNoted.LoadOrStore(friendDirKey{a, friend}, true); !said && note != nil {
		fmt.Fprintf(note, "NOTE friend=%s has no dir on her nova-config row, so her working directory is %s; run: nova-config friend set %s --dir <her real working directory>\n", friend, oneline.Field(fallback), friend)
	}
	return fallback
}

// friendRowDirs is each friend's dir as nova-config's friend rows say it, by name (a
// row with none is absent), or nil when the rows cannot be read: the seat's inbox
// reads it, where no friends table is at hand.
func (a *app) friendRowDirs(ctx context.Context) map[string]string {
	if a.friends == nil {
		return nil
	}
	rows, err := a.friends(ctx, "")
	if err != nil {
		return nil
	}
	dirs := map[string]string{}
	for _, r := range rows {
		if d := config.FriendDir(r); d != "" {
			dirs[r.Name] = d
		}
	}
	return dirs
}
