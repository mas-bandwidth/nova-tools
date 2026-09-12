#!/usr/bin/env python3
"""Decide whether a release may proceed from the check-runs evidence for the
commit a tag points at.

Reads the GitHub check-runs API response on stdin (the body of
`GET /repos/{owner}/{repo}/commits/{sha}/check-runs`) and takes on argv the
expected commit SHA followed by the names of the check runs that must have
concluded `success`. With no names on argv it requires the ci workflow's
aggregate gate, `ci-ok`.

Prints ALLOW on stdout and exits 0 when every required check run exists for the
exact SHA, is completed, and concluded success. Prints one REFUSE line and exits
non-zero otherwise.

The gate fails closed. Each of these is a refusal, in this order:

  * the API response has no check_runs (or none at all) for the SHA,
  * no check run with the required name exists for the SHA,
  * the newest such run reports a different SHA than the one resolved,
  * the newest such run has not completed (still queued or in_progress),
  * the newest such run completed with a conclusion other than `success`.

The conclusion strings treated as not-success are the ones GitHub can report for
a job that did not pass -- failure, cancelled, timed_out, action_required, stale,
startup_failure, neutral and skipped -- plus a null conclusion (a run that never
reached one). Anything that is not exactly `success` is refused; the list is
spelled out so a timed-out Windows leg in ci.yml is a refusal here, never an
absence.
"""

import json
import sys

_NOT_SUCCESS = frozenset(
    (
        "failure",
        "cancelled",
        "timed_out",
        "action_required",
        "stale",
        "startup_failure",
        "neutral",
        "skipped",
    )
)


def latest_run(runs, name):
    matching = [r for r in runs if r.get("name") == name]
    if not matching:
        return None
    # Newest attempt first. completed_at is an ISO 8601 timestamp, so string order is
    # chronological; a run that never completed has no completed_at and sorts last, which
    # is exactly what we want for a run still in flight.
    matching.sort(
        key=lambda r: (
            r.get("completed_at") is not None,
            r.get("completed_at") or "",
            r.get("id", 0),
        ),
        reverse=True,
    )
    return matching[0]


def refuse(reason):
    print("REFUSE: " + reason)
    return 1


def main(argv):
    if len(argv) < 2:
        return refuse("release-evidence.py <expected-sha> [check-name ...]")
    expected_sha = argv[1]
    required = argv[2:] or ["ci-ok"]

    try:
        data = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        return refuse("the API response is not JSON: %s" % exc)

    runs = data.get("check_runs")
    if runs is None:
        return refuse("the API response has no check_runs for %s" % expected_sha)
    if not runs:
        return refuse("no check runs exist for %s" % expected_sha)

    for name in required:
        run = latest_run(runs, name)
        if run is None:
            return refuse("no check run named %r exists for %s" % (name, expected_sha))
        head_sha = run.get("head_sha")
        if head_sha != expected_sha:
            return refuse(
                "the %s check run reports SHA %s, not the resolved %s"
                % (name, head_sha, expected_sha)
            )
        status = run.get("status")
        if status != "completed":
            return refuse(
                "the %s check run is %s, not completed"
                % (name, status if status else "unreported")
            )
        conclusion = run.get("conclusion")
        if conclusion != "success":
            if conclusion in _NOT_SUCCESS:
                return refuse(
                    "the %s check run concluded %s, not success" % (name, conclusion)
                )
            if conclusion is None:
                return refuse(
                    "the %s check run completed without a conclusion" % name
                )
            return refuse(
                "the %s check run concluded %r, not success" % (name, conclusion)
            )

    print("ALLOW")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
