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

REDIS_ADDR = os.environ.get("NOVA_SPRINT_REDIS") or os.environ.get("NOVA_REDIS_ADDR") or "127.0.0.1:6379"
TABLE_DEFAULT = "cards"

def get_nova_table_bin():
    nova_table = os.environ.get("NOVA_TABLE_BIN")
    if not nova_table:
        script_dir = os.path.dirname(os.path.realpath(__file__))
        repo_root = os.path.dirname(script_dir)
        candidate = os.path.join(repo_root, "bin", "nova-table")
        if os.path.exists(candidate) and os.access(candidate, os.X_OK):
            nova_table = candidate
        else:
            nova_table = "nova-table"
    return nova_table

def nova_table_cmd(*args):
    cmd = [get_nova_table_bin()] + list(args) + ["--redis", REDIS_ADDR]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    return p.returncode, p.stdout.strip(), p.stderr.strip()

def run_nova_table_batch(manifest):
    bin_path = get_nova_table_bin()
    for m in manifest.get("members", []):
        for k in list(m.keys()):
            if m[k] is None:
                del m[k]
    cmd = [bin_path, "batch", "-", "--redis", REDIS_ADDR]
    p = subprocess.run(
        cmd,
        input=json.dumps(manifest),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    return p.returncode, p.stdout.strip(), p.stderr.strip()

def redis_cmd(*args):
    host = REDIS_ADDR.split(":")[0]
    port = int(REDIS_ADDR.split(":")[1])
    s = socket.socket()
    s.connect((host, port))
    parts = ["*" + str(len(args)) + "\r\n"]
    for a in args:
        encoded = str(a).encode("utf-8")
        parts.append("$" + str(len(encoded)) + "\r\n" + str(a) + "\r\n")
    s.sendall("".join(parts).encode("utf-8"))
    
    data = b""
    while True:
        chunk = s.recv(4096)
        if not chunk:
            break
        data += chunk
        if len(chunk) < 4096:
            break
    s.close()
    return data

def redis_hgetall(key):
    raw = redis_cmd("HGETALL", key)
    lines = raw.split(b"\r\n")
    if not lines or not lines[0].startswith(b"*"):
        return {}
    num_elements = int(lines[0][1:])
    result = {}
    idx = 1
    for _ in range(num_elements // 2):
        if idx >= len(lines) or not lines[idx].startswith(b"$"):
            break
        k = lines[idx+1].decode("utf-8", errors="replace")
        idx += 2
        if idx >= len(lines) or not lines[idx].startswith(b"$"):
            break
        v = lines[idx+1].decode("utf-8", errors="replace")
        idx += 2
        result[k] = v
    return result

def redis_hset(key, mapping):
    args = ["HSET", key]
    for k, v in mapping.items():
        args.extend([str(k), str(v)])
    return redis_cmd(*args)

def redis_del(key):
    return redis_cmd("DEL", key)

def get_table_revision(table):
    raw = redis_cmd("HGET", f"table:{table}:revision", "n")
    lines = raw.split(b"\r\n")
    if len(lines) > 1 and lines[0].startswith(b"$") and lines[0] != b"$-1":
        return int(lines[1].decode("utf-8"))
    return 0

def inc_table_revision(table):
    raw = redis_cmd("HINCRBY", f"table:{table}:revision", "n", "1")
    lines = raw.split(b"\r\n")
    if len(lines) > 1 and lines[0].startswith(b":"):
        return int(lines[0][1:])
    return get_table_revision(table) + 1

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
        else:
            i += 1
    if not admissions_file:
        sys.stderr.write("refused: card add wants --admissions <file> --table <table>\n")
        return 2

    with open(admissions_file) as f:
        manifest = json.load(f)

    op_id = manifest.get("operation_id", "op-add")
    epoch = str(manifest.get("epoch", "0"))
    expected_rev = str(manifest.get("expected_table_revision", "0"))
    admissions = manifest.get("admissions", [])

    # Check for duplicate card IDs in admissions manifest
    seen = set()
    for adm in admissions:
        cid = adm.get("id")
        if cid in seen:
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=duplicate_card_in_manifest expected=unique observed={cid} changed=no remedy=remove duplicate from {admissions_file}\n")
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
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=already_admitted expected=absent observed={mem.get(f'place:{table}')} changed=no remedy=card inspect --table {table}\n")
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
        sys.stderr.write(f"{err}\n")
        return code

    after_table_rev = get_table_revision(table)
    delta = {
        "operation_id": op_id,
        "changed_count": len(delta_members),
        "guard_count": 0,
        "members": delta_members
    }
    print(f"RECEIPT {op_id} epoch={epoch} before={before_table_rev} after={after_table_rev} outcome=changed delta={json.dumps(delta)}")
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
            dep_kind = dep_mem.get("kind", "")
            dep_outcome = dep_mem.get("outcome", "")
            # Dependency is met when landed, or done/completed for non-PR kinds
            is_landed = "landed" in dep_place
            is_done_non_pr = ("done" in dep_place) and (dep_outcome == "completed") and (dep_kind in ["read", "probe", "text", "tone", "report"])
            if not (is_landed or is_done_non_pr):
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
            sys.stderr.write(f"{err}\n")
            return code

    after_table_rev = get_table_revision(table)
    outcome = "changed" if delta_members else "unchanged"
    # Clean stream from delta_members for receipt output
    cleaned_deltas = []
    for m in delta_members:
        cleaned_deltas.append({
            "id": m["id"],
            "before_place": m["before_place"],
            "after_place": m["after_place"],
            "before_rev": m["before_rev"],
            "after_rev": m["after_rev"]
        })

    delta = {
        "operation_id": op_id,
        "changed_count": len(cleaned_deltas),
        "blocked_count": len(blocked_members),
        "members": cleaned_deltas,
        "blocked": blocked_members
    }
    print(f"RECEIPT {op_id} epoch={epoch} before={before_table_rev} after={after_table_rev} outcome={outcome} delta={json.dumps(delta)}")
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

    # Check stale table revision if specified
    current_table_rev = get_table_revision(table)
    if expected_table_rev is not None and str(expected_table_rev) != str(current_table_rev):
        sys.stderr.write(f"REFUSED {op_id} cause=stale_table_revision expected={expected_table_rev} observed={current_table_rev} changed=no remedy=card inspect --table {table}\n")
        return 1

    # Check for duplicate events for one card in same batch
    seen_cards = set()
    for ev in events:
        cid = ev.get("id")
        if cid in seen_cards:
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=duplicate_card_events_in_batch expected=one_event_per_card observed=multiple changed=no remedy=split into sequential batches\n")
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
            "kind": mem.get("kind", "")
        }
        
        # Check expected revision guard
        exp_rev = ev.get("expect_revision")
        if exp_rev is not None and str(exp_rev) != str(pre_rev):
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=stale_member_revision expected={exp_rev} observed={pre_rev} changed=no remedy=card inspect --table {table}\n")
            return 1

        # Check expected place guard
        exp_place = ev.get("expect_place")
        if exp_place is not None and exp_place not in place:
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=unexpected_place expected={exp_place} observed={place} changed=no remedy=card inspect --table {table}\n")
            return 1

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

        set_fields = {}
        if "outcome" in ev:
            set_fields["outcome"] = ev["outcome"]
        if "landing_sha" in ev:
            set_fields["landing_sha"] = ev["landing_sha"]

        entry = {
            "id": cid,
            "expect": {
                "revision": state["revision"],
                "place": {"row": stream, "col": from_col}
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

    table_manifest = {
        "schema": 1,
        "table": table,
        "epoch": epoch,
        "expected_table_revision": str(current_table_rev),
        "operation_id": op_id,
        "members": batch_members
    }

    code, out, err = run_nova_table_batch(table_manifest)
    if code != 0:
        sys.stderr.write(f"{err}\n")
        return code

    after_table_rev = get_table_revision(table)
    delta = {
        "operation_id": op_id,
        "changed_count": len(delta_members),
        "members": delta_members
    }
    print(f"RECEIPT {op_id} epoch={epoch} before={current_table_rev} after={after_table_rev} outcome=changed delta={json.dumps(delta)}")
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

        mem = redis_hgetall(f"card:{cid}")
        actual_digest = mem.get("digest")
        if digest and digest != actual_digest:
            sys.stderr.write(f"REFUSED {op_id} card={cid} cause=digest_mismatch expected={actual_digest} observed={digest} changed=no remedy=re-read card definition\n")
            return 1

        ev_key = f"evidence:{cid}:{op_id}"
        ev_data = {
            "card_id": cid,
            "head": head,
            "reader": reader,
            "disposition": disposition,
            "ci_status": ci_status or "pass",
            "epoch": epoch
        }
        redis_hset(ev_key, ev_data)
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
        new_file = p.get("new_file")
        stream = p.get("stream", "stream-1")

        old_mem = redis_hgetall(f"card:{old_id}")
        old_place = old_mem.get(f"place:{table}", "")
        if "working" in old_place:
            sys.stderr.write(f"REFUSED {op_id} card={old_id} cause=working_card_cannot_be_replaced expected=non_working observed={old_place} changed=no remedy=wait or cancel working card first\n")
            return 1
        if "done" in old_place or "landed" in old_place:
            sys.stderr.write(f"REFUSED {op_id} card={old_id} cause=terminal_card_cannot_be_replaced expected=non_terminal observed={old_place} changed=no remedy=admit new card directly\n")
            return 1

        new_mem = redis_hgetall(f"card:{new_id}")
        if new_mem.get(f"place:{table}"):
            sys.stderr.write(f"REFUSED {op_id} card={new_id} cause=already_admitted expected=absent observed={new_mem.get(f'place:{table}')} changed=no remedy=pick fresh successor id\n")
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
        sys.stderr.write(f"{err}\n")
        return code

    after_table_rev = get_table_revision(table)
    delta = {
        "operation_id": op_id,
        "changed_count": len(delta_members),
        "members": delta_members
    }
    print(f"RECEIPT {op_id} epoch={epoch} before={before_table_rev} after={after_table_rev} outcome=changed delta={json.dumps(delta)}")
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
        raw_keys = redis_cmd("KEYS", "card:*")
        lines = raw_keys.split(b"\r\n")
        for line in lines:
            if line.startswith(b"card:") and b":" not in line[5:]:
                members.append(line[5:].decode("utf-8"))

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
  card add --admissions <file> --table <table>
  card resolve --scope <scope-file> --table <table>
  card move --events <file> --table <table>
  card evidence --evidence <file> --table <table>
  card replace --replacements <file> --table <table>
  card inspect [--scope <scope-file>] --table <table>
  check <table>
  help [<verb>]
  version

exit codes: 0 done, 1 refused, 2 usage
""")
    return 0

def main():
    if len(sys.argv) < 2:
        return cmd_help([])
    verb = sys.argv[1]
    args = sys.argv[2:]

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
    elif verb == "help":
        return cmd_help(args)
    elif verb == "version":
        print("nova-sprint card manager v1.0.0-draft (ideas#825)")
        return 0
    else:
        sys.stderr.write(f"unknown verb: {verb}\n")
        return 2

if __name__ == "__main__":
    sys.exit(main())
