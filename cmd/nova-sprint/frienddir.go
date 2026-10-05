package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// friendDirFallbackNote prints the note when a friend row has no dir and
// falls back to <root>/<name>-working. It prints at most once per friend per app.
func (a *app) friendDirFallbackNote(friend, fallback string, say func(string)) {
	a.dirNotesMu.Lock()
	defer a.dirNotesMu.Unlock()
	if a.dirNotes == nil {
		a.dirNotes = make(map[string]bool)
	}
	if a.dirNotes[friend] {
		return
	}
	a.dirNotes[friend] = true
	note := fmt.Sprintf("NOTE friend=%s: her row has no dir; falling back to %s", friend, fallback)
	if say != nil {
		say(note)
	}
}

// resolveFriendDir returns rowDir if set; otherwise it returns
// filepath.Join(root, friend+"-working") and calls friendDirFallbackNote.
func (a *app) resolveFriendDir(friend, rowDir, root string, say func(string)) string {
	if rowDir != "" {
		return rowDir
	}
	fallback := filepath.Join(root, friend+"-working")
	a.friendDirFallbackNote(friend, fallback, say)
	return fallback
}

// friendDirOf looks up the friend's working directory from the store (if st != nil),
// or from a.friends config rows (if st == nil or st lookup returns empty/error).
func (a *app) friendDirOf(ctx context.Context, st *store.Store, friend string) string {
	if st != nil {
		if spec, err := st.FriendSpecOf(ctx, friend); err == nil && spec.Dir != "" {
			return spec.Dir
		}
	}
	if a.friends != nil {
		if rows, err := a.friends(ctx, ""); err == nil {
			for _, r := range rows {
				if r.Name == friend {
					return config.FriendDir(r)
				}
			}
		}
	}
	return ""
}
