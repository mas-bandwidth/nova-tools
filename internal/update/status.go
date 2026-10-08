// status is inspection: the reads of check; writes nothing.

package update

import "io"

// statusVerb is inspection: the reads of check; writes nothing.
func statusVerb(name string, o options, asJSON bool, positional []string, out, errs io.Writer, env Environment) int {
	return emit(checked(name, "status", o, positional, env), asJSON, o.max, out, errs)
}
