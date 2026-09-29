#!/usr/bin/env python3
"""
card-manager.py — Candidate card layer manager CLI for SPEC nova-sprint v1 (ideas#825).
Operates over arrays of cards against Redis table storage via nova-table batch backend.
"""

import sys
import os
import json
import socket
import hashlib
import subprocess

TABLE_DEFAULT = "cards"
REDIS_ADDR = None

def get_redis_addr():
    global REDIS_ADDR
    if REDIS_ADDR:
        return REDIS_ADDR
    addr = None
    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "--redis" and i + 1 < len(args):
            addr = args[i+1]
            break
        i += 1
    if not addr:
        addr = os.environ.get("NOVA_SPRINT_REDIS") or os.environ.get("NOVA_REDIS_ADDR")
    if not addr:
        sys.stderr.write("refused: explicit redis address required (--redis or NOVA_REDIS_ADDR); ambient autodiscovery disabled for test confinement\n")
        sys.exit(2)
    REDIS_ADDR = addr
    return REDIS_ADDR

def get_nova_table_bin():
    nova_table = os.environ.get("NOVA_TABLE_BIN")
    if nova_table and os.path.exists(nova_table) and os.access(nova_table, os.X_OK):
        return nova_table
    import shutil
    candidate = shutil.which("nova-table")
    if candidate:
        return candidate
    raise RuntimeError("NOVA_TABLE_BIN not set to executable and nova-table not found in PATH")

def nova_table_cmd(*args):
    addr = get_redis_addr()
    cmd = [get_nova_table_bin()] + list(args) + ["--redis", addr]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    return p.returncode, p.stdout.strip(), p.stderr.strip()

