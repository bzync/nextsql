#!/usr/bin/env python3
"""Search normalized Philippine education-policy seed records."""
import argparse
import json
from pathlib import Path

DATA = Path(__file__).resolve().parents[1] / "data" / "policies.jsonl"

def load():
    return [json.loads(x) for x in DATA.read_text(encoding="utf-8").splitlines() if x.strip()]

def main():
    ap = argparse.ArgumentParser(description="Search normalized Philippine education policy seed records")
    ap.add_argument('--agency')
    ap.add_argument('--topic')
    ap.add_argument('--text')
    ap.add_argument('--year', type=int)
    ap.add_argument('--status')
    args = ap.parse_args()
    rows = load()
    out = []
    for r in rows:
        if args.agency and r.get('agency','').upper() != args.agency.upper(): continue
        if args.topic and args.topic.lower() not in [t.lower() for t in r.get('topics',[])]: continue
        if args.year and r.get('series_year') != args.year: continue
        if args.status and r.get('status', '').upper() != args.status.upper(): continue
        if args.text:
            hay = json.dumps(r, ensure_ascii=False).lower()
            if args.text.lower() not in hay: continue
        out.append(r)
    out.sort(key=lambda row: (row.get('series_year') or 0, row.get('number') or 0, row.get('id') or ''))
    print(json.dumps(out, ensure_ascii=False, indent=2))

if __name__ == '__main__': main()
