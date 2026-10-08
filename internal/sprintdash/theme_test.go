package sprintdash

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPageHasNoThemeToggleAndStaysDark is the always-dark rule (docs/SPEC-SPRINT-DASHBOARD.md):
// the page has no theme toggle or theme read; its inline script clears a stored
// sprint-theme. Unused light CSS cannot change the default dark document.
func TestPageHasNoThemeToggleAndStaysDark(t *testing.T) {
	t.Parallel()
	html := string(file("index.html"))
	app := string(file("app.js"))
	assert.NotContains(t, html, `id="theme"`, "index.html has a theme toggle button")
	assert.NotContains(t, html, `id="theme"`, "unused light CSS never exposes a theme switch")
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`, "the html element stays dark")
	assert.NotContains(t, app, "sprint-theme", "app.js still stores a theme")
	assert.NotContains(t, app, `$("theme")`, "app.js still binds the toggle button")
	assert.Contains(t, html, `removeItem("sprint-theme")`, "the inline script clears a stored theme")
	assert.NotContains(t, html, `getItem("sprint-theme")`, "panel folds may persist, but theme never restores")
}
