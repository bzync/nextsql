import type { StudioResultSet } from "../ops/api";
import {
  currentWordRange, locateOffsetInText, tokenizeSQLWithSpans,
  NEXTSQL_FUNCTION_SUGGESTIONS, NEXTSQL_TYPE_SUGGESTIONS, type SQLSuggestion,
} from "./resultTools";
import { NEXTSQL_KEYWORDS } from "./SqlCodeEditor";
import native from "./sqlReference.generated.json";
import commands from "./sqlCommands.json";

// Advisory editing work is deliberately smaller than the executable SQL
// limit. The editor remains usable beyond this bound; server parsing and
// execution are unchanged. Never tokenize unbounded input on every keypress.
export const MAX_COMPLETION_CHARS = 128 * 1024;
export const MAX_FEATURE_NOTES = 20;
export type Availability = NonNullable<SQLSuggestion["availability"]>;
export type CapabilityMap = ReadonlyMap<string, { status: string; description: string }>;
const nativeFunctions = new Set(native.functions);
const geoFunctions = new Set(native.geoFunctions);
const windowFunctions = new Set(["row_number", "rank", "dense_rank", "lag", "lead", "first_value", "last_value"]);
const vectorFunctions = new Set(["cosine", "cosine_distance", "l1", "l2", "manhattan", "inner_product", "dot", "vector_dot", "vector_add", "vector_sub", "vector_subtract", "vector_scale", "vector_dim", "vector_dims", "dimensions", "vector_norm", "norm", "vector_normalize", "normalize"]);
const orderedCommands = [...commands].sort((a, b) => b.label.length - a.label.length);

export function readSQLCapabilities(result?: StudioResultSet | null): CapabilityMap {
  const map = new Map<string, { status: string; description: string }>();
  if (!result) return map;
  const name = result.columns.indexOf("name");
  const status = result.columns.indexOf("status");
  const description = result.columns.indexOf("description");
  if (name < 0 || status < 0) return map;
  for (const row of result.rows.slice(0, 1024)) {
    if (typeof row[name] !== "string" || typeof row[status] !== "string") continue;
    const key = row[name]!.toLowerCase();
    // Contradictory duplicate metadata must never turn an unavailable
    // feature into a supported completion.
    const value = { status: row[status]!.toLowerCase(), description: description >= 0 ? String(row[description] ?? "").slice(0, 1024) : "" };
    map.set(key, map.has(key) ? { status: "unknown", description: "Conflicting capability metadata" } : value);
  }
  return map;
}

