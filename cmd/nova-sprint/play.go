package main

import "io"

func (a *app) cmdPlay(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("play")
	if _, err := parse(fs, args); err != nil {
		return refuse(stderr, "play", err.Error())
	}
	return refuse(stderr, "play", "not built yet")
}
