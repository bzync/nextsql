import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type CSSProperties,
  type KeyboardEvent,
  type UIEvent,
} from "react";

export type SqlTokenType =
  | "keyword"
  | "type"
  | "function"
  | "string"
  | "number"
  | "comment"
  | "operator"
  | "punctuation"
  | "parameter"
  | "identifier"
  | "plain";

export interface SqlToken {
  type: SqlTokenType;
  value: string;
}

// Browser-side presentation counterpart of internal/sql/lexer/lexer.go's
// keywords map. test-sql-editor.mjs compares both sets on every Studio test
// run, so a lexer change cannot silently leave highlighting stale. This set is
// never used for parsing or completion.
export const NEXTSQL_KEYWORDS: ReadonlySet<string> = new Set([
  "create", "table", "index", "unique", "on", "insert", "into", "values",
  "select", "distinct", "from", "where", "update", "set", "delete", "begin",
  "commit", "rollback", "primary", "key", "not", "null", "default", "and",
  "or", "between", "in", "is", "limit", "offset", "as", "true", "false",
  "transaction", "read", "committed", "snapshot", "serializable", "uuid",
  "string", "text", "blob", "int8", "int16", "int32", "int64", "uint8",
  "uint16", "uint32", "uint64", "char", "varchar", "enum", "float32",
  "float64", "decimal", "timestamptz", "timestamp", "date", "time", "interval",
  "json", "struct", "array", "map", "vector", "bitvector", "sparsevector",
  "f32", "f16", "i8", "explain", "analyze", "maintain", "point", "box",
  "location", "linestring", "polygon", "spatial", "geometry", "geography",
  "fulltext", "search", "for", "nearest", "to", "using", "hnsw", "cosine",
  "l2", "inner_product", "hamming", "join", "inner", "left", "right", "full",
  "cross", "outer", "group", "having", "by", "drop", "user", "role", "grant",
  "revoke", "identified", "cluster", "database", "schema", "column", "function",
  "backup", "replication", "administration", "connect", "execute", "all",
  "privileges", "admin", "reset", "foreign", "references", "constraint",
  "cascade", "restrict", "action", "match", "alter", "add", "rename", "rebuild",
  "order", "asc", "desc", "if", "exists", "case", "when", "then", "else",
  "end", "union", "intersect", "except", "with", "over", "schedule", "every",
  "at", "cron", "upsert", "returning", "workflow", "run", "trigger", "before",
  "after", "each", "show", "task", "tasks", "cancel", "subscribe", "encrypted",
  "client", "deterministic", "transfer", "leader", "resource", "drain",
  "maintenance", "enable", "disable", "reconcile", "confirm", "unnest",
  "like",
]);

const NEXTSQL_TYPE_KEYWORDS = new Set([
  "uuid", "string", "text", "blob", "int8", "int16", "int32", "int64",
  "uint8", "uint16", "uint32", "uint64", "char", "varchar", "enum", "float32",
  "float64", "decimal", "timestamptz", "timestamp", "date", "time", "interval",
  "json", "struct", "array", "map", "vector", "bitvector", "sparsevector",
  "f32", "f16", "i8", "point", "box", "location", "linestring", "polygon",
  "geometry", "geography",
]);

// A 1 MiB Studio request can still contain hundreds of thousands of tokens or
// lines. Rendering one DOM node for each would let a local editor buffer freeze
// the page. Above either ceiling the textarea remains fully editable and the
// cosmetic overlay/gutter simply falls back to plain text.
export const MAX_HIGHLIGHT_CHARS = 256 * 1024;
export const MAX_HIGHLIGHT_LINES = 10_000;

function isASCIIDigit(ch: string | undefined): boolean {
  return ch !== undefined && ch >= "0" && ch <= "9";
}

function isASCIIIdentStart(ch: string | undefined): boolean {
  return ch !== undefined && ((ch >= "A" && ch <= "Z") || (ch >= "a" && ch <= "z") || ch === "_");
}

function isASCIIIdentPart(ch: string | undefined): boolean {
  return isASCIIIdentStart(ch) || isASCIIDigit(ch);
}

