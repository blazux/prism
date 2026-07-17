#!/usr/bin/env python3
# TOOL: {"name":"pcap","description":"Decode a pcap/pcapng capture with tshark. Reads a file already on disk (a chat attachment lands in uploads/).","when_to_use":"Any question about a network capture: which SIP calls are in it, whether a header like Diversion is present, what a device sent at a given moment, RTP quality.","usage":"mode=calls (default) lists the SIP calls, one line per message \u2014 start here. mode=messages prints whole SIP messages, start line + headers + SDP body: use it to read an INVITE. It is verbose, so pass call_id=\"...\" (from mode=calls) to follow one call, offset to page through, or only=\"From,To,Diversion\" to keep just those headers across every message. mode=filter runs a raw tshark display filter (filter=\"sip.Diversion\", or with fields=\"ip.src,sip.r-uri\") \u2014 the cheapest way to answer \"is header X present\". mode=timeline is one line per packet, all protocols.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"Path to the capture, absolute or relative to /workspace (e.g. uploads/call.pcap)"},"mode":{"type":"string","description":"calls (default) | messages | timeline | filter"},"filter":{"type":"string","description":"tshark display filter, required for mode=filter"},"fields":{"type":"string","description":"Comma-separated tshark field names to print (mode=filter only)"},"limit":{"type":"integer","description":"Max lines to print (default 100); in mode=messages, max messages (default 5)"},"call_id":{"type":"string","description":"mode=messages: show only this SIP Call-ID"},"only":{"type":"string","description":"mode=messages: comma-separated header names to keep (drops the body)"},"offset":{"type":"integer","description":"mode=messages: skip this many messages (paging)"}},"required":["path"]}}
"""Decode a capture for the agent.

Derived from the WiresharkConvert service: same tshark invocations, no HTTP.
The point of the modes is to never hand the model a whole capture — a timeline
of 29 000 packets buries the answer it was looking for. `calls` and `filter`
answer a question; `messages` reads the SIP verbatim once you know which call
you care about; `timeline` is the fallback when you don't know what you are
looking for yet.
"""

import json
import os
import subprocess
import sys
from datetime import datetime

WORKSPACE = "/workspace"
DEFAULT_LIMIT = 100


def fail(msg):
    print(f"ERROR: {msg}")
    sys.exit(0)  # the executor shows stdout; a non-zero exit would hide it


def resolve(path):
    """Capture paths come from the model, so keep them inside the workspace."""
    p = path if os.path.isabs(path) else os.path.join(WORKSPACE, path)
    p = os.path.realpath(p)
    if p != WORKSPACE and not p.startswith(WORKSPACE + os.sep):
        fail(f"path must stay under {WORKSPACE}")
    if not os.path.isfile(p):
        fail(f"no such file: {p}")
    return p


def tshark(args, timeout=120):
    try:
        r = subprocess.run(["tshark"] + args, capture_output=True, text=True, timeout=timeout)
    except FileNotFoundError:
        fail("tshark is not installed in this container (add it to /workspace/.apt-packages)")
    except subprocess.TimeoutExpired:
        fail("tshark timed out — narrow the filter or the capture")
    if r.returncode != 0:
        fail(f"tshark: {r.stderr.strip()[:400]}")
    return r.stdout


def rows(path, display_filter, fields, aggregator="|"):
    """tshark -T fields, as a list of dicts (empty values dropped)."""
    cmd = ["-r", path]
    if display_filter:
        cmd += ["-Y", display_filter]
    cmd += ["-T", "fields", "-E", "header=y", "-E", "separator=\t"]
    if aggregator:
        # tshark rejects an empty aggregator= pair, so only set it when joining
        # multi-valued fields (SDP attributes). Whole SIP messages need none.
        cmd += ["-E", f"aggregator={aggregator}"]
    for f in fields:
        cmd += ["-e", f]
    lines = [l for l in tshark(cmd).strip().splitlines() if l.strip()]
    if len(lines) < 2:
        return []
    headers = lines[0].split("\t")
    out = []
    for line in lines[1:]:
        row = {h: v for h, v in zip(headers, line.split("\t")) if v}
        if row:
            out.append(row)
    return out


