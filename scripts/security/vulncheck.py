#!/usr/bin/env python3
"""Run govulncheck and fail on any vulnerability that is not formally accepted.

    scripts/security/vulncheck.py [--mode symbol|module] [--accepted FILE] [PACKAGES]

govulncheck exits non-zero whenever it finds anything, which makes it useless as
a gate while an unfixable advisory is outstanding: the build is red for a problem
nobody can act on, so the signal gets ignored. This wrapper reads the JSON
stream instead and fails only on findings that are not listed in
scripts/security/accepted.json.

An acceptance also expires. Once an entry's `review_by` date has passed the gate
fails on it again, so "we looked at this and it is fine" cannot quietly become
"nobody has looked at this since the spring".

Default --mode symbol reports only vulnerabilities govulncheck believes the code
actually calls. --mode module is the stricter sweep: everything in the module
graph, reachable or not.
"""
import argparse
import datetime
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def load_accepted(path):
    """Accepted entries by vulnerability id, and the ids whose review date passed."""
    with open(path) as fh:
        doc = json.load(fh)
    today = datetime.date.today()
    accepted, expired = {}, []
    for entry in doc.get("go", []):
        missing = [k for k in ("id", "reason", "review_by") if not entry.get(k)]
        if missing:
            sys.exit(f"{path}: entry {entry.get('id', '?')} is missing {', '.join(missing)}")
        try:
            review_by = datetime.date.fromisoformat(entry["review_by"])
        except ValueError:
            sys.exit(f"{path}: entry {entry['id']} has an unparseable review_by {entry['review_by']!r}")
        accepted[entry["id"]] = entry
        if review_by < today:
            expired.append((entry["id"], entry["review_by"]))
    return accepted, expired


def run_govulncheck(mode, packages):
    """The findings govulncheck reports, as (id, module, summary, called) tuples.

    The JSON stream describes each vulnerability with an `osv` message and then
    one `finding` per trace. A finding whose deepest frame names a function is a
    called symbol; one that stops at the module is only present in the graph.
    """
    command = ["govulncheck", "-format", "json", "-scan", "symbol", *packages]
    env = {**os.environ, "GOWORK": "off", "GOTOOLCHAIN": os.environ.get("GOTOOLCHAIN", "auto")}
    proc = subprocess.run(command, capture_output=True, text=True, env=env)
    if not proc.stdout.strip():
        sys.exit(f"govulncheck produced no output (exit {proc.returncode}):\n{proc.stderr}")

    summaries, findings = {}, {}
    decoder = json.JSONDecoder()
    text = proc.stdout.lstrip()
    while text:
        message, end = decoder.raw_decode(text)
        text = text[end:].lstrip()
        if "osv" in message:
            osv = message["osv"]
            summaries[osv["id"]] = osv.get("summary") or osv.get("details", "").split("\n")[0]
        if "finding" in message:
            found = message["finding"]
            osv_id = found["osv"]
            trace = found.get("trace") or [{}]
            called = bool(trace[0].get("function"))
            # The trace runs from the vulnerable frame outwards to the entry
            # point, so the module at fault is the first one, not the last.
            module = trace[0].get("module") or "standard library"
            previous = findings.get(osv_id)
            # Keep the strongest evidence: a called trace outranks a module-only one.
            if previous is None or (called and not previous[2]):
                findings[osv_id] = (osv_id, module, called)

    out = [(i, m, summaries.get(i, ""), c) for i, m, c in findings.values()]
    if mode == "symbol":
        out = [f for f in out if f[3]]
    return sorted(out)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--mode", choices=("symbol", "module"), default="symbol",
                        help="symbol: only vulnerabilities the code calls (default). module: the whole module graph.")
    parser.add_argument("--accepted", default=os.path.join(HERE, "accepted.json"))
    parser.add_argument("packages", nargs="*", default=["./..."])
    args = parser.parse_args()

    accepted, expired = load_accepted(args.accepted)
    findings = run_govulncheck(args.mode, args.packages or ["./..."])

    blocking = [f for f in findings if f[0] not in accepted]
    waived = [f for f in findings if f[0] in accepted]
    stale = [f for f in waived if f[0] in dict(expired)]

    for osv_id, module, summary, called in waived:
        mark = "EXPIRED" if osv_id in dict(expired) else "accepted"
        print(f"{mark}: {osv_id}  {module}  {summary}")
        print(f"          {accepted[osv_id]['reason']}")
    for osv_id, module, summary, called in blocking:
        print(f"BLOCKING: {osv_id}  {module}  {summary}  (called={called})")
        print(f"          https://pkg.go.dev/vuln/{osv_id}")

    if stale:
        print(f"\n{len(stale)} acceptance(s) past their review date: "
              + ", ".join(f"{i} (due {d})" for i, d in expired if i in {f[0] for f in stale}))
        print("Re-check the advisory, then either upgrade or move review_by forward with a fresh reason.")
    if blocking:
        print(f"\n{len(blocking)} unaccepted vulnerability/ies ({args.mode} scan). Upgrade the module, or "
              f"add an entry with a reason and a review date to {os.path.relpath(args.accepted)}.")
    if blocking or stale:
        return 1
    print(f"\nNo unaccepted vulnerabilities ({args.mode} scan; {len(waived)} accepted).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
