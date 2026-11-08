#!/bin/sh
# ACTIONS_RUNNER_HOOK_JOB_COMPLETED: the CI rules (cmd/nova-fleet/README.md, "CI rules"); put here by nova-fleet, edits are overwritten.
exec sh "$(dirname "$0")/nova-ci-rules.sh" completed
