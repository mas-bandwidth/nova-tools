# nova-friend: friend daemon installation

nova-friend installs and manages a friend's daemon, which is a launchd plist
that runs the friend's work loop.

## Installation

The install verb writes the daemon's plist with environment variables and
arguments:

    nova-friend install --deny-self <paths> [--dry-run] [--as <you>]

--deny-self specifies paths that the daemon's wall (sandbox) should deny.
This is written into the plist's EnvironmentVariables with the key
NOVA_FRIEND_DENY_SELF.

If --deny-self is not given, nova-friend reads NOVA_FRIEND_DENY_SELF from
the environment.

--as is the friend name. When NOVA_FRIEND is set in the environment, it must
equal --as.

--dry-run prints what the run would be without actually installing:

    run: nova-friend run --as <you> --deny-self <paths>

## Uninstall

The uninstall verb removes the daemon's plist:

    nova-friend uninstall --as <you>

--as is the friend name. When NOVA_FRIEND is set in the environment, it must
equal --as.

## Exit codes

0 done
1 refused (store said no)
2 usage (flag or argument problem)

## Environment

NOVA_FRIEND The friend's name (default for --as)
NOVA_FRIEND_DENY_SELF Default deny paths for --deny-self

## Run verb

The run verb is called by the daemon after installation. It takes --deny-self
and sets up the wall with those deny paths.

    nova-friend run --as <you> --deny-self <paths>
