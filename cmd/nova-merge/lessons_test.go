package main

import (
	"testing"
)

// The lessons that are code, each with the hurt it came from.

// Lesson 48: a lane's --base is stored and later handed to git as an argument. A value
// starting with a dash is a flag to whatever reads it, and a value holding a space or a
// control character is a refname git will refuse later, far from the person who typed it.
func TestARefNameIsCheckedWhereItIsTyped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	for _, c := range []struct{ flag, value string }{
		{"--base", "--upload-pack=touch /evil"},
		{"--base", "a branch with spaces"},
		{"--lane-branch", "-x"},
	} {
		t.Run(c.flag+c.value, func(t *testing.T) {
			args := []string{"init", "--lane", l.dir + "/l", "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane"}
			for i := range args {
				if args[i] == c.flag {
					args[i+1] = c.value
				}
			}
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("a refname is checked where it is typed: exit %d\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, c.flag)
		})
	}
}
