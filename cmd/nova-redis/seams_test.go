package main

// The test seams of nova-redis: main() runs redisTool(realDeps()).Main(),
// and the tests run the same Tool in process over their own deps and
// buffers. Both are test code so the binary carries no function it never
// calls (internal/ci TestDeadCode).

import (
	"flag"
	"io"
	"os"
)

// run is the tool built over d, run in process: the same Tool main() runs.
func run(args []string, stdout, stderr io.Writer, d deps) int {
	return redisTool(d).Run(args, os.Stdin, stdout, stderr)
}

// loginFromFlags reads the login flags from a flag set, for the unit tests
// that hold the login logic directly.
func loginFromFlags(fs *flag.FlagSet) login {
	return login{
		addr:        fs.String("addr", "", "the store's address as <host:port>, such as 127.0.0.1:6379 (no default)"),
		user:        fs.String("user", "", "the ACL user to log in as (default $"+UserEnv+"; with neither, the store's default user)"),
		passwordEnv: fs.String("password-env", "", "the NAME of the variable that holds the password, never the password itself (default: the variable $"+PasswordEnvEnv+" names, else "+PasswordEnv+")"),
		givenFn: func(name string) bool {
			on := false
			fs.Visit(func(f *flag.Flag) { on = on || f.Name == name })
			return on
		},
	}
}