function nativeIdentifierPartWidth(src: string, offset: number): number {
  if (isASCIIIdentPart(src[offset])) return 1;
  const codePoint = src.codePointAt(offset);
  if (codePoint === undefined) return 0;
  const character = String.fromCodePoint(codePoint);
  return /[\p{L}\p{N}]/u.test(character) ? character.length : 0;
}

// Presentation-only mirror of the lexical forms accepted by NextSQL's native
// lexer. React escapes every token value when rendering. Invalid or incomplete
// input stays visible; the server parser remains authoritative and Studio's
// existing diagnostics route reports real errors.
export function tokenizeSql(src: string): SqlToken[] {
  const tokens: SqlToken[] = [];
  let i = 0;

  const push = (type: SqlTokenType, start: number) => {
    tokens.push({ type, value: src.slice(start, i) });
  };

  while (i < src.length) {
    const c = src[i];

    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      const start = i++;
      while (i < src.length && (src[i] === " " || src[i] === "\t" || src[i] === "\n" || src[i] === "\r")) i++;
      push("plain", start);
      continue;
    }

    if (c === "-" && src[i + 1] === "-") {
      const start = i;
      i += 2;
      while (i < src.length && src[i] !== "\n") i++;
      push("comment", start);
      continue;
    }

    if (c === "/" && src[i + 1] === "*") {
      const start = i;
      i += 2;
      while (i + 1 < src.length && !(src[i] === "*" && src[i + 1] === "/")) i++;
      i = i + 1 < src.length ? i + 2 : src.length;
      push("comment", start);
      continue;
    }

    if ((c === "x" || c === "X") && src[i + 1] === "'") {
      const start = i;
      i += 2;
      while (i < src.length && src[i] !== "'") i++;
      if (i < src.length) i++;
      push("string", start);
      continue;
    }

    if (c === "'") {
      const start = i++;
      while (i < src.length) {
        if (src[i] !== "'") {
          i++;
          continue;
        }
        if (src[i + 1] === "'") {
          i += 2;
          continue;
        }
        i++;
        break;
      }
      push("string", start);
      continue;
    }

    if (c === '"') {
      const start = i++;
      while (i < src.length) {
        if (src[i] !== '"') {
          i++;
          continue;
        }
        if (src[i + 1] === '"') {
          i += 2;
          continue;
        }
        i++;
        break;
      }
      push("identifier", start);
      continue;
    }

    if (c === "$") {
      const start = i++;
      if (isASCIIDigit(src[i])) {
        while (isASCIIDigit(src[i])) i++;
      } else if (isASCIIIdentStart(src[i])) {
        while (isASCIIIdentPart(src[i])) i++;
      }
      push("parameter", start);
      continue;
    }

    if (isASCIIDigit(c)) {
      const start = i++;
      while (isASCIIDigit(src[i])) i++;
      if (src[i] === ".") {
        i++;
        while (isASCIIDigit(src[i])) i++;
      }
      push("number", start);
      continue;
    }

    if (isASCIIIdentStart(c)) {
      const start = i++;
      for (let width = nativeIdentifierPartWidth(src, i); width > 0; width = nativeIdentifierPartWidth(src, i)) i += width;
      const word = src.slice(start, i).toLowerCase();
      if (NEXTSQL_TYPE_KEYWORDS.has(word)) {
        push("type", start);
      } else if (NEXTSQL_KEYWORDS.has(word)) {
        push("keyword", start);
      } else {
        let next = i;
        while (src[next] === " " || src[next] === "\t" || src[next] === "\n" || src[next] === "\r") next++;
        push(src[next] === "(" ? "function" : "identifier", start);
      }
      continue;
    }

    const start = i;
    const pair = src.slice(i, i + 2);
    if (pair === "!=" || pair === "<>" || pair === "<=" || pair === ">=") {
      i += 2;
      push("operator", start);
      continue;
    }
    if (c === "=" || c === "<" || c === ">" || c === "+" || c === "-" || c === "/" || c === "*") {
      i++;
      push("operator", start);
      continue;
    }
    if (c === "(" || c === ")" || c === "[" || c === "]" || c === "," || c === "." || c === ";") {
      i++;
      push("punctuation", start);
      continue;
    }

    i++;
    push("plain", start);
  }

  return tokens;
}