export function featureForSQL(name: string, kind?: SQLSuggestion["kind"]): string | undefined {
  const lower = name.toLowerCase().replace(/\(.*$/, "").trim();
  if (kind === "function") {
    if (geoFunctions.has(lower)) return "geo";
    if (vectorFunctions.has(lower)) return "vector";
    if (windowFunctions.has(lower)) return "window_functions";
    if (lower.startsWith("json_")) return "json";
    if (lower === "highlight" || lower === "snippet") return "fulltext";
    return undefined;
  }
  const command = commands.find((entry) => entry.label.toLowerCase() === lower);
  if (command) return command.feature;
  if (lower.startsWith("system.")) return "system_catalog";
  if (/^(search|fulltext|highlight|snippet|facet|weight|analyzer)$/.test(lower)) return "fulltext";
  if (/^(nearest|vector(?:<.*)?|bitvector(?:<.*)?|cosine|l2|inner_product|hamming)$/.test(lower)) return "vector";
  if (lower === "hnsw") return "hnsw";
  if (lower === "ivf") return "vector_ivf";
  if (lower === "ivfpq") return "vector_ivfpq";
  if (lower === "quantization") return "quantized_vector_index";
  if (/^(sparse|sparsevector(?:<.*)?)$/.test(lower)) return "vector_sparse";
  if (/^(point|box|linestring|polygon|geometry|geography)(?:\b|<)/.test(lower)) return "geo";
  if (lower === "json") return "json";
  if (lower === "online") return "rebuild_index_online";
  if (lower === "include") return "covering_indexes";
  if (lower === "distinct") return "distinct";
  if (lower === "returning") return "returning";
  if (/^(union(?: all)?|intersect|except)$/.test(lower)) return "set_operations";
  if (lower === "over") return "window_functions";
  if (lower === "with recursive") return "cte";
  if (lower === "encrypted client" || lower === "deterministic") return "field_encryption_client";
  if (lower === "partition by range") return "partitions_range";
  if (lower === "partition by hash") return "partitions_hash";
  if (lower === "partition by list") return "partitions_list";
  return undefined;
}

export function sqlAvailability(feature: string | undefined, capabilities: CapabilityMap): { availability: Availability; note?: string } {
  if (!feature) return { availability: "supported" };
  const metadata = capabilities.get(feature);
  const status = metadata?.status;
  if (status === "supported" || status === "production") return { availability: "supported" };
  if (status === "unsupported" || status === "disabled" || status === "unavailable") {
    return { availability: "unsupported", note: `Unavailable on this server: ${feature}.${metadata?.description ? ` ${metadata.description}` : ""}` };
  }
  if (status === "experimental" || status === "deprecated") {
    return { availability: status, note: `${feature} is ${status} on this server.${metadata?.description ? ` ${metadata.description}` : ""}` };
  }
  return { availability: "unknown", note: `This server does not advertise ${feature}; availability is unverified.` };
}

export function annotateSQLSuggestions(items: SQLSuggestion[], capabilities: CapabilityMap): SQLSuggestion[] {
  return items.map((item) => {
    const name = item.kind === "function" ? item.insertText.replace(/\(.*$/, "") : item.insertText;
    const feature = featureForSQL(name, item.kind);
    const availability = sqlAvailability(feature, capabilities);
    const contextual = name === "AI" ? "AI() generates an incrementing default value; valid only in a column DEFAULT."
      : windowFunctions.has(name.toLowerCase()) ? "Requires an OVER (...) window clause."
        : name === "ONLINE" ? "Online rebuild is limited to non-partitioned B+Tree, UNIQUE, JSON-path, and spatial indexes."
          : item.note;
    return { ...item, ...availability, note: [availability.note, contextual].filter(Boolean).join(" ") || undefined };
  });
}

function tokensForEditing(sql: string) {
  return tokenizeSQLWithSpans(sql).filter((token) => !["ws", "line-comment", "block-comment"].includes(token.kind));
}

export function canAutoCompleteSQL(sql: string, cursor: number): boolean {
  if (sql.length > MAX_COMPLETION_CHARS || cursor < 0 || cursor > sql.length) return false;
  const tokens = tokenizeSQLWithSpans(sql.slice(0, cursor));
  const last = tokens[tokens.length - 1];
  if (!last || last.end !== cursor || last.kind === "param") return false;
  if (["string", "qident", "blob", "line-comment", "block-comment"].includes(last.kind)) return false;
  return (last.kind === "word" && last.text.length >= 2) || last.text === ".";
}

// Complete multiword statement heads as one replacement. Semicolons inside
// literals/comments never reset the range, and unrelated statement text is
// never replaced. Existing table/column completion takes over after a head.
export function commandSQLSuggestions(sql: string, cursor: number): SQLSuggestion[] | null {
  if (sql.length > MAX_COMPLETION_CHARS) return null;
  const tokens = tokensForEditing(sql.slice(0, cursor));
  const separator = tokens.map((token) => token.kind === "punct" && token.text === ";").lastIndexOf(true);
  const tail = tokens.slice(separator + 1);
  if (!tail.length || tail.length > 5 || tail.some((token) => token.kind !== "word")) return null;
  if (!/^[A-Za-z_\s]+$/.test(sql.slice(tail[0].start, cursor))) return null;
  const prefix = tail.map((token) => token.text.toUpperCase()).join(" ");
  const matches = commands.filter((command) => command.label.startsWith(prefix));
  if (!matches.length || matches.every((command) => command.label === prefix)) return null;
  const range = currentWordRange(sql, cursor);
  return matches.slice(0, 50).map((command) => ({
    kind: "keyword", label: command.label, insertText: command.label,
    detail: command.sample, note: command.note,
    replaceStart: tail[0].start, replaceEnd: range.end,
  }));
}

export function contextSQLSuggestions(sql: string, cursor: number): SQLSuggestion[] | null {
  if (sql.length > MAX_COMPLETION_CHARS) return null;
  const all = tokensForEditing(sql.slice(0, cursor));
  const separator = all.map((token) => token.kind === "punct" && token.text === ";").lastIndexOf(true);
  const tokens = all.slice(separator + 1);
  const words = tokens.map((token) => token.text.toUpperCase());
  const range = currentWordRange(sql, cursor);
  const prefix = sql.slice(range.start, cursor).toUpperCase();
  const last = tokens[tokens.length - 1];
  const before = last?.end === cursor && last.kind === "word" ? words.slice(0, -1) : words;
  const items = (values: readonly string[], kind: SQLSuggestion["kind"], detail: string) => values.filter((value) => value.startsWith(prefix)).map((value) => ({ kind, label: value, insertText: value, detail }));
  if (words[0] === "CREATE" && words[1] === "VECTOR" && words[2] === "INDEX" && before[before.length - 1] === "USING") {
    return items(["HNSW", "IVF", "IVFPQ", "SPARSE"], "keyword", "Native vector index algorithm");
  }
  if (words[0] === "ALTER" && words[1] === "TABLE" && tokens.length >= 3 && before.length === 3) {
    return items(["ADD COLUMN", "DROP COLUMN", "ALTER COLUMN", "RENAME COLUMN", "RENAME TO", "ADD CONSTRAINT", "DROP CONSTRAINT", "ADD PARTITION", "DROP PARTITION", "ATTACH PARTITION", "DETACH PARTITION"], "keyword", "Native table alteration");
  }
  let typeSlot = false;
  if (words[0] === "CREATE" && words[1] === "TABLE") {
    let depth = 0;
    let start = -1;
    for (let i = 2; i < tokens.length; i++) {
      if (tokens[i].text === "(") { depth++; if (depth === 1) start = i + 1; }
      if (tokens[i].text === ")") depth--;
      if (tokens[i].text === "," && depth === 1) start = i + 1;
    }
    const tail = tokens.slice(start);
    typeSlot = depth === 1 && start >= 0 && tail.length >= 1 && tail.length <= 2 &&
      ["word", "qident"].includes(tail[0].kind) &&
      !["PRIMARY", "FOREIGN", "CHECK", "CONSTRAINT", "UNIQUE"].includes(tail[0].text.toUpperCase()) &&
      (tail.length === 2 || last?.end !== cursor);
  }
  if (before.length >= 5 && before[0] === "ALTER" && before[1] === "TABLE" &&
      (before[before.length - 3] === "ADD" && before[before.length - 2] === "COLUMN")) typeSlot = true;
  if (before[before.length - 1] === "AS" && before.includes("CAST") && before.includes("(")) typeSlot = true;
  return typeSlot ? items(NEXTSQL_TYPE_SUGGESTIONS, "type", "Native column type; edit parameters for your schema") : null;
}

export type SQLFeatureNote = { title: string; message: string; offset: number; line: number; column: number; severity: "warning" | "info" };
const unsupportedHeads: Record<string, string> = {
  "CREATE DATABASE": "A NextSQL deployment serves one database. Initialize another deployment with Setup or the CLI.",
  "DROP DATABASE": "Database lifecycle is managed through Setup or the CLI, not SQL.",
  "ALTER DATABASE": "Database lifecycle is managed through Setup or the CLI, not SQL.",
  "CREATE PROCEDURE": "Use CREATE WORKFLOW and RUN WORKFLOW for native automation.",
  "CREATE FUNCTION": "SQL user-defined functions are not available in NextSQL.",
  "CREATE RETRIEVER": "Built-in RAG and retrievers are not part of NextSQL.",
  "USE": "Switch the server connection in Studio to work with another deployment.",
  "CALL": "Invoke native workflows with RUN WORKFLOW.",
  "TRUNCATE": "Use DELETE FROM with an explicit review of the affected rows.",
  "VACUUM": "Use MAINTAIN DATABASE, TABLE, or INDEX for native maintenance.",
  "COPY": "Use Studio's data import/export tools or the native CLI.",
  "MERGE": "Use native UPSERT for supported unique-key conflict handling.",
  "SHOW REALMS": "Multi-realm hosting was removed; a deployment serves one database.",
  "SET TENANT": "Shared row tenancy was removed; deployment isolation is authoritative.",
  "RESET TENANT": "Shared row tenancy was removed; deployment isolation is authoritative.",
  "RESTORE": "Restore and PITR are offline CLI operations, not SQL statements.",
};
const syntaxCalls = new Set([...NEXTSQL_KEYWORDS, "cast", "check", "exists", "over", "filter", "partition", "values", "in", "as", "recursive", "materialized", "unique", "key", "include", "unnest"]);

export function sqlFeatureNotes(sql: string, capabilities: CapabilityMap): SQLFeatureNote[] {
  if (sql.length > MAX_COMPLETION_CHARS) return [];
  const tokens = tokensForEditing(sql);
  const notes: SQLFeatureNote[] = [];
  const seen = new Set<string>();
  const add = (offset: number, title: string, message: string, severity: SQLFeatureNote["severity"] = "warning") => {
    const key = `${offset}:${message}`;
    if (notes.length >= MAX_FEATURE_NOTES || seen.has(key)) return;
    seen.add(key);
    notes.push({ title, message, offset, ...locateOffsetInText(sql, offset), severity });
  };
  let head = 0;
  let inQuery = false;
  let depth = 0;
  let cteDeclarations = false;
  for (let i = 0; i < tokens.length && notes.length < MAX_FEATURE_NOTES; i++) {
    const token = tokens[i];
    if (token.kind === "punct" && token.text === ";") { head = i + 1; inQuery = false; depth = 0; cteDeclarations = false; continue; }
    if (token.kind === "punct" && token.text === "(") depth++;
    if (token.kind === "punct" && token.text === ")") depth = Math.max(0, depth - 1);
    if (token.kind !== "word") continue;
    const word = token.text.toUpperCase();
    if (i === head) {
      inQuery = ["SELECT", "WITH", "INSERT", "UPSERT", "UPDATE", "DELETE", "EXPLAIN"].includes(word);
      const words = tokens.slice(i, i + 5).filter((t) => t.kind === "word").map((t) => t.text.toUpperCase());
      const unsupported = Object.keys(unsupportedHeads).find((label) => label.split(" ").every((part, n) => words[n] === part));
      if (unsupported) add(token.start, "Command unavailable in NextSQL", `${unsupported} is not supported. ${unsupportedHeads[unsupported]}`);
      const command = orderedCommands.find((entry) => entry.label.split(" ").every((part, n) => words[n] === part));
      if (command?.feature) {
        const availability = sqlAvailability(command.feature, capabilities);
        if (availability.note) add(token.start, availability.availability === "unsupported" ? "Feature unavailable on this server" : "Feature availability", availability.note, availability.availability === "unknown" ? "info" : "warning");
      }
    }
    if (inQuery && depth === 0 && word === "WITH") cteDeclarations = true;
    if (inQuery && depth === 0 && word === "SELECT") cteDeclarations = false;
    if (inQuery && ["SEARCH", "NEAREST", "OVER", "RETURNING", "DISTINCT"].includes(word)) {
      const availability = sqlAvailability(featureForSQL(word), capabilities);
      if (availability.note) add(token.start, "Feature availability", availability.note, availability.availability === "unknown" ? "info" : "warning");
    }
    if (["VECTOR", "BITVECTOR", "SPARSEVECTOR", "JSON", "POINT", "BOX", "LINESTRING", "POLYGON", "GEOMETRY", "GEOGRAPHY"].includes(word)) {
      const availability = sqlAvailability(featureForSQL(word, "type"), capabilities);
      if (availability.note) add(token.start, "Feature availability", availability.note, availability.availability === "unknown" ? "info" : "warning");
    }
    const vectorIndex = tokens[head]?.text.toUpperCase() === "CREATE" && tokens[head + 1]?.text.toUpperCase() === "VECTOR" && tokens[head + 2]?.text.toUpperCase() === "INDEX";
    const nextWord = tokens[i + 1]?.kind === "word" ? tokens[i + 1].text.toUpperCase() : "";
    let optionFeature: string | undefined;
    if (vectorIndex && ["HNSW", "IVF", "IVFPQ", "SPARSE", "QUANTIZATION"].includes(word)) optionFeature = featureForSQL(word);
    if (word === "ENCRYPTED" && nextWord === "CLIENT") optionFeature = "field_encryption_client";
    if (word === "FOREIGN" && nextWord === "KEY") optionFeature = "foreign_keys";
    if (word === "REFERENCES") optionFeature = "foreign_keys";
    if (word === "INCLUDE" && tokens[i + 1]?.text === "(") optionFeature = "covering_indexes";
    if (word === "ONLINE" && tokens[head]?.text.toUpperCase() === "REBUILD") optionFeature = "rebuild_index_online";
    if (word === "PARTITION" && nextWord === "BY") {
      const method = tokens[i + 2]?.text.toUpperCase() ?? "";
      if (["RANGE", "HASH", "LIST"].includes(method)) optionFeature = `partitions_${method.toLowerCase()}`;
    }
    if (word === "NEAREST" && inQuery && tokens.slice(head, i).some((entry) => entry.kind === "word" && entry.text.toUpperCase() === "SEARCH")) optionFeature = "hybrid_search";
    if (optionFeature) {
      const availability = sqlAvailability(optionFeature, capabilities);
      if (availability.note) add(token.start, "Feature availability", availability.note, availability.availability === "unknown" ? "info" : "warning");
    }
    // UDFs are not supported. Limit unknown-call warnings to expression
    // statements; CREATE TABLE columns and workflow declarations are not
    // function calls. Quoted identifiers/literals/comments are never matched.
    if (inQuery && tokens[i + 1]?.text === "(" && tokens[i - 1]?.text !== "." &&
        !syntaxCalls.has(token.text.toLowerCase()) &&
        !(cteDeclarations && depth === 0) &&
        !["FROM", "JOIN", "INTO", "UPDATE", "WITH"].includes(tokens[i - 1]?.text.toUpperCase() ?? "")) {
      if (!nativeFunctions.has(token.text.toLowerCase())) {
        add(token.start, "Function unavailable in NextSQL", `${word}() is not a built-in in this Admin build. Check the native function suggestions; the server validates on Run.`);
      } else {
        const availability = sqlAvailability(featureForSQL(word, "function"), capabilities);
        if (availability.note) add(token.start, "Feature availability", availability.note, availability.availability === "unknown" ? "info" : "warning");
      }
    }
    if (word === "AI" && tokens[i + 1]?.text === "(" && inQuery) add(token.start, "Default-only function", "AI() is valid only in a column DEFAULT.");
  }
  return notes;
}

export const SQL_REFERENCE_SUMMARY = `${commands.length} command forms · ${NEXTSQL_FUNCTION_SUGGESTIONS.length} built-ins · native types and catalog objects`;
