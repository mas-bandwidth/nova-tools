package fixtures

import "os"

func run(args []string) int {
	if len(args) == 0 {
		return 0
	}
	if args[0] == "help" || args[0] == "--help" {
		os.Stderr.WriteString("REFUSED: no help\n")
		return 2
	}
	taskText := os.Args[2]
	_ = taskText
	return 0
}
