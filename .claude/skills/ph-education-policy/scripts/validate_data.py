#!/usr/bin/env python3
"""Validate the education-policy seed corpus and its cross-record invariants."""
from __future__ import annotations

import json
import re
import sys
from datetime import date
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
POLICIES = ROOT / "data" / "policies.jsonl"
CURRICULA = ROOT / "data" / "curricula.json"
AGENCIES = ROOT / "data" / "agencies.json"
ID_RE = re.compile(r"^[A-Z0-9]+(?:-[A-Z0-9]+)*$")
SCHOOL_YEAR_RE = re.compile(r"^(\d{4})-(\d{4})$")
ALLOWED_SCOPE = {
    "education_levels", "grade_levels", "institution_types", "key_stages", "pilot_only", "programs"
}


def read_json(path: Path, errors: list[str]) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        errors.append(f"{path.name}: {exc}")
        return None


def valid_school_year(value: Any, label: str, errors: list[str], nullable: bool = True) -> int | None:
    if value is None and nullable:
        return None
    if not isinstance(value, str):
        errors.append(f"{label}: school year must be a string like 2026-2027")
        return None
    match = SCHOOL_YEAR_RE.fullmatch(value)
    if not match or int(match.group(2)) != int(match.group(1)) + 1:
        errors.append(f"{label}: invalid consecutive school year {value!r}")
        return None
    return int(match.group(1))


def valid_issued_at(value: Any, label: str, errors: list[str]) -> int | None:
    if value is None:
        return None
    if isinstance(value, str) and re.fullmatch(r"\d{4}", value):
        return int(value)
    if not isinstance(value, str):
        errors.append(f"{label}: issued_at must be YYYY, YYYY-MM-DD, or null")
        return None
    try:
        return date.fromisoformat(value).year
    except ValueError:
        errors.append(f"{label}: invalid issued_at {value!r}")
        return None