def clock(epoch):
    try:
        return datetime.fromtimestamp(float(epoch)).strftime("%H:%M:%S.%f")[:-3]
    except (TypeError, ValueError):
        return ""


def emit(lines, limit):
    """Print at most `limit` lines and say what was left out."""
    for l in lines[:limit]:
        print(l)
    if len(lines) > limit:
        print(f"\n[{len(lines) - limit} more lines. Raise `limit` or narrow the filter.]")


# ─── modes ───────────────────────────────────────────────────────────────────

SIP_FIELDS = [
    "frame.time_epoch", "ip.src", "ip.dst",
    "udp.srcport", "udp.dstport", "tcp.srcport", "tcp.dstport",
    "sip.Method", "sip.Status-Line", "sip.r-uri",
    "sip.from.addr", "sip.to.addr", "sip.P-Asserted-Identity",
    "sip.Call-ID", "sdp.media_attr",
]


def codecs_of(attr):
    """SDP a= lines, aggregated by tshark with '|'. Keep the rtpmap names."""
    names = []
    for a in attr.split("|"):
        a = a.strip()
        if a.startswith("rtpmap:"):
            parts = a.split(" ", 1)
            if len(parts) == 2 and parts[1] not in names:
                names.append(parts[1])
    return names


MSG_FIELDS = [
    "frame.number", "frame.time_epoch", "ip.src", "ip.dst",
    "udp.srcport", "udp.dstport", "tcp.srcport", "tcp.dstport",
    "sip.Request-Line", "sip.Status-Line", "sip.msg_hdr", "sip.msg_body",
]


def mode_messages(path, display_filter, call_id, only, offset, limit):
    """Print whole SIP messages, start line + headers + body, one after another.

    tshark escapes CRLF as the two literal characters \\r \\n inside -T fields,
    and hands the body back as hex — so the message has to be reassembled here.
    Verbose by design: this is what you read when a summary is not enough. Five
    messages at a time, because one INVITE is thirty lines.
    """
    filt = display_filter or "sip"
    if call_id:
        filt = f'sip.Call-ID == "{call_id}"'
    wanted = [h.strip().lower() for h in only.split(",") if h.strip()] if only else []

    msgs = rows(path, filt, MSG_FIELDS, aggregator="")
    if not msgs:
        print(f"0 SIP messages match `{filt}`.")
        return

    total = len(msgs)
    window = msgs[offset:offset + limit]
    print(f"{total} SIP message(s) match `{filt}` — showing {offset + 1}..{offset + len(window)}\n")

    for r in window:
        sport = r.get("udp.srcport") or r.get("tcp.srcport", "")
        dport = r.get("udp.dstport") or r.get("tcp.dstport", "")
        print(f"── #{r.get('frame.number','?')}  {clock(r.get('frame.time_epoch'))}  "
              f"{r.get('ip.src','')}:{sport} -> {r.get('ip.dst','')}:{dport}")
        print(r.get("sip.Request-Line") or r.get("sip.Status-Line", ""))

        for line in r.get("sip.msg_hdr", "").replace("\\r\\n", "\n").split("\n"):
            line = line.rstrip()
            if not line:
                continue
            if wanted and line.split(":", 1)[0].strip().lower() not in wanted:
                continue
            print(line)

        body = r.get("sip.msg_body", "")
        if body and not wanted:
            try:
                print()
                print(bytes.fromhex(body).decode("utf-8", "replace").strip())
            except ValueError:
                pass
        print()

    left = total - offset - len(window)
    if left > 0:
        print(f"[{left} more message(s). Use offset={offset + len(window)}, "
              f"or `only` to keep just some headers, or `call_id` to focus one call.]")