def run_nova_table_batch(manifest):
    addr = get_redis_addr()
    bin_path = get_nova_table_bin()
    for m in manifest.get("members", []):
        for k in list(m.keys()):
            if m[k] is None:
                del m[k]
    cmd = [bin_path, "batch", "-", "--redis", addr]
    p = subprocess.run(
        cmd,
        input=json.dumps(manifest),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    return p.returncode, p.stdout.strip(), p.stderr.strip()

def read_resp(f):
    line = f.readline()
    if not line:
        return None
    prefix = line[:1]
    payload = line[1:-2]
    if prefix == b"+":
        return payload.decode("utf-8", errors="replace")
    elif prefix == b"-":
        raise RuntimeError(f"Redis error: {payload.decode('utf-8', errors='replace')}")
    elif prefix == b":":
        return int(payload)
    elif prefix == b"$":
        length = int(payload)
        if length == -1:
            return None
        val = f.read(length)
        f.read(2)  # trailing \r\n
        return val.decode("utf-8", errors="replace")
    elif prefix == b"*":
        count = int(payload)
        if count == -1:
            return None
        items = []
        for _ in range(count):
            items.append(read_resp(f))
        return items
    else:
        raise RuntimeError(f"Unknown RESP prefix: {prefix}")

def redis_cmd(*args):
    addr = get_redis_addr()
    if ":" in addr:
        host, port_str = addr.rsplit(":", 1)
        port = int(port_str)
    else:
        host = "127.0.0.1"
        port = int(addr)
    s = socket.socket()
    s.connect((host, port))
    req = b"*" + str(len(args)).encode("utf-8") + b"\r\n"
    for a in args:
        if isinstance(a, bytes):
            encoded = a
        else:
            encoded = str(a).encode("utf-8")
        req += b"$" + str(len(encoded)).encode("utf-8") + b"\r\n" + encoded + b"\r\n"
    s.sendall(req)
    f = s.makefile("rb")
    res = read_resp(f)
    s.close()
    return res

def redis_hgetall(key):
    raw = redis_cmd("HGETALL", key)
    if not isinstance(raw, list):
        return {}
    d = {}
    for i in range(0, len(raw), 2):
        if i + 1 < len(raw):
            d[raw[i]] = raw[i+1]
    return d

def redis_hset(key, mapping):
    args = ["HSET", key]
    for k, v in mapping.items():
        args.extend([str(k), str(v)])
    res = redis_cmd(*args)
    if not isinstance(res, int) or res < 0:
        raise RuntimeError(f"HSET {key} failed: {res}")
    return res

def redis_del(key):
    return redis_cmd("DEL", key)

def get_table_revision(table):
    raw = redis_cmd("HGET", f"table:{table}:revision", "n")
    if raw is not None:
        try:
            return int(raw)
        except ValueError:
            return 0
    return 0

def decode_and_verify_receipt(table, op_id, expected_epoch, nova_table_stdout):
    """
    Decodes the actual committed receipt from nova-table stdout,
    verifies it against the independent Redis oprecord, table revision,
    and change stream, and retains it in durable Redis storage.
    """
    batch_data = {}
    receipt_data = {}
    for line in nova_table_stdout.splitlines():
        line = line.strip()
        if line.startswith("TABLE BATCH "):
            parts = line.split()
            for part in parts[2:]:
                if "=" in part:
                    k, v = part.split("=", 1)
                    batch_data[k] = v
        elif line.startswith("TABLE RECEIPT "):
            parts = line.split()
            for part in parts[2:]:
                if "=" in part:
                    k, v = part.split("=", 1)
                    receipt_data[k] = v

    if not receipt_data or "event" not in receipt_data:
        raise RuntimeError(f"failed to decode committed receipt from nova-table stdout:\n{nova_table_stdout}")

    event_id = receipt_data["event"]
    r_epoch = receipt_data.get("epoch", str(expected_epoch))
    r_before = int(receipt_data.get("before", "0"))
    r_after = int(receipt_data.get("after", "0"))
    r_outcome = receipt_data.get("outcome", "changed")

    if batch_data:
        if batch_data.get("table") and batch_data.get("table") != table:
            raise RuntimeError(f"TABLE BATCH table {batch_data.get('table')} != requested table {table}")
        if batch_data.get("operation") and batch_data.get("operation") != op_id:
            raise RuntimeError(f"TABLE BATCH operation {batch_data.get('operation')} != requested operation {op_id}")
        if batch_data.get("epoch") and str(batch_data.get("epoch")) != str(expected_epoch):
            raise RuntimeError(f"TABLE BATCH epoch {batch_data.get('epoch')} != expected epoch {expected_epoch}")

    if str(r_epoch) != str(expected_epoch):
        raise RuntimeError(f"receipt epoch {r_epoch} != expected epoch {expected_epoch}")

    # 1. Query independent oprecord in Redis: table:{table}:op:{op_id} or table:{table}:{epoch}:op:{op_id}
    prefix = f"table:{table}" if str(r_epoch) == "0" else f"table:{table}:{r_epoch}"
    op_key = f"{prefix}:op:{op_id}"
    op_record = redis_hgetall(op_key)
    if not op_record:
        raise RuntimeError(f"committed oprecord not found at independent Redis key {op_key}")

    # Compare actual committed receipt against independent Redis state / oprecord
    if op_record.get("stream_id") != event_id:
        raise RuntimeError(f"receipt event {event_id} does not match oprecord stream_id {op_record.get('stream_id')}")
    if op_record.get("epoch") != str(r_epoch):
        raise RuntimeError(f"receipt epoch {r_epoch} does not match oprecord epoch {op_record.get('epoch')}")
    if int(op_record.get("rev_before", "0")) != r_before:
        raise RuntimeError(f"receipt rev_before {r_before} does not match oprecord rev_before {op_record.get('rev_before')}")
    if int(op_record.get("rev_after", "0")) != r_after:
        raise RuntimeError(f"receipt rev_after {r_after} does not match oprecord rev_after {op_record.get('rev_after')}")
    if op_record.get("outcome") != r_outcome:
        raise RuntimeError(f"receipt outcome {r_outcome} does not match oprecord outcome {op_record.get('outcome')}")

    # Decode complete committed batch delta from oprecord["result"]
    op_result_raw = op_record.get("result", "")
    if not op_result_raw:
        raise RuntimeError(f"oprecord at {op_key} missing result field")
    op_result = json.loads(op_result_raw)
    if not isinstance(op_result, list) or len(op_result) < 2 or not isinstance(op_result[1], list):
        raise RuntimeError(f"malformed oprecord result wire format at {op_key}")
    wire_receipt = op_result[1]
    if len(wire_receipt) < 7:
        raise RuntimeError(f"oprecord result wire receipt missing batch_delta at {op_key}")
    delta_payload = wire_receipt[6]
    batch_delta = json.loads(delta_payload) if isinstance(delta_payload, str) else delta_payload

    # 2. Independent table revision in Redis
    current_rev = get_table_revision(table)
    if current_rev != r_after:
        raise RuntimeError(f"receipt after revision {r_after} does not match Redis table revision {current_rev}")

    # 3. Independent change stream entry verification in table:{table}:changes
    stream_entries = redis_cmd("XRANGE", f"table:{table}:changes", event_id, event_id)
    if not stream_entries or len(stream_entries) != 1:
        raise RuntimeError(f"change stream entry {event_id} missing in table:{table}:changes")
    se = stream_entries[0]
    sfv = se[1]
    smap = {sfv[i]: sfv[i+1] for i in range(0, len(sfv), 2)}
    if smap.get("verb") != "apply":
        raise RuntimeError(f"stream entry verb is {smap.get('verb')}, expected apply")
    if smap.get("epoch") != str(r_epoch):
        raise RuntimeError(f"stream entry epoch {smap.get('epoch')} != {r_epoch}")
    if int(smap.get("rev_before", "0")) != r_before or int(smap.get("rev_after", "0")) != r_after:
        raise RuntimeError(f"stream entry revs {smap.get('rev_before')}->{smap.get('rev_after')} != {r_before}->{r_after}")
    if smap.get("outcome") != r_outcome:
        raise RuntimeError(f"stream entry outcome {smap.get('outcome')} != {r_outcome}")

    # 4. Retain complete committed receipt in Redis state
    redis_hset(f"receipt:{table}:{op_id}", {
        "operation_id": op_id,
        "table": table,
        "event": event_id,
        "epoch": str(r_epoch),
        "before": str(r_before),
        "after": str(r_after),
        "outcome": r_outcome,
        "verified": "yes",
        "op_key": op_key,
        "digest": batch_delta.get("digest", ""),
        "actor": batch_delta.get("actor", ""),
        "selected_count": str(batch_delta.get("selected_count", 0)),
        "guard_count": str(batch_delta.get("guard_count", 0)),
        "changed_count": str(batch_delta.get("changed_count", 0)),
        "batch_delta": json.dumps(batch_delta)
    })

    return {
        "event": event_id,
        "epoch": r_epoch,
        "before": r_before,
        "after": r_after,
        "outcome": r_outcome,
        "op_key": op_key,
        "batch_delta": batch_delta
    }

def file_digest(path):
    if os.path.exists(path):
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    return "0" * 64

def parse_card_file(path):
    if not os.path.exists(path):
        raise ValueError(f"file not found: {path}")
    with open(path, "r", encoding="utf-8") as f:
        lines = [line.rstrip("\r\n") for line in f]
    if not lines:
        raise ValueError("empty card file")
    line1 = lines[0].strip()
    headers = {}
    i = 1
    while i < len(lines):
        line = lines[i]
        if not line:
            i += 1
            continue
        if ":" in line and not line.startswith(" ") and not line.startswith("\t") and not line.startswith("#"):
            k, v = line.split(":", 1)
            headers[k.strip()] = v.strip()
            i += 1
        else:
            break
    card_id = headers.get("ID", line1)
    return card_id, headers, file_digest(path)

# ----------------- Verbs -----------------

def cmd_add(args):
    table = TABLE_DEFAULT
    admissions_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--admissions" and i + 1 < len(args):
            admissions_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1
    if not admissions_file:
        sys.stderr.write("refused: card add wants --admissions <file> --table <table>\n")
        return 2

    with open(admissions_file) as f:
        manifest = json.load(f)

    op_id = manifest.get("operation_id", "op-add")
    epoch = str(manifest.get("epoch", "0"))
    admissions = manifest.get("admissions", [])

    # Check for duplicate card IDs in admissions manifest (preflight validation)
    seen = set()
    for adm in admissions:
        cid = adm.get("id")
        if cid in seen:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=duplicate_card_in_manifest expected=unique observed={cid} changed=no remedy=remove duplicate from {admissions_file}\n")
            return 1
        seen.add(cid)

    # Pre-state check: verify cards absent
    before_table_rev = get_table_revision(table)
    delta_members = []
    batch_members = []

    for idx, adm in enumerate(admissions):
        cid = adm.get("id")
        card_file = adm.get("file")
        stream = adm.get("stream", "stream-1")
        
        # Check if already present in table
        mem = redis_hgetall(f"card:{cid}")
        if mem.get(f"place:{table}"):
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=already_admitted expected=absent observed={mem.get(f'place:{table}')} changed=no remedy=card inspect --table {table}\n")
            return 1

        _, headers, digest = parse_card_file(card_file)
        batch_members.append({
            "id": cid,
            "expect": {"absent": True},
            "create": {
                "row": stream,
                "col": "waiting",
                "score": float(idx + 1)
            },
            "set": {
                "id": cid,
                "stream": stream,
                "title": headers.get("TITLE", ""),
                "kind": headers.get("KIND", "fix-red"),
                "paths": headers.get("PATHS", "none"),
                "depends_on": headers.get("DEPENDS-ON", "-"),
                "tier": headers.get("TIER", "flash"),
                "definition": card_file,
                "digest": digest
            }
        })

        delta_members.append({
            "id": cid,
            "before_place": "",
            "after_place": f"{stream}:waiting",
            "before_rev": "0",
            "after_rev": "1"
        })

    table_manifest = {
        "schema": 1,
        "table": table,
        "epoch": epoch,
        "expected_table_revision": str(before_table_rev),
        "operation_id": op_id,
        "members": batch_members
    }

    code, out, err = run_nova_table_batch(table_manifest)
    if code != 0:
        sys.stderr.write(f"RUNTIME REFUSED {op_id} server refusal: {err}\n")
        return code

    rcpt = decode_and_verify_receipt(table, op_id, epoch, out)
    delta = rcpt["batch_delta"]
    print(f"RECEIPT {op_id} event={rcpt['event']} epoch={rcpt['epoch']} before={rcpt['before']} after={rcpt['after']} outcome={rcpt['outcome']} delta={json.dumps(delta)}")
    return 0

def cmd_resolve(args):
    table = TABLE_DEFAULT
    scope_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--scope" and i + 1 < len(args):
            scope_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1
    if not scope_file:
        sys.stderr.write("refused: card resolve wants --scope <scope-file> --table <table>\n")
        return 2

    with open(scope_file) as f:
        scope = json.load(f)

    op_id = scope.get("operation_id", "op-resolve")
    epoch = str(scope.get("epoch", "0"))
    members = scope.get("members", [])

    before_table_rev = get_table_revision(table)
    delta_members = []
    blocked_members = []

    # Read pre-state for ALL members in scope
    pre_state = {}
    for cid in members:
        mem = redis_hgetall(f"card:{cid}")
        place = mem.get(f"place:{table}", "")
        pre_state[cid] = {
            "place": place,
            "depends_on": mem.get("depends_on", "-"),
            "kind": mem.get("kind", ""),
            "stream": mem.get("stream", "stream-1"),
            "rev": mem.get("revision", "1")
        }

    # Evaluate eligibility based on single pre-state
    for cid, state in pre_state.items():
        if "waiting" not in state["place"]:
            continue
        deps = [d.strip() for d in state["depends_on"].split(",") if d.strip() and d.strip() != "-"]
        all_met = True
        missing_dep = None
        for dep in deps:
            dep_mem = redis_hgetall(f"card:{dep}")
            dep_place = dep_mem.get(f"place:{table}", "")
            # Dependency satisfied only when landed or done
            if not ("landed" in dep_place or "done" in dep_place):
                all_met = False
                missing_dep = dep
                break
        
        stream = state["stream"]
        if all_met:
            new_rev = str(int(state["rev"]) + 1)
            delta_members.append({
                "id": cid,
                "before_place": f"{stream}:waiting",
                "after_place": f"{stream}:ready",
                "before_rev": state["rev"],
                "after_rev": new_rev,
                "stream": stream
            })
        else:
            blocked_members.append({
                "id": cid,
                "place": f"{stream}:waiting",
                "blocked_by": missing_dep
            })

    event_id = ""
    outcome = "unchanged"
    after_table_rev = before_table_rev
    if delta_members:
        batch_members = []
        for m in delta_members:
            batch_members.append({
                "id": m["id"],
                "expect": {
                    "revision": m["before_rev"],
                    "place": {"row": m["stream"], "col": "waiting"}
                },
                "move": {
                    "row": m["stream"],
                    "col": "ready"
                }
            })
        table_manifest = {
            "schema": 1,
            "table": table,
            "epoch": epoch,
            "expected_table_revision": str(before_table_rev),
            "operation_id": op_id,
            "members": batch_members
        }
        code, out, err = run_nova_table_batch(table_manifest)
        if code != 0:
            sys.stderr.write(f"RUNTIME REFUSED {op_id} server refusal: {err}\n")
            return code

        rcpt = decode_and_verify_receipt(table, op_id, epoch, out)
        event_id = rcpt["event"]
        before_table_rev = rcpt["before"]
        after_table_rev = rcpt["after"]
        outcome = rcpt["outcome"]
        delta = dict(rcpt["batch_delta"])
        delta["blocked_count"] = len(blocked_members)
        delta["blocked"] = blocked_members
    else:
        delta = {
            "operation_id": op_id,
            "changed_count": 0,
            "blocked_count": len(blocked_members),
            "members": [],
            "blocked": blocked_members
        }

    event_str = f"event={event_id} " if event_id else ""
    print(f"RECEIPT {op_id} {event_str}epoch={epoch} before={before_table_rev} after={after_table_rev} outcome={outcome} delta={json.dumps(delta)}")
    return 0

def cmd_move(args):
    table = TABLE_DEFAULT
    events_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--events" and i + 1 < len(args):
            events_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1
    if not events_file:
        sys.stderr.write("refused: card move wants --events <file> --table <table>\n")
        return 2

    with open(events_file) as f:
        manifest = json.load(f)

    op_id = manifest.get("operation_id", "op-move")
    epoch = str(manifest.get("epoch", "0"))
    expected_table_rev = manifest.get("expected_table_revision")
    events = manifest.get("events", [])

    # Check for duplicate events for one card in same batch (preflight validation)
    seen_cards = set()
    for ev in events:
        cid = ev.get("id")
        if cid in seen_cards:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=duplicate_card_events_in_batch expected=one_event_per_card observed=multiple changed=no remedy=split into sequential batches\n")
            return 1
        seen_cards.add(cid)

    # Pre-state read and guard validation for ALL events
    pre_state = {}
    for ev in events:
        cid = ev.get("id")
        mem = redis_hgetall(f"card:{cid}")
        place = mem.get(f"place:{table}", "")
        pre_rev = mem.get("revision", "1")
        pre_state[cid] = {
            "place": place,
            "revision": pre_rev,
            "stream": mem.get("stream", "stream-1"),
            "kind": mem.get("kind", ""),
            "head": mem.get("head", "")
        }

    # Prepare atomic batch mutation
    batch_members = []
    delta_members = []
    for ev in events:
        cid = ev.get("id")
        to_state = ev.get("to")
        state = pre_state[cid]
        stream = state["stream"]
        from_col = state["place"].split(":")[-1]
        new_rev = str(int(state["revision"]) + 1)

        # Gate check for moving to merging: require at least 2 distinct reader records with accepted disposition & pass CI matching exact card head
        if to_state == "merging":
            card_head = ev.get("head") or state.get("head")
            if not card_head:
                mem = redis_hgetall(f"card:{cid}")
                card_head = mem.get("head")

            if not card_head:
                sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=missing_card_head expected=exact_head_commit observed=none changed=no remedy=record exact-head evidence first\n")
                return 1

            git_check = subprocess.run(["git", "rev-parse", "--verify", f"{card_head}^{{commit}}"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            if git_check.returncode != 0:
                sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=invalid_card_head expected=exact_commit_sha observed={card_head} changed=no\n")
                return 1
            exact_card_head = git_check.stdout.strip()

            readers_raw = redis_cmd("SMEMBERS", f"evidence:{cid}:readers")
            reader_list = readers_raw if isinstance(readers_raw, list) else []

            valid_readers = []
            matching_readers = []
            keys_raw = redis_cmd("KEYS", f"evidence:{cid}:*:*")
            evidence_keys = keys_raw if isinstance(keys_raw, list) else []

            for ekey in evidence_keys:
                if ekey.endswith(":readers"):
                    continue
                ktype = redis_cmd("TYPE", ekey)
                if ktype != "hash":
                    continue
                edata = redis_hgetall(ekey)
                r_name = edata.get("reader")
                if not r_name:
                    continue
                if edata.get("disposition") in ("accepted", "approved") and edata.get("ci_status") in ("pass", "passed", "green"):
                    valid_readers.append(r_name)
                    rec_head = edata.get("head")
                    if rec_head == exact_card_head:
                        matching_readers.append(r_name)

            if len(set(valid_readers)) < 2:
                sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=insufficient_evidence expected=at_least_2_approved_readers observed={len(set(valid_readers))} changed=no remedy=record 2 distinct reader approvals with ci_status=pass\n")
                return 1

            if len(set(matching_readers)) < 2:
                sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=head_mismatch expected_head={exact_card_head} matching_readers={len(set(matching_readers))} changed=no remedy=reader evidence must match exact card head\n")
                return 1

        set_fields = {}
        if "outcome" in ev:
            set_fields["outcome"] = ev["outcome"]
        if "landing_sha" in ev:
            set_fields["landing_sha"] = ev["landing_sha"]
        if "head" in ev:
            set_fields["head"] = ev["head"]

        exp_rev = str(ev.get("expect_revision", state["revision"]))
        exp_row = stream
        exp_col = from_col
        if "expect_place" in ev:
            ep = ev["expect_place"]
            if ":" in ep:
                exp_row, exp_col = ep.split(":", 1)
            else:
                exp_col = ep

        entry = {
            "id": cid,
            "expect": {
                "revision": exp_rev,
                "place": {"row": exp_row, "col": exp_col}
            },
            "move": {
                "row": stream,
                "col": to_state
            }
        }
        if set_fields:
            entry["set"] = set_fields
        batch_members.append(entry)

        delta_members.append({
            "id": cid,
            "before_place": f"{stream}:{from_col}",
            "after_place": f"{stream}:{to_state}",
            "before_rev": state["revision"],
            "after_rev": new_rev
        })

    # Pass expected_table_rev directly so server FCALL exercises runtime revision rejection
    current_table_rev = get_table_revision(table)
    exp_table_rev_wire = str(expected_table_rev) if expected_table_rev is not None else str(current_table_rev)

    table_manifest = {
        "schema": 1,
        "table": table,
        "epoch": epoch,
        "expected_table_revision": exp_table_rev_wire,
        "operation_id": op_id,
        "members": batch_members
    }

    code, out, err = run_nova_table_batch(table_manifest)
    if code != 0:
        sys.stderr.write(f"RUNTIME REFUSED {op_id} server refusal: {err}\n")
        return code

    rcpt = decode_and_verify_receipt(table, op_id, epoch, out)
    delta = rcpt["batch_delta"]
    print(f"RECEIPT {op_id} event={rcpt['event']} epoch={rcpt['epoch']} before={rcpt['before']} after={rcpt['after']} outcome={rcpt['outcome']} delta={json.dumps(delta)}")
    return 0

def cmd_evidence(args):
    table = TABLE_DEFAULT
    evidence_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--evidence" and i + 1 < len(args):
            evidence_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1
    if not evidence_file:
        sys.stderr.write("refused: card evidence wants --evidence <file> --table <table>\n")
        return 2

    with open(evidence_file) as f:
        manifest = json.load(f)

    op_id = manifest.get("operation_id", "op-evidence")
    epoch = str(manifest.get("epoch", "0"))
    evidence_list = manifest.get("evidence", [])

    recorded = []
    for ev in evidence_list:
        cid = ev.get("card_id")
        head = ev.get("head")
        digest = ev.get("digest")
        reader = ev.get("reader")
        disposition = ev.get("disposition")
        ci_status = ev.get("ci_status")

        if not reader:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=missing_reader expected=reader_id observed=none changed=no\n")
            return 1

        mem = redis_hgetall(f"card:{cid}")
        if not mem or not mem.get(f"place:{table}"):
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=card_not_in_table expected=placed observed=none changed=no\n")
            return 1

        actual_digest = mem.get("digest")
        if not digest or digest != actual_digest:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=digest_mismatch expected={actual_digest} observed={digest} changed=no remedy=re-read card definition\n")
            return 1

        if not head:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=missing_head expected=exact_commit_sha observed=none changed=no\n")
            return 1

        # Bind reader evidence to exact head commit
        git_check = subprocess.run(["git", "rev-parse", "--verify", f"{head}^{{commit}}"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if git_check.returncode != 0:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=head_commit_not_found expected=exact_reachable_commit observed={head} changed=no\n")
            return 1
        exact_head = git_check.stdout.strip()

        if disposition not in ("accepted", "approved"):
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=invalid_disposition expected=accepted observed={disposition} changed=no\n")
            return 1

        if ci_status not in ("pass", "passed", "green"):
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={cid} cause=ci_not_passed expected=pass observed={ci_status} changed=no\n")
            return 1

        # Durable reader key: distinct per reader so entries NEVER overwrite each other
        ev_key = f"evidence:{cid}:{op_id}:{reader}"
        ev_data = {
            "card_id": cid,
            "operation_id": op_id,
            "head": exact_head,
            "digest": actual_digest,
            "reader": reader,
            "disposition": disposition,
            "ci_status": ci_status,
            "epoch": epoch
        }
        redis_hset(ev_key, ev_data)
        redis_cmd("SADD", f"evidence:{cid}:readers", reader)
        redis_cmd("SADD", f"evidence:{cid}:{op_id}:readers", reader)
        redis_hset(f"card:{cid}", {"head": exact_head})
        recorded.append(ev_key)

    current_table_rev = get_table_revision(table)
    delta = {
        "operation_id": op_id,
        "recorded_count": len(recorded),
        "evidence_keys": recorded
    }
    print(f"RECEIPT {op_id} epoch={epoch} before={current_table_rev} after={current_table_rev} outcome=recorded delta={json.dumps(delta)}")
    return 0

def cmd_replace(args):
    table = TABLE_DEFAULT
    replacements_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--replacements" and i + 1 < len(args):
            replacements_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1
    if not replacements_file:
        sys.stderr.write("refused: card replace wants --replacements <file> --table <table>\n")
        return 2

    with open(replacements_file) as f:
        manifest = json.load(f)

    op_id = manifest.get("operation_id", "op-replace")
    epoch = str(manifest.get("epoch", "0"))
    pairs = manifest.get("replacements", [])

    before_table_rev = get_table_revision(table)

    # Pre-state check for all pairs
    for p in pairs:
        old_id = p.get("old_id")
        new_id = p.get("new_id")
        old_mem = redis_hgetall(f"card:{old_id}")
        old_place = old_mem.get(f"place:{table}", "")
        if "working" in old_place:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={old_id} cause=working_card_cannot_be_replaced expected=non_working observed={old_place} changed=no remedy=wait or cancel working card first\n")
            return 1
        if "done" in old_place or "landed" in old_place:
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={old_id} cause=terminal_card_cannot_be_replaced expected=non_terminal observed={old_place} changed=no remedy=admit new card directly\n")
            return 1

        new_mem = redis_hgetall(f"card:{new_id}")
        if new_mem.get(f"place:{table}"):
            sys.stderr.write(f"PREFLIGHT REFUSED {op_id} card={new_id} cause=already_admitted expected=absent observed={new_mem.get(f'place:{table}')} changed=no remedy=pick fresh successor id\n")
            return 1

    batch_members = []
    delta_members = []
    for p in pairs:
        old_id = p.get("old_id")
        new_id = p.get("new_id")
        new_file = p.get("new_file")
        stream = p.get("stream", "stream-1")
        old_mem = redis_hgetall(f"card:{old_id}")
        old_place = old_mem.get(f"place:{table}", "")
        from_col = old_place.split(":")[-1]
        old_rev = old_mem.get("revision", "1")
        new_rev = str(int(old_rev) + 1)

        # 1. Terminate old card as done/replaced
        batch_members.append({
            "id": old_id,
            "expect": {
                "revision": str(old_rev),
                "place": {"row": stream, "col": from_col}
            },
            "move": {
                "row": stream,
                "col": "done"
            },
            "set": {
                "outcome": "replaced",
                "successor": new_id
            }
        })
        delta_members.append({
            "id": old_id,
            "before_place": f"{stream}:{from_col}",
            "after_place": f"{stream}:done",
            "outcome": "replaced",
            "successor": new_id
        })

        # 2. Admit new card in waiting
        _, headers, digest = parse_card_file(new_file)
        batch_members.append({
            "id": new_id,
            "expect": {"absent": True},
            "create": {
                "row": stream,
                "col": "waiting",
                "score": 1000.0
            },
            "set": {
                "id": new_id,
                "stream": stream,
                "title": headers.get("TITLE", ""),
                "kind": headers.get("KIND", "fix-red"),
                "paths": headers.get("PATHS", "none"),
                "depends_on": headers.get("DEPENDS-ON", "-"),
                "tier": headers.get("TIER", "flash"),
                "replaces": old_id,
                "definition": new_file,
                "digest": digest
            }
        })
        delta_members.append({
            "id": new_id,
            "before_place": "",
            "after_place": f"{stream}:waiting",
            "before_rev": "0",
            "after_rev": "1",
            "replaces": old_id
        })

    table_manifest = {
        "schema": 1,
        "table": table,
        "epoch": epoch,
        "expected_table_revision": str(before_table_rev),
        "operation_id": op_id,
        "members": batch_members
    }

    code, out, err = run_nova_table_batch(table_manifest)
    if code != 0:
        sys.stderr.write(f"RUNTIME REFUSED {op_id} server refusal: {err}\n")
        return code

    rcpt = decode_and_verify_receipt(table, op_id, epoch, out)
    delta = rcpt["batch_delta"]
    print(f"RECEIPT {op_id} event={rcpt['event']} epoch={rcpt['epoch']} before={rcpt['before']} after={rcpt['after']} outcome={rcpt['outcome']} delta={json.dumps(delta)}")
    return 0

def cmd_inspect(args):
    table = TABLE_DEFAULT
    scope_file = None
    i = 0
    while i < len(args):
        if args[i] == "--table" and i + 1 < len(args):
            table = args[i+1]
            i += 2
        elif args[i] == "--scope" and i + 1 < len(args):
            scope_file = args[i+1]
            i += 2
        elif args[i] == "--redis" and i + 1 < len(args):
            i += 2
        else:
            i += 1

    members = []
    if scope_file and os.path.exists(scope_file):
        with open(scope_file) as f:
            data = json.load(f)
            members = data.get("members", [])
    
    table_rev = get_table_revision(table)
    print(f"TABLE INSPECT table={table} revision={table_rev}")
    if not members:
        keys_list = redis_cmd("KEYS", "card:*")
        if isinstance(keys_list, list):
            for k in keys_list:
                if k.startswith("card:") and ":" not in k[5:]:
                    members.append(k[5:])

    for cid in sorted(members):
        mem = redis_hgetall(f"card:{cid}")
        place = mem.get(f"place:{table}", "unplaced")
        rev = mem.get("revision", "0")
        kind = mem.get("kind", "")
        deps = mem.get("depends_on", "-")
        outcome = mem.get("outcome", "")
        succ = mem.get("successor", "")
        out_str = f"CARD id={cid:14s} place={place:18s} rev={rev:2s} kind={kind:8s} deps={deps}"
        if outcome:
            out_str += f" outcome={outcome}"
        if succ:
            out_str += f" successor={succ}"
        print(out_str)
    return 0

def cmd_check(args):
    table = args[0] if args else TABLE_DEFAULT
    code, stdout, stderr = nova_table_cmd("check", table)
    if code != 0:
        sys.stderr.write(f"{stderr}\n")
        return code
    print(stdout)
    return 0

def cmd_help(args):
    print("""nova-sprint card manager — batch operations over arrays of cards

usage:
  card add --admissions <file> --table <table> [--redis <addr>]
  card resolve --scope <scope-file> --table <table> [--redis <addr>]
  card move --events <file> --table <table> [--redis <addr>]
  card evidence --evidence <file> --table <table> [--redis <addr>]
  card replace --replacements <file> --table <table> [--redis <addr>]
  card inspect [--scope <scope-file>] --table <table> [--redis <addr>]
  check <table> [--redis <addr>]
  help [<verb>]
  version

exit codes: 0 done, 1 refused, 2 usage
""")
    return 0

def main():
    raw_args = sys.argv[1:]
    if not raw_args:
        return cmd_help([])

    verb = None
    args = []
    i = 0
    while i < len(raw_args):
        if raw_args[i] == "--redis" and i + 1 < len(raw_args):
            global REDIS_ADDR
            REDIS_ADDR = raw_args[i+1]
            i += 2
        elif verb is None and not raw_args[i].startswith("-"):
            verb = raw_args[i]
            i += 1
        else:
            args.append(raw_args[i])
            i += 1

    if not verb:
        if "--help" in raw_args or "-h" in raw_args or "help" in raw_args:
            return cmd_help([])
        if "--version" in raw_args or "version" in raw_args:
            print("nova-sprint card manager v1.0.0-draft (ideas#825)")
            return 0
        return cmd_help([])

    if verb == "help":
        return cmd_help(args)
    elif verb == "version":
        print("nova-sprint card manager v1.0.0-draft (ideas#825)")
        return 0

    # For all operational verbs, ensure Redis address is resolved
    get_redis_addr()

    if verb == "add":
        return cmd_add(args)
    elif verb == "resolve":
        return cmd_resolve(args)
    elif verb == "move":
        return cmd_move(args)
    elif verb == "evidence":
        return cmd_evidence(args)
    elif verb == "replace":
        return cmd_replace(args)
    elif verb == "inspect":
        return cmd_inspect(args)
    elif verb == "check":
        return cmd_check(args)
    else:
        sys.stderr.write(f"unknown verb: {verb}\n")
        return 2

if __name__ == "__main__":
    sys.exit(main())
