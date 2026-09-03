#!/usr/bin/env python3
"""Filter likely applicable policy candidates for a normalized school context."""
import argparse
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
POLICIES = ROOT / 'data' / 'policies.jsonl'
AGENCIES = ROOT / 'data' / 'agencies.json'


def school_year_arg(value):
    match = re.fullmatch(r'(\d{4})-(\d{4})', value)
    if not match or int(match.group(2)) != int(match.group(1)) + 1:
        raise argparse.ArgumentTypeError('expected a consecutive school year such as 2026-2027')
    return value


def grade_arg(value):
    grade = int(value)
    if not 0 <= grade <= 12:
        raise argparse.ArgumentTypeError('grade must be from 0 (Kindergarten) through 12')
    return grade

def sy_start(s):
    if not s: return None
    m = re.match(r'^(\d{4})', str(s))
    return int(m.group(1)) if m else None

def in_effect(row, sy):
    y = sy_start(sy); a = sy_start(row.get('effective_from')); b = sy_start(row.get('effective_until'))
    if a is None:
        a = row.get('series_year') if isinstance(row.get('series_year'), int) else sy_start(row.get('issued_at'))
    if y is None: return True
    if a is not None and y < a: return False
    if b is not None and y > b: return False
    return True

def grade_matches(row, grade):
    if grade is None: return True
    scope = row.get('scope',{})
    levels = scope.get('grade_levels')
    if levels is not None:
        return grade in levels
    key_stages = scope.get('key_stages')
    if key_stages:
        # Common DepEd K-12 key-stage mapping; Kindergarten is handled separately.
        stage = 1 if 1 <= grade <= 3 else 2 if 4 <= grade <= 6 else 3 if 7 <= grade <= 10 else 4 if 11 <= grade <= 12 else None
        return stage in key_stages
    return True


def scope_matches(row, key, requested):
    if requested is None:
        return True
    declared = row.get('scope', {}).get(key)
    if declared is None:
        return True
    normalized = requested.upper()
    return normalized in {str(value).upper() for value in declared}

def main():
    ap = argparse.ArgumentParser(description='Find likely applicable policy candidates. This is a filter, not a substitute for enclosure review.')
    agency_codes = [row['code'] for row in json.loads(AGENCIES.read_text(encoding='utf-8'))]
    ap.add_argument('--agency', required=True, type=str.upper, choices=agency_codes)
    ap.add_argument('--school-year', required=True, type=school_year_arg)
    ap.add_argument('--grade', type=grade_arg)
    ap.add_argument('--topic')
    ap.add_argument('--education-level')
    ap.add_argument('--institution-type')
    ap.add_argument('--program')
    ap.add_argument('--pilot', action='store_true')
    args = ap.parse_args()
    rows = [json.loads(x) for x in POLICIES.read_text(encoding='utf-8').splitlines() if x.strip()]
    out=[]
    for r in rows:
        if r.get('agency') != args.agency: continue
        if not in_effect(r,args.school_year): continue
        if not grade_matches(r,args.grade): continue
        if args.topic and args.topic.lower() not in [t.lower() for t in r.get('topics',[])]: continue
        if not scope_matches(r, 'education_levels', args.education_level): continue
        if not scope_matches(r, 'institution_types', args.institution_type): continue
        if not scope_matches(r, 'programs', args.program): continue
        pilot_only = r.get('scope',{}).get('pilot_only',False)
        if pilot_only and not args.pilot: continue
        out.append(r)
    out.sort(key=lambda row: (row.get('series_year') or 0, row.get('number') or 0, row.get('id') or ''))
    result={
      'context':vars(args),
      'warning':'Candidates only. Resolve amendments, cohort/pilot status, institution scope, and controlling enclosures before implementation.',
      'policy_candidates':out
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))

if __name__=='__main__': main()
