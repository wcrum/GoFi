#!/usr/bin/env python3
"""Compare two raw `go test -bench` outputs and fail on allocs/op regressions.

    scripts/bench/compare.py --max-regress 20 old.txt new.txt

For every benchmark present in both files the median allocs/op (and ns/op,
B/op for the table) is compared; a benchmark whose new median allocs/op is
more than --max-regress percent above the old one fails the gate. Prints a
Markdown table.
"""
import argparse
import re
import statistics
import sys

LINE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(.*)$")


def parse(path):
    out = {}
    with open(path) as fh:
        for line in fh:
            m = LINE.match(line.strip())
            if not m:
                continue
            name, rest = m.group(1), m.group(2)
            fields = rest.split()
            metrics = {}
            for value, unit in zip(fields[0::2], fields[1::2]):
                try:
                    metrics[unit] = float(value)
                except ValueError:
                    pass
            for unit, value in metrics.items():
                out.setdefault(name, {}).setdefault(unit, []).append(value)
    return {name: {unit: statistics.median(vals) for unit, vals in units.items()} for name, units in out.items()}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--max-regress", type=float, default=20.0, help="allowed allocs/op increase in percent")
    ap.add_argument("old")
    ap.add_argument("new")
    args = ap.parse_args()
    old, new = parse(args.old), parse(args.new)
    rows, failures = [], []
    for name in sorted(set(old) & set(new)):
        o, n = old[name], new[name]
        delta = {}
        for unit in ("ns/op", "B/op", "allocs/op"):
            if unit in o and unit in n:
                base = o[unit]
                delta[unit] = (n[unit] - base) / base * 100 if base else (0.0 if n[unit] == 0 else float("inf"))
        rows.append((name, o, n, delta))
        d = delta.get("allocs/op")
        if d is not None and d > args.max_regress and n["allocs/op"] - o["allocs/op"] >= 1:
            failures.append((name, o["allocs/op"], n["allocs/op"], d))
    print("| benchmark | ns/op old → new | B/op old → new | allocs/op old → new | allocs Δ |")
    print("| --- | --- | --- | --- | --- |")
    for name, o, n, delta in rows:
        def fmt(unit):
            if unit not in o or unit not in n:
                return "—"
            return f"{o[unit]:.4g} → {n[unit]:.4g}"
        d = delta.get("allocs/op")
        print(f"| {name} | {fmt('ns/op')} | {fmt('B/op')} | {fmt('allocs/op')} | {'' if d is None else f'{d:+.1f}%'} |")
    if not rows:
        print("\nno common benchmarks between the two runs")
    if failures:
        print(f"\nFAIL: {len(failures)} benchmark(s) regressed allocs/op by more than {args.max_regress:.0f}%:")
        for name, a, b, d in failures:
            print(f"  {name}: {a:.0f} → {b:.0f} allocs/op ({d:+.1f}%)")
        sys.exit(1)
    print(f"\nOK: no allocs/op regression above {args.max_regress:.0f}% in {len(rows)} benchmark(s)")


if __name__ == "__main__":
    main()
