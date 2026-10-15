package notice

import "os/exec"

func runScript(script, word, dir string) error { return exec.Command(script, word, dir).Run() }