def load_policies(errors: list[str]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    try:
        lines = POLICIES.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as exc:
        errors.append(f"policies.jsonl: {exc}")
        return rows
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            errors.append(f"policies.jsonl line {line_number}: {exc}")
            continue
        if not isinstance(row, dict):
            errors.append(f"policies.jsonl line {line_number}: record must be an object")
            continue
        rows.append(row)
    return rows


def validate() -> tuple[list[str], int, int]:
    errors: list[str] = []
    agencies = read_json(AGENCIES, errors)
    curricula = read_json(CURRICULA, errors)
    rows = load_policies(errors)
    if not isinstance(agencies, list):
        errors.append("agencies.json: root must be an array")
        agencies = []
    if not isinstance(curricula, list):
        errors.append("curricula.json: root must be an array")
        curricula = []

    agency_codes: set[str] = set()
    for index, agency in enumerate(agencies):
        label = f"agencies[{index}]"
        if not isinstance(agency, dict):
            errors.append(f"{label}: must be an object")
            continue
        code = agency.get("code")
        if not isinstance(code, str) or not ID_RE.fullmatch(code):
            errors.append(f"{label}: invalid code")
            continue
        if code in agency_codes:
            errors.append(f"{label}: duplicate agency code {code}")
        agency_codes.add(code)
        urls = agency.get("official_urls")
        if not isinstance(urls, list) or not urls:
            errors.append(f"{label}: official_urls must be a non-empty array")
        else:
            for url in urls:
                parsed = urlsplit(url) if isinstance(url, str) else None
                if not parsed or parsed.scheme != "https" or not parsed.netloc:
                    errors.append(f"{label}: invalid official HTTPS URL {url!r}")

    ids: list[Any] = [row.get("id") for row in rows]
    if len(ids) != len(set(ids)):
        errors.append("policies.jsonl: duplicate policy IDs found")
    known_ids = {value for value in ids if isinstance(value, str)}
    required = (
        "id", "agency", "issuance_type", "title", "issued_at", "effective_from", "effective_until",
        "status", "topics", "scope", "relations", "source",
    )
    for row in rows:
        label = str(row.get("id", "<unknown>"))
        for key in required:
            if key not in row:
                errors.append(f"{label}: missing {key}")
        if not ID_RE.fullmatch(label):
            errors.append(f"{label}: invalid policy ID")
        if row.get("agency") not in agency_codes:
            errors.append(f"{label}: unknown agency {row.get('agency')!r}")
        for key in ("issuance_type", "title", "status"):
            if not isinstance(row.get(key), str) or not row.get(key, "").strip():
                errors.append(f"{label}: {key} must be a non-empty string")
        series_year = row.get("series_year")
        if series_year is not None and (
            not isinstance(series_year, int) or isinstance(series_year, bool) or not 1900 <= series_year <= 2200
        ):
            errors.append(f"{label}: series_year must be a plausible integer or null")
        issued_year = valid_issued_at(row.get("issued_at"), label, errors)
        if issued_year is not None and isinstance(series_year, int) and issued_year != series_year:
            errors.append(f"{label}: issued_at year does not match series_year")
        start = valid_school_year(row.get("effective_from"), f"{label}.effective_from", errors)
        end = valid_school_year(row.get("effective_until"), f"{label}.effective_until", errors)
        if start is not None and end is not None and start > end:
            errors.append(f"{label}: effective_from is after effective_until")

        topics = row.get("topics")
        if not isinstance(topics, list) or not topics or not all(isinstance(x, str) and x for x in topics):
            errors.append(f"{label}: topics must be a non-empty string array")
        elif len(topics) != len(set(topics)):
            errors.append(f"{label}: topics contains duplicates")

        scope = row.get("scope")
        if not isinstance(scope, dict):
            errors.append(f"{label}: scope must be an object")
            scope = {}
        unknown_scope = sorted(set(scope) - ALLOWED_SCOPE)
        if unknown_scope:
            errors.append(f"{label}: unknown scope keys: {', '.join(unknown_scope)}")
        for key in ("education_levels", "institution_types", "programs"):
            value = scope.get(key)
            if value is not None and (
                not isinstance(value, list) or not value or not all(isinstance(x, str) and x for x in value)
            ):
                errors.append(f"{label}: scope.{key} must be a non-empty string array")
        grades = scope.get("grade_levels")
        if grades is not None and (
            not isinstance(grades, list)
            or not grades
            or not all(isinstance(x, int) and not isinstance(x, bool) and 0 <= x <= 12 for x in grades)
        ):
            errors.append(f"{label}: scope.grade_levels must contain integers from 0 to 12")
        stages = scope.get("key_stages")
        if stages is not None and (
            not isinstance(stages, list)
            or not stages
            or not all(isinstance(x, int) and not isinstance(x, bool) and 1 <= x <= 4 for x in stages)
        ):
            errors.append(f"{label}: scope.key_stages must contain integers from 1 to 4")
        if "pilot_only" in scope and not isinstance(scope["pilot_only"], bool):
            errors.append(f"{label}: scope.pilot_only must be boolean")

        relations = row.get("relations")
        if not isinstance(relations, list):
            errors.append(f"{label}: relations must be an array")
            relations = []
        seen_relations: set[tuple[str, str]] = set()
        for index, relation in enumerate(relations):
            if not isinstance(relation, dict):
                errors.append(f"{label}.relations[{index}]: must be an object")
                continue
            relation_type = relation.get("type")
            target = relation.get("target")
            if not isinstance(relation_type, str) or not relation_type:
                errors.append(f"{label}.relations[{index}]: missing type")
            if target not in known_ids:
                errors.append(f"{label}.relations[{index}]: unknown target {target!r}")
            if target == label:
                errors.append(f"{label}.relations[{index}]: self-reference is not allowed")
            pair = (str(relation_type), str(target))
            if pair in seen_relations:
                errors.append(f"{label}.relations[{index}]: duplicate relationship")
            seen_relations.add(pair)

        source = row.get("source")
        if not isinstance(source, dict):
            errors.append(f"{label}: source must be an object")
            continue
        if source.get("official") is not True:
            errors.append(f"{label}: maintained seed records require an official source")
        url = source.get("url")
        parsed = urlsplit(url) if isinstance(url, str) else None
        if source.get("official") and (not parsed or parsed.scheme != "https" or not parsed.netloc):
            errors.append(f"{label}: official source requires an HTTPS URL")
        verified = source.get("verified_at")
        if verified is not None:
            try:
                verified_date = date.fromisoformat(verified)
                if verified_date > date.today():
                    errors.append(f"{label}: source.verified_at is in the future")
                if issued_year is not None and verified_date.year < issued_year:
                    errors.append(f"{label}: source.verified_at predates the issuance")
            except (TypeError, ValueError):
                errors.append(f"{label}: source.verified_at must be an ISO date or null")
        else:
            errors.append(f"{label}: source.verified_at is required for maintained seed records")

    curriculum_ids: set[str] = set()
    for index, curriculum in enumerate(curricula):
        label = f"curricula[{index}]"
        if not isinstance(curriculum, dict):
            errors.append(f"{label}: must be an object")
            continue
        curriculum_id = curriculum.get("id")
        if not isinstance(curriculum_id, str) or not ID_RE.fullmatch(curriculum_id):
            errors.append(f"{label}: invalid id")
            continue
        label = curriculum_id
        if curriculum_id in curriculum_ids:
            errors.append(f"{label}: duplicate curriculum ID")
        curriculum_ids.add(curriculum_id)
        if curriculum.get("agency") not in agency_codes:
            errors.append(f"{label}: unknown agency {curriculum.get('agency')!r}")
        for key in ("name", "status"):
            if not isinstance(curriculum.get(key), str) or not curriculum.get(key, "").strip():
                errors.append(f"{label}: {key} must be a non-empty string")
        levels = curriculum.get("education_levels")
        if not isinstance(levels, list) or not levels or not all(isinstance(x, str) and x for x in levels):
            errors.append(f"{label}: education_levels must be a non-empty string array")
        start = valid_school_year(curriculum.get("effective_from"), f"{label}.effective_from", errors, False)
        end = valid_school_year(curriculum.get("effective_until"), f"{label}.effective_until", errors)
        if start is not None and end is not None and start > end:
            errors.append(f"{label}: effective_from is after effective_until")
        bases = curriculum.get("legal_bases", [])
        if not isinstance(bases, list):
            errors.append(f"{label}: legal_bases must be an array")
        else:
            for basis in bases:
                if basis not in known_ids:
                    errors.append(f"{label}: missing legal basis record {basis!r}")
        rollout = curriculum.get("grade_rollout", {})
        if not isinstance(rollout, dict):
            errors.append(f"{label}: grade_rollout must be an object")
        else:
            for school_year, grades in rollout.items():
                valid_school_year(school_year, f"{label}.grade_rollout", errors, False)
                if not isinstance(grades, list) or not all(
                    isinstance(x, int) and not isinstance(x, bool) and 0 <= x <= 12 for x in grades
                ):
                    errors.append(f"{label}.grade_rollout[{school_year!r}]: invalid grades")

    return errors, len(rows), len(curricula)


def main() -> None:
    errors, policy_count, curriculum_count = validate()
    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        raise SystemExit(1)
    print(f"OK: {policy_count} policy records, {curriculum_count} curriculum records validated")


if __name__ == "__main__":
    main()
