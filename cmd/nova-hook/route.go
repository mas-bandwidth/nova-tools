package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// A friend hooked with no harness route (no --harness, no Codex desktop route) has one
// delivery route: this process's stdout. Its HOOK CHALLENGE lines and its HOOK TAKEN lines
// (lanes.go) reach the model only when the friend's session reads that stream (a Monitor on
// the hook command). 2026-10-24: a friend's hook ran without --harness, its stdout a log file
// under an exited parent, so her challenges went unread and she kept dropping out. At arming
// the hook says so: one STATE state=route-stdout line always, and a WARN route=unread line
// when it can tell no session is reading (stdoutUnread).

// harnessFlagHint names the flag that delivers into the harness instead.
const harnessFlagHint = "--harness <grok|muse|claude|deepseek|antigravity|codex>"

// routeLines are the lines a friend's hook with no harness route prints after ARMED.
func (w world) routeLines() []*tool.Out {
	out := []*tool.Out{tool.Done().As("STATE").Fact("state", "route-stdout").
		Note("no harness route: HOOK CHALLENGE and HOOK TAKEN lines reach the model only through this process's stdout, so the friend's session must read it (a Monitor on this command); to deliver into the harness instead, hook with " + harnessFlagHint)}
	why := ""
	if w.stdoutUnread != nil {
		why = w.stdoutUnread()
	}
	if why != "" {
		out = append(out, tool.Done().As("WARN").Fact("route", "unread").
			Note(why+": challenges will not reach the model, so its record never proves and its lanes take no card; hook again under the session's Monitor, or with "+harnessFlagHint))
	}
	return out
}

// stdoutUnread says why no session can be reading this process's stdout: it is a regular
// file, or the null device, or the hook's parent has exited (parent pid 1). "" when a
// session may be reading it (a pipe or a terminal under a live parent): the hook cannot see
// past the pipe, so it never claims a reader.
func stdoutUnread() string {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return ""
	}
	if fi.Mode().IsRegular() {
		return "this hook's stdout is a regular file, which no session reads"
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return "this hook's stdout is " + os.DevNull
	}
	if os.Getppid() == 1 {
		return "this hook's parent has exited (parent pid 1), so no session reads its stdout"
	}
	return ""
}
