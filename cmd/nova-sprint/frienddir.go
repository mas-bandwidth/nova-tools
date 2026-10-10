package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/layout"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A friend's working directory is her nova-config row's dir (config.FriendDir).
// friend sync carries it onto the friends table (store.FriendSpec.Dir), and sync,
// reconcile (the verb and the run loop's tick), clean and the seat's inbox read it
// there or from the row. A row with no dir is <root>/<name>-working, or
// <nova_root>/ai/<name>/working when the machine's nova-config row names a nova_root.
// The explicit path avoids requiring a symlink as a sandbox root.

// friendDirNoted is the friends each app has said the fallback for, so a given
// writer receives the note once a run (a loop's passes and the server's ticks included).
var friendDirNoted sync.Map // friendDirKey -> true

type friendDirKey struct {
	a      *app
	friend string
}

// friendDir is the friend's working directory: dir, her row's, when it is set; else
// <root>/<friend>-working, or <nova_root>/ai/<friend>/working when the machine's
// nova-config row names a nova_root; the note is said on note the first time this app
// falls back for her. A nil note says nothing.
func (a *app) friendDir(friend, dir, root string, note io.Writer) string {
	if dir != "" {
		return dir
	}
	fallback := filepath.Join(root, friend+"-working")
	if nr := a.machineNovaRoot(); nr != "" {
		fallback = layout.ResolveFriend(nr, friend, "").Working
	}
	if note != nil {
		if _, said := friendDirNoted.LoadOrStore(friendDirKey{a, friend}, true); !said {
			fmt.Fprintf(note, "NOTE friend=%s has no dir on her nova-config row, so her working directory is %s; run: nova-config friend set %s --dir <her real working directory>\n", friend, oneline.Field(fallback), friend)
		}
	}
	return fallback
}

// machineNovaRoot is this machine's nova_root out of nova-config: the machine row whose
// name is this host, else the one row that sets a root. "" when no config store is named,
// no row sets a root, or more than one does and none names this host.
func (a *app) machineNovaRoot() string {
	if a.inventory == nil {
		return ""
	}
	machines, err := a.inventory(context.Background(), "")
	if err != nil {
		return ""
	}
	host, _ := os.Hostname()
	found, many := "", false
	for _, m := range machines {
		if m.NovaRoot == "" {
			continue
		}
		if m.Machine == host {
			return m.NovaRoot
		}
		switch {
		case found == "":
			found = m.NovaRoot
		case found != m.NovaRoot:
			many = true
		}
	}
	if many {
		return ""
	}
	return found
}

// friendWorkDir is the friend's working directory as a brief or a view names it to her:
// dir, her row's, when it is set, so no symlink is needed; else <nova_root>/ai/<name>/working
// when a nova_root is known, else today's ~/<friend>-working.
func friendWorkDir(novaRoot, friend, dir string) string {
	if dir != "" {
		return dir
	}
	if novaRoot == "" {
		return "~/" + friend + "-working"
	}
	return layout.ResolveFriend(novaRoot, friend, "").Working
}

// friendRowDirs is each friend's dir as nova-config's friend rows say it, by name (a
// row with none is absent). A nil reader, or a reader that refuses because no
// config store is named (--pg, else NOVA_PG_DSN), means no rows.
func (a *app) friendRowDirs(ctx context.Context) (map[string]string, error) {
	if a.friends == nil {
		return nil, nil
	}
	rows, err := a.friends(ctx, "")
	if err != nil {
		if strings.Contains(err.Error(), "--pg is required") {
			return nil, nil
		}
		return nil, err
	}
	dirs := map[string]string{}
	for _, r := range rows {
		if d := config.FriendDir(r); d != "" {
			dirs[r.Name] = d
		}
	}
	return dirs, nil
}
