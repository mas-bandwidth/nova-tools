package main

// The value types the Prints verbs' line printer (line, in fn.go) renders:
// a quoted Go string, free text already escaped, a bare value printed with no
// key, a reason a line closes with, and the command a line closes with.

type (
	quoted string // a value printed as a quoted Go string: a remedy, a reason
	free   string // a value printed as it is: text already escaped (oneline.Err), or a list of words
	why    string // the reason a line closes with, ": <why>"
	next   string // the command a line closes with, "; run: <next>"
)

// bare is words printed with no key (a user's rules, a receipt's fields as
// their own String prints them), and v is the same thing as JSON.
type bare struct {
	text string
	v    any
}