export interface SqlCodeEditorProps {
  value: string;
  onChange?: (value: string) => void;
  language?: string;
  placeholder?: string;
  minRows?: number;
  maxRows?: number;
  readOnly?: boolean;
  className?: string;
}

export function SqlCodeEditor({
  value,
  onChange,
  language = "sql",
  placeholder = "Enter NextSQL SQL",
  minRows = 12,
  maxRows = 28,
  readOnly = false,
  className,
}: SqlCodeEditorProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const preRef = useRef<HTMLPreElement>(null);
  const gutterRef = useRef<HTMLDivElement>(null);
  const [cursor, setCursor] = useState({ line: 1, column: 1, selected: 0 });

  const lineCount = useMemo(() => {
    let count = 1;
    for (let i = 0; i < value.length; i++) if (value[i] === "\n") count++;
    return count;
  }, [value]);
  const rows = Math.min(maxRows, Math.max(minRows, lineCount));
  const highlightEnabled = value.length <= MAX_HIGHLIGHT_CHARS && lineCount <= MAX_HIGHLIGHT_LINES;
  const tokens = useMemo(() => highlightEnabled ? tokenizeSql(value) : [], [highlightEnabled, value]);
  const bodyStyle = { height: `${Math.max(180, rows * 24 + 24)}px` } as CSSProperties;

  const syncScroll = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    if (preRef.current) {
      preRef.current.scrollTop = textarea.scrollTop;
      preRef.current.scrollLeft = textarea.scrollLeft;
    }
    if (gutterRef.current) gutterRef.current.scrollTop = textarea.scrollTop;
  }, []);

  const updateCursor = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const before = textarea.value.slice(0, textarea.selectionStart);
    const lines = before.split("\n");
    setCursor({
      line: lines.length,
      column: lines[lines.length - 1].length + 1,
      selected: Math.max(0, textarea.selectionEnd - textarea.selectionStart),
    });
  }, []);

  useLayoutEffect(() => {
    syncScroll();
  }, [value, syncScroll]);

  const setValueAndSelection = useCallback((next: string, start: number, end = start) => {
    onChange?.(next);
    requestAnimationFrame(() => {
      const textarea = textareaRef.current;
      if (!textarea) return;
      textarea.focus();
      textarea.setSelectionRange(start, end);
      updateCursor();
      syncScroll();
    });
  }, [onChange, syncScroll, updateCursor]);

  const handleChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onChange?.(event.currentTarget.value);
    updateCursor();
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const textarea = event.currentTarget;
    const { selectionStart, selectionEnd, value: current } = textarea;

    if (event.key === "Tab") {
      event.preventDefault();
      const lineStart = current.lastIndexOf("\n", selectionStart - 1) + 1;
      const nextLine = current.indexOf("\n", selectionEnd);
      const blockEnd = nextLine < 0 ? current.length : nextLine;
      const block = current.slice(lineStart, blockEnd);
      if (event.shiftKey) {
        const unindented = block.replace(/^ {1,2}/gm, "");
        const beforeSelection = current.slice(lineStart, selectionStart);
        const removedBeforeStart = beforeSelection.length - beforeSelection.replace(/^ {1,2}/gm, "").length;
        const removed = block.length - unindented.length;
        setValueAndSelection(
          current.slice(0, lineStart) + unindented + current.slice(blockEnd),
          Math.max(lineStart, selectionStart - removedBeforeStart),
          Math.max(lineStart, selectionEnd - removed),
        );
      } else if (selectionStart === selectionEnd) {
        setValueAndSelection(current.slice(0, selectionStart) + "  " + current.slice(selectionEnd), selectionStart + 2);
      } else {
        const indented = block.replace(/^/gm, "  ");
        setValueAndSelection(
          current.slice(0, lineStart) + indented + current.slice(blockEnd),
          selectionStart + 2,
          selectionEnd + indented.length - block.length,
        );
      }
      return;
    }

    if (event.key === "Enter" && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault();
      const lineStart = current.lastIndexOf("\n", selectionStart - 1) + 1;
      const indent = current.slice(lineStart, selectionStart).match(/^(\s*)/)?.[1] ?? "";
      const insert = `\n${indent}`;
      setValueAndSelection(current.slice(0, selectionStart) + insert + current.slice(selectionEnd), selectionStart + insert.length);
      return;
    }

    const pairs: Record<string, string> = { "(": ")", "[": "]", "{": "}", "'": "'", '"': '"' };
    const closing = pairs[event.key];
    if (closing) {
      if (selectionStart === selectionEnd && current[selectionStart] === event.key && (event.key === "'" || event.key === '"')) {
        event.preventDefault();
        textarea.setSelectionRange(selectionStart + 1, selectionStart + 1);
        updateCursor();
        return;
      }
      event.preventDefault();
      const selected = current.slice(selectionStart, selectionEnd);
      setValueAndSelection(
        current.slice(0, selectionStart) + event.key + selected + closing + current.slice(selectionEnd),
        selectionStart + 1,
        selectionEnd > selectionStart ? selectionEnd + 1 : selectionStart + 1,
      );
      return;
    }

    if ((event.key === ")" || event.key === "]" || event.key === "}") && selectionStart === selectionEnd && current[selectionStart] === event.key) {
      event.preventDefault();
      textarea.setSelectionRange(selectionStart + 1, selectionStart + 1);
      updateCursor();
      return;
    }

    if (event.key === "Backspace" && selectionStart === selectionEnd && selectionStart > 0) {
      const pair = current.slice(selectionStart - 1, selectionStart + 1);
      if (pair === "()" || pair === "[]" || pair === "{}" || pair === "''" || pair === '\"\"') {
        event.preventDefault();
        setValueAndSelection(current.slice(0, selectionStart - 1) + current.slice(selectionStart + 1), selectionStart - 1);
      }
    }
  };

  return (
    <div className={`nss-sql-editor${highlightEnabled ? "" : " nss-sql-editor--plain"}${className ? ` ${className}` : ""}`}>
      <div className="nss-sql-editor-header">
        <span className="nss-sql-editor-badge">{language.toUpperCase()}</span>
        <div className="nss-sql-editor-header-right">
          {!highlightEnabled ? <span role="status">Highlighting paused for large buffer</span> : null}
          {cursor.selected > 0 ? <span className="nss-sql-editor-sel">{cursor.selected} selected</span> : null}
          <span>Ln {cursor.line}, Col {cursor.column}</span>
          <span>{lineCount.toLocaleString()} {lineCount === 1 ? "line" : "lines"}</span>
        </div>
      </div>
      <div className="nss-sql-editor-body" style={bodyStyle}>
        {highlightEnabled ? (
          <div ref={gutterRef} className="nss-sql-editor-gutter" aria-hidden="true">
            {Array.from({ length: lineCount }, (_, index) => (
              <div key={index} className="nss-sql-editor-gutter-line">{index + 1}</div>
            ))}
          </div>
        ) : null}
        <div className="nss-sql-editor-surface">
          {highlightEnabled ? (
            <pre ref={preRef} className="nss-sql-editor-highlight" aria-hidden="true">
              <code>
                {tokens.map((token, index) => (
                  <span key={index} className={`nss-token-${token.type}`}>{token.value}</span>
                ))}
                {value.endsWith("\n") ? "\n" : ""}
              </code>
            </pre>
          ) : null}
          <textarea
            ref={textareaRef}
            value={value}
            onChange={handleChange}
            onScroll={(_event: UIEvent<HTMLTextAreaElement>) => syncScroll()}
            onKeyDown={handleKeyDown}
            onKeyUp={updateCursor}
            onClick={updateCursor}
            onSelect={updateCursor}
            readOnly={readOnly}
            placeholder={placeholder}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            autoCorrect="off"
            aria-label="SQL editor"
            rows={rows}
            className="nss-sql-editor-textarea"
          />
        </div>
      </div>
    </div>
  );
}
