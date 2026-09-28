#!/usr/bin/env python3
"""Validate bounded nova-table shell executions against TableSession.

Capture is a real functional Go test on its own Redis and loss relay. TLC checks
the observed session projection; durable/printed receipts are checked separately.
The modeled subset is answered/lost verbs, usage/length refusals, keep-going,
quit and EOF. Signals/store outages have separate functional/model controls.
One 120-second budget includes capture, TLC and mandatory negative controls.
No tools are downloaded. Output contains source hashes, traces and runnable TLC.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


def traces_from_go(path):
    rows = [json.loads(line) for line in path.read_text().splitlines()]
    if any(r.get("Action") == "fail" for r in rows):
        raise ValueError("execution test failed")
    if not any(r.get("Action") == "pass" and "Test" not in r for r in rows):
        raise ValueError("execution test did not pass (a skip is not evidence)")
    # test2json splits long t.Log lines across Output events.
    output = "".join(r.get("Output", "") for r in rows)
    traces = [json.loads(line.split("SESSION_TRACE ", 1)[1])
              for line in output.splitlines() if "SESSION_TRACE " in line]
    if len(traces) != 16:
        raise ValueError(f"wanted all 16 bounded sessions, got {len(traces)}")
    return traces


def check_receipts(traces):
    for tr in traces:
        revision, ids = 0, set()
        for step in tr["Steps"]:
            action, receipts = step["Action"], step["Receipts"]
            if len(receipts) != int(action["Write"]):
                raise ValueError("receipt count does not match committed effects")
            printed = [s for s in step["Stdout"].splitlines() if s.startswith("TABLE RECEIPT ")]
            if len(printed) != int(action["Write"] and not action["Lost"]):
                raise ValueError("printed receipt count differs")
            for receipt in receipts:
                fields = receipt["Values"]
                if receipt["ID"] in ids:
                    raise ValueError("duplicate durable receipt")
                ids.add(receipt["ID"])
                if (fields["rev_before"], fields["rev_after"], fields["actor"]) != (
                        str(revision), str(revision + 1), "trace"):
                    raise ValueError("receipt revision or actor differs")
                revision += 1
                if not action["Lost"] and f'event={receipt["ID"]} ' not in printed[0]:
                    raise ValueError("printed receipt ID differs from durable event")


def seq(items):
    return "<<" + ", ".join(items) + ">>"


def string(value):
    return json.dumps(value)


def module(traces):
    inputs, keeps, exits, remains, ends, lengths, actions, observations = [], [], [], [], [], [], [], []
    for case, tr in enumerate(traces, 1):
        inputs.append(seq([string(a["Kind"]) for a in tr["Actions"]]))
        keeps.append(str(tr["Keep"]).upper())
        exits.append(str(tr["Exit"]))
        remains.append(str(len(tr["Actions"]) - len(tr["Steps"])))
        ends.append(string("eof" if tr["EOF"] else
                           "quit" if tr["Steps"][-1]["Action"]["Kind"] == "quit" else "fail"))
        tick, observed_exit = 0, 0
        def add(action):
            nonlocal tick
            actions.append(f'  \\/ /\\ traceCase = {case} /\\ tick = {tick}\n'
                           f'     /\\ {action}\n     /\\ tick\' = {tick + 1}')
            tick += 1
        for step in tr["Steps"]:
            kind = step["Action"]["Kind"]
            read_code = 2 if kind in ("usage", "long") else 0
            add("ReadLine /\\ lineCode' = " + str(read_code))
            if kind in ("ok", "no"):
                # Observe the response classification, not the fixture's intended
                # loss toggle. RunVerb determines the model's line status.
                connection = "dead" if step["Code"] == 2 else "live"
                add('RunVerb /\\ conn\' = ' + string(connection) +
                    " /\\ lineCode' = IF conn' = \"dead\" THEN 2 ELSE Code(cur)")
            observed_exit = max(observed_exit, step["Code"])
            observations.append(f" /\\ ((traceCase = {case} /\\ tick = {tick}) => (exit = {observed_exit} /\\ lineCode = {step['Code']}))")
        if tr["EOF"]:
            add("EndOfInput /\\ UNCHANGED lineCode")
        lengths.append(str(tick))
    # Restrict Inputs to the suffix closure of captured input sequences. This
    # only bounds the trace replay; every transition still uses the model.
    return """---------------------- MODULE SessionTrace ----------------------
EXTENDS TableSession
VARIABLES traceCase, tick, lineCode
traceVars == <<vars, traceCase, tick, lineCode>>
ObservedInputs == %s
TraceInputs == UNION {{SubSeq(s, j, Len(s)) : j \\in 1..(Len(s)+1)} : s \\in {ObservedInputs[i] : i \\in DOMAIN ObservedInputs}}
ObservedKeep == %s
ObservedExit == %s
ObservedRemaining == %s
ObservedEnd == %s
TraceLength == %s
TraceInit ==
 /\\ traceCase \\in 1..%d
 /\\ input = ObservedInputs[traceCase]
 /\\ keep = ObservedKeep[traceCase]
 /\\ store = "up"
 /\\ Init
 /\\ tick = 0
 /\\ lineCode = 0
TraceStep ==
%s
TraceNext ==
 /\\ UNCHANGED traceCase
 /\\ (TraceStep \\/ (tick = TraceLength[traceCase] /\\ UNCHANGED <<vars, tick, lineCode>>))
TraceSpec == TraceInit /\\ [][TraceNext]_traceVars
TraceObserved ==
%s
TraceEnd ==
 (tick = TraceLength[traceCase]) =>
   /\\ pc = "done"
   /\\ exit = ObservedExit[traceCase]
   /\\ Len(input) = ObservedRemaining[traceCase]
   /\\ how = ObservedEnd[traceCase]