def mode_calls(path, limit):
    calls = {}
    for r in rows(path, "sip", SIP_FIELDS):
        cid = r.get("sip.Call-ID", "unknown")
        c = calls.setdefault(cid, {"from": "", "to": "", "pai": "", "codecs": [], "msgs": []})
        for key, field in (("from", "sip.from.addr"), ("to", "sip.to.addr"), ("pai", "sip.P-Asserted-Identity")):
            if not c[key] and r.get(field):
                c[key] = r[field]
        for name in codecs_of(r.get("sdp.media_attr", "")):
            if name not in c["codecs"]:
                c["codecs"].append(name)
        sport = r.get("udp.srcport") or r.get("tcp.srcport", "")
        dport = r.get("udp.dstport") or r.get("tcp.dstport", "")
        kind = r.get("sip.Method") or r.get("sip.Status-Line", "?")
        uri = r.get("sip.r-uri", "")
        c["msgs"].append(
            f"    {clock(r.get('frame.time_epoch'))}  {r.get('ip.src','')}:{sport} -> "
            f"{r.get('ip.dst','')}:{dport}  {kind}{' ' + uri if uri else ''}"
        )

    if not calls:
        print("No SIP in this capture. Try mode=timeline, or mode=filter with a display filter.")
        return

    out = []
    for cid, c in calls.items():
        out.append(f"Call-ID: {cid}")
        out.append(f"  from: {c['from']}   to: {c['to']}")
        if c["pai"]:
            out.append(f"  P-Asserted-Identity: {c['pai']}")
        if c["codecs"]:
            out.append(f"  codecs: {', '.join(c['codecs'])}")
        out.append(f"  {len(c['msgs'])} messages:")
        out += c["msgs"]
        out.append("")
    print(f"{len(calls)} SIP call(s) in {os.path.basename(path)}\n")
    emit(out, limit)


def mode_timeline(path, limit):
    lines = [l for l in tshark(["-r", path]).splitlines() if l.strip()]
    print(f"{len(lines)} packets in {os.path.basename(path)}\n")
    emit(lines, limit)


def mode_filter(path, display_filter, fields, limit):
    if not display_filter:
        fail("mode=filter needs a `filter` (e.g. 'sip.Diversion' or 'sip.Method == \"INVITE\"')")
    if fields:
        names = [f.strip() for f in fields.split(",") if f.strip()]
        lines = []
        for r in rows(path, display_filter, names):
            lines.append("  ".join(f"{n}={r[n]}" for n in names if n in r))
    else:
        lines = [l for l in tshark(["-r", path, "-Y", display_filter]).splitlines() if l.strip()]
    if not lines:
        print(f"0 packets match `{display_filter}`.")
        print("For a header, that means it is absent from this capture.")
        return
    print(f"{len(lines)} packet(s) match `{display_filter}`\n")
    emit(lines, limit)


def main():
    try:
        args = json.loads(sys.argv[1]) if len(sys.argv) > 1 and sys.argv[1].strip() else {}
    except json.JSONDecodeError as e:
        fail(f"bad arguments: {e}")

    path = args.get("path")
    if not path:
        fail("`path` is required")
    path = resolve(path)

    limit = args.get("limit") or DEFAULT_LIMIT
    try:
        limit = max(1, int(limit))
    except (TypeError, ValueError):
        limit = DEFAULT_LIMIT

    try:
        offset = max(0, int(args.get("offset") or 0))
    except (TypeError, ValueError):
        offset = 0

    mode = (args.get("mode") or "calls").lower()
    if mode == "calls":
        mode_calls(path, limit)
    elif mode == "timeline":
        mode_timeline(path, limit)
    elif mode == "filter":
        mode_filter(path, args.get("filter", ""), args.get("fields", ""), limit)
    elif mode == "messages":
        # Whole messages are long, so the caller's `limit` (tuned for lines)
        # would dump the entire capture. Five is what fits in one answer.
        if not args.get("limit"):
            limit = 5
        mode_messages(path, args.get("filter", ""), args.get("call_id", ""),
                      args.get("only", ""), offset, limit)
    else:
        fail(f"unknown mode {mode!r}: use calls, messages, timeline or filter")


if __name__ == "__main__":
    main()
