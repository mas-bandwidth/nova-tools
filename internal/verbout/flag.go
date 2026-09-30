package verbout

import "flag"

// Flag registers the standard --json flag on fs and returns a pointer to its boolean value.
func Flag(fs *flag.FlagSet) *bool {
	return fs.Bool("json", false, "print the result as one JSON object instead of the lines")
}