=============================================================================
""" % (seq(inputs), seq(keeps), seq(exits), seq(remains), seq(ends), seq(lengths),
       len(traces), "\n".join(actions), "\n".join(observations))


def run_tlc(root, jar, out, traces, deadline):
    out.mkdir(parents=True, exist_ok=True)
    (out / "TableSession.tla").write_bytes((root / "tla/TableSession.tla").read_bytes())
    (out / "SessionTrace.tla").write_text(module(traces))
    bound = max(len(tr["Actions"]) for tr in traces)
    (out / "SessionTrace.cfg").write_text(
        'SPECIFICATION TraceSpec\nCONSTANTS\n Inputs <- TraceInputs\n MaxLines = ' + str(bound) +
        '\n Broken = "none"\nINVARIANTS TypeOK NothingStartsAfterStop StopOnlyWhenStopped '
        'NoFalseAlarm ConnectionFailureIsTwo AtMostOnce StopsAtFirstFailure ExitIsHighest '
        'KeepGoingReadsEveryLine EndOfInputMeansNoFailure EndsForAReason LineInHand '
        'LiveMeansUp TraceObserved TraceEnd\n')
    with tempfile.TemporaryDirectory(prefix="tlc-session-") as scratch:
        with (out / "tlc.log").open("w") as log:
            result = subprocess.run(
                ["java", "-XX:+UseParallelGC", "-Xmx512m",
                 "-Djava.io.tmpdir=" + scratch, "-cp", str(jar), "tlc2.TLC",
                 "-workers", "2", "-metadir", str(Path(scratch) / "states"),
                 "-config", "SessionTrace.cfg", "SessionTrace.tla"],
                cwd=out, stdout=log, stderr=subprocess.STDOUT,
                timeout=max(.01, deadline - time.monotonic()))
    return result.returncode, (out / "tlc.log").read_text()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--jar", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    if not args.jar.is_file():
        parser.error("--jar must name an existing TLC jar")
    root = Path(__file__).resolve().parents[1]
    out, jar = args.out.resolve(), args.jar.resolve()
    out.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    deadline = started + 120
    capture = out / "capture.json"
    with capture.open("w") as log:
        result = subprocess.run(
            ["go", "test", "-tags", "functional", "-p", "2", "-parallel", "2",
             "-count=1", "-timeout", "50s", "-json", "./cmd/nova-table",
             "-run", "^TestShellRandomSequencesProduceSessionTrace$"],
            cwd=root, env=dict(os.environ, GOMAXPROCS="2", NOVA_CI="1"),
            stdout=log, stderr=subprocess.STDOUT,
            timeout=max(.01, deadline - time.monotonic()))
    if result.returncode:
        raise RuntimeError("capture failed: " + str(capture))
    traces = traces_from_go(capture)
    check_receipts(traces)
    (out / "trace.json").write_text(json.dumps(traces, indent=2) + "\n")
    files = ["tla/TableSession.tla", "tla/check_session_trace.py",
             "cmd/nova-table/session.go", "cmd/nova-table/session_property_functional_test.go",
             "cmd/nova-table/connect_functional_test.go"]
    hashes = {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in files}
    hashes["tlc_jar"] = hashlib.sha256(jar.read_bytes()).hexdigest()
    (out / "hashes.json").write_text(json.dumps(hashes, indent=2) + "\n")
    code, log = run_tlc(root, jar, out / "positive", traces, deadline)
    if code or "Model checking completed. No error has been found." not in log:
        raise RuntimeError("positive TLC failed: " + str(out / "positive/tlc.log"))
    bad_exit = copy.deepcopy(traces)
    bad_exit[0]["Exit"] = (bad_exit[0]["Exit"] + 1) % 3
    code, log = run_tlc(root, jar, out / "wrong-exit", bad_exit, deadline)
    if code != 12 or "Invariant TraceEnd is violated." not in log:
        raise RuntimeError("TLC failed to reject wrong final exit")
    bad_status = copy.deepcopy(traces)
    # Corrupt a success AFTER exit status has already reached 2. Checking
    # only the accumulated exit would miss this per-line corruption.
    corrupted = False
    for tr in bad_status:
        high = 0
        for step in tr["Steps"]:
            if high == 2 and step["Code"] == 0 and step["Action"]["Kind"] == "ok":
                step["Code"] = 1
                corrupted = True
                break
            high = max(high, step["Code"])
        if corrupted:
            break
    if not corrupted:
        raise RuntimeError("no high-exit trace for line-status negative control")
    code, log = run_tlc(root, jar, out / "wrong-line-status", bad_status, deadline)
    if code != 12 or "Invariant TraceObserved is violated." not in log:
        raise RuntimeError("TLC failed to reject wrong per-line status")
    bad_receipt = copy.deepcopy(traces)
    bad_receipt[0]["Steps"][0]["Receipts"].append(copy.deepcopy(
        bad_receipt[0]["Steps"][0]["Receipts"][0]))
    try:
        check_receipts(bad_receipt)
    except ValueError:
        pass
    else:
        raise RuntimeError("receipt checker accepted duplicate effect")
    (out / "result.json").write_text(json.dumps({
        "seconds": round(time.monotonic() - started, 3),
        "sessions": len(traces), "steps": sum(len(tr["Steps"]) for tr in traces),
        "positive_tlc": "PASS", "wrong_exit_control": "REJECTED",
        "wrong_line_status_control": "REJECTED", "duplicate_effect_control": "REJECTED",
    }, indent=2) + "\n")
    print((out / "result.json").read_text(), end="")


if __name__ == "__main__":
    main()
