import type { ReactNode } from "react";

const SQL_TYPES = new Set([
  "UUID",
  "STRING",
  "TEXT",
  "BOOL",
  "BOOLEAN",
  "INT",
  "INT2",
  "INT4",
  "INT8",
  "SMALLINT",
  "BIGINT",
  "FLOAT",
  "FLOAT4",
  "FLOAT8",
  "DOUBLE",
  "DECIMAL",
  "NUMERIC",
  "TIMESTAMP",
  "TIMESTAMPTZ",
  "DATE",
  "TIME",
  "JSON",
  "POINT",
  "GEOMETRY",
  "GEOGRAPHY",
  "ARRAY",
  "MAP",
  "STRUCT",
  "VECTOR",
  "BYTES",
  "BLOB",
  "F32",
  "F16",
  "I8",
]);

const SQL_FUNCTIONS = new Set([
  "NOW",
  "UUID",
  "COUNT",
  "SUM",
  "AVG",
  "MIN",
  "MAX",
  "ARRAY_AGG",
  "STRING_AGG",
  "CONCAT",
  "SUBSTRING",
  "LOWER",
  "UPPER",
  "LENGTH",
  "COALESCE",
  "ROUND",
  "ABS",
  "FLOOR",
  "CEIL",
  "COSINE",
  "L2",
  "INNER_PRODUCT",
  "UNNEST",
  "ST_POINT",
  "ST_DISTANCE",
  "ST_DWITHIN",
  "ST_CONTAINS",
  "ST_INTERSECTS",
  "ST_ASTEXT",
  "ST_GEOMFROMTEXT",
  "JSON_EXTRACT",
  "JSON_VALUE",
  "JSON_QUERY",
]);

const SQL_KEYWORDS = new Set([
  "CREATE",
  "TABLE",
  "INDEX",
  "UNIQUE",
  "FULLTEXT",
  "SPATIAL",
  "INSERT",
  "INTO",
  "VALUES",
  "SELECT",
  "FROM",
  "WHERE",
  "AND",
  "OR",
  "NOT",
  "GROUP",
  "BY",
  "ORDER",
  "ASC",
  "DESC",
  "LIMIT",
  "OFFSET",
  "JOIN",
  "LEFT",
  "RIGHT",
  "FULL",
  "OUTER",
  "CROSS",
  "INNER",
  "ON",
  "UPDATE",
  "SET",
  "DELETE",
  "BEGIN",
  "COMMIT",
  "ROLLBACK",
  "ANALYZE",
  "EXPLAIN",
  "SEARCH",
  "FOR",
  "NEAREST",
  "TO",
  "USING",
  "HNSW",
  "IVF",
  "IVF_PQ",
  "DEFAULT",
  "PRIMARY",
  "KEY",
  "REFERENCES",
  "FOREIGN",
  "CONSTRAINT",
  "CASCADE",
  "RESTRICT",
  "AS",
  "IN",
  "IS",
  "LIKE",
  "ILIKE",
  "BETWEEN",
  "EXISTS",
  "CASE",
  "WHEN",
  "THEN",
  "ELSE",
  "END",
  "CAST",
  "GRANT",
  "REVOKE",
  "SUBSCRIBE",
  "AFTER",
  "ALTER",
  "DROP",
  "ADD",
  "RENAME",
  "TRUNCATE",
  "SHOW",
  "WITH",
  "UNION",
  "ALL",
  "DISTINCT",
  "HAVING",
  "WINDOW",
  "OVER",
  "PARTITION",
  "ROWS",
  "RANGE",
  "PRECEDING",
  "FOLLOWING",
  "CURRENT",
  "ROW",
  "FILTER",
  "FACET",
  "WEIGHT",
  "SIMILARITY",
  "ANALYZER",
  "SCORE",
  "HIGHLIGHT",
  "SNIPPET",
]);

const SHELL_COMMANDS = new Set([
  "nextsql",
  "nextsqld",
  "nextsql-admin",
  "./nextsql",
  "./nextsqld",
  "./nextsql-admin",
  "go",
  "git",
  "docker",
  "compose",
  "curl",
  "wget",
  "npm",
  "pnpm",
  "yarn",
  "bun",
  "composer",
  "pip",
  "pip3",
  "gem",
  "python",
  "python3",
  "node",
  "ruby",
  "php",
  "sudo",
  "chmod",
  "chown",
  "printf",
  "echo",
  "cat",
  "mkdir",
  "cd",
  "cp",
  "mv",
  "rm",
  "tar",
  "systemctl",
  "journalctl",
  "kill",
  "killall",
  "export",
  "source",
  "grep",
  "sed",
  "awk",
  "find",
  "ls",
  "head",
  "tail",
  "touch",
  "nano",
  "vim",
  "which",
  "diff",
  "sort",
  "uniq",
  "ps",
  "df",
  "du",
  "env",
  "sleep",
  "sha256sum",
  "dpkg",
  "apt",
  "apt-get",
  "yum",
  "dnf",
  "brew",
  "openssl",
  "ssh",
  "scp",
  "rsync",
  "tee",
  "xargs",
  "sh",
  "bash",
  "zsh",
]);

const SHELL_SUBCOMMANDS = new Set([
  "init",
  "setup",
  "lifecycle",
  "detect",
  "preflight",
  "backup-config",
  "upgrade",
  "repair",
  "uninstall",
  "registry",
  "adopt",
  "migrate-tenant",
  "show",
  "key",
  "status",
  "add-recovery",
  "verify-recovery",
  "remove-recovery",
  "recover",
  "login",
  "logout",
  "whoami",
  "exec",
  "migrate",
  "backup",
  "restore",
  "prune",
  "list",
  "version",
  "pending",
  "validate",
  "create",
  "up",
  "down",
  "force",
  "cluster",
  "compose",
  "build",
  "run",
  "test",
  "install",
  "get",
  "pull",
  "push",
  "add",
  "commit",
  "checkout",
  "clone",
  "require",
  "update",
  "stop",
  "start",
  "restart",
  "logs",
  "ps",
]);

export function HighlightCode({
  code,
  lang,
}: {
  code: string;
  lang?: string;
}) {
  const kind = (lang || "").toLowerCase();
  if (kind === "sql") return <>{tokenizeSql(code)}</>;
  if (
    kind === "bash" ||
    kind === "sh" ||
    kind === "shell" ||
    kind === "terminal" ||
    kind === "zsh" ||
    kind === "console"
  ) {
    return <>{tokenizeShell(code)}</>;
  }
  if (
    kind === "go" ||
    kind === "golang" ||
    kind === "js" ||
    kind === "javascript" ||
    kind === "ts" ||
    kind === "typescript" ||
    kind === "php" ||
    kind === "python" ||
    kind === "py" ||
    kind === "ruby" ||
    kind === "rb"
  ) {
    return <>{tokenizeDriverCode(code, kind)}</>;
  }
  if (
    kind === "wire" ||
    kind === "protocol" ||
    kind === "proto" ||
    kind === "frame" ||
    kind === "packet" ||
    kind === "binary" ||
    kind === "nsql-proto"
  ) {
    return <>{tokenizeWireProtocol(code)}</>;
  }
  return <>{code}</>;
}

const GO_KW = new Set([
  "package",
  "import",
  "func",
  "return",
  "defer",
  "if",
  "else",
  "for",
  "range",
  "go",
  "select",
  "case",
  "default",
  "switch",
  "const",
  "var",
  "type",
  "struct",
  "interface",
  "map",
  "chan",
  "nil",
  "true",
  "false",
]);

const JS_KW = new Set([
  "const",
  "let",
  "var",
  "function",
  "return",
  "await",
  "async",
  "import",
  "export",
  "from",
  "require",
  "if",
  "else",
  "for",
  "while",
  "switch",
  "case",
  "default",
  "new",
  "this",
  "class",
  "extends",
  "try",
  "catch",
  "finally",
  "throw",
  "typeof",
  "instanceof",
  "null",
  "undefined",
  "true",
  "false",
]);

const PHP_KW = new Set([
  "require",
  "include",
  "require_once",
  "include_once",
  "function",
  "return",
  "class",
  "public",
  "private",
  "protected",
  "static",
  "new",
  "if",
  "else",
  "elseif",
  "for",
  "foreach",
  "while",
  "switch",
  "case",
  "default",
  "try",
  "catch",
  "finally",
  "throw",
  "use",
  "namespace",
  "null",
  "true",
  "false",
  "array",
]);

const PY_KW = new Set([
  "import",
  "from",
  "as",
  "def",
  "class",
  "return",
  "if",
  "elif",
  "else",
  "for",
  "while",
  "try",
  "except",
  "finally",
  "raise",
  "with",
  "pass",
  "break",
  "continue",
  "lambda",
  "yield",
  "async",
  "await",
  "True",
  "False",
  "None",
  "and",
  "or",
  "not",
  "in",
  "is",
]);

const RB_KW = new Set([
  "require",
  "require_relative",
  "def",
  "end",
  "class",
  "module",
  "return",
  "if",
  "elsif",
  "else",
  "unless",
  "while",
  "for",
  "begin",
  "rescue",
  "ensure",
  "raise",
  "yield",
  "do",
  "true",
  "false",
  "nil",
  "and",
  "or",
  "not",
  "self",
]);

function tokenizeDriverCode(code: string, lang: string): ReactNode[] {
  const tokenRegex =
    /(\s+|\/\/[^\n]*|\/\*[\s\S]*?\*\/|#[^\n]*|"""[\s\S]*?"""|'''[\s\S]*?'''|`[^`]*`|'[^']*'|"[^"]*"|::|->|=>|:=|<=|>=|!=|==|&&|\|\||[+\-*\/%<>=&|!~^]|[(),;\[\]{}]|\$[a-zA-Z_][a-zA-Z0-9_]*|:[a-zA-Z_][a-zA-Z0-9_]*|\b\d+(?:\.\d+)?\b|[a-zA-Z_][a-zA-Z0-9_]*|[^\s\w]+)/g;

  const nodes: ReactNode[] = [];
  let m: RegExpExecArray | null;
  let keyIdx = 0;

  while ((m = tokenRegex.exec(code)) !== null) {
    const raw = m[0];
    const key = keyIdx++;

    if (/^\s+$/.test(raw)) {
      nodes.push(<span key={key}>{raw}</span>);
      continue;
    }
    if (
      raw.startsWith("//") ||
      raw.startsWith("/*") ||
      ((lang === "python" || lang === "py" || lang === "ruby" || lang === "rb" || lang === "php") &&
        raw.startsWith("#"))
    ) {
      nodes.push(
        <span key={key} className="hl-driver-cmt">
          {raw}
        </span>,
      );
      continue;
    }
    if (
      raw.startsWith("'") ||
      raw.startsWith('"') ||
      raw.startsWith("`") ||
      raw.startsWith('"""') ||
      raw.startsWith("'''")
    ) {
      const quoteChar = raw.startsWith('"""') || raw.startsWith("'''") ? raw.slice(0, 3) : raw.slice(0, 1);
      const inner = raw.slice(quoteChar.length, -quoteChar.length);
      const isSql = /^\s*(SELECT|INSERT|UPDATE|DELETE|CREATE|SUBSCRIBE|BEGIN|COMMIT|DROP|ALTER)\b/i.test(inner);

      if (isSql) {
        nodes.push(
          <span key={key}>
            <span className="hl-driver-str">{quoteChar}</span>
            {tokenizeSql(inner)}
            <span className="hl-driver-str">{quoteChar}</span>
          </span>,
        );
      } else {
        nodes.push(
          <span key={key} className="hl-driver-str">
            {raw}
          </span>,
        );
      }
      continue;
    }
    if (raw.startsWith("$")) {
      nodes.push(
        <span key={key} className="hl-driver-var">
          {raw}
        </span>,
      );
      continue;
    }
    if (raw.startsWith(":")) {
      nodes.push(
        <span key={key} className="hl-driver-prop">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^\d+(?:\.\d+)?$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-driver-num">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^(::|->|=>|:=|<=|>=|!=|==|&&|\|\||[+\-*\/%<>=&|!~^])$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-driver-op">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^[(),;\[\]{}]$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-driver-punct">
          {raw}
        </span>,
      );
      continue;
    }

    const isKw =
      ((lang === "go" || lang === "golang") && GO_KW.has(raw)) ||
      ((lang === "js" || lang === "javascript" || lang === "ts" || lang === "typescript") && JS_KW.has(raw)) ||
      (lang === "php" && PHP_KW.has(raw)) ||
      ((lang === "python" || lang === "py") && PY_KW.has(raw)) ||
      ((lang === "ruby" || lang === "rb") && RB_KW.has(raw));

    if (isKw) {
      nodes.push(
        <span key={key} className="hl-driver-kw">
          {raw}
        </span>,
      );
      continue;
    }

    const rest = code.slice(tokenRegex.lastIndex).trimStart();
    const isCall = rest.startsWith("(");

    if (isCall) {
      if (
        /^[A-Z][a-zA-Z0-9_]*$/.test(raw) &&
        (lang === "go" ||
          lang === "golang" ||
          lang === "python" ||
          lang === "py" ||
          lang === "ruby" ||
          lang === "rb" ||
          lang === "js" ||
          lang === "ts")
      ) {
        nodes.push(
          <span key={key} className="hl-driver-type">
            {raw}
          </span>,
        );
      } else {
        nodes.push(
          <span key={key} className="hl-driver-fn">
            {raw}
          </span>,
        );
      }
      continue;
    }

    if (/^[A-Z][a-zA-Z0-9_]*$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-driver-type">
          {raw}
        </span>,
      );
      continue;
    }

    if (rest.startsWith(":") && !rest.startsWith("::")) {
      nodes.push(
        <span key={key} className="hl-driver-prop">
          {raw}
        </span>,
      );
      continue;
    }

    nodes.push(
      <span key={key} className="hl-driver-id">
        {raw}
      </span>,
    );
  }

  return nodes;
}

function tokenizeSql(code: string): ReactNode[] {
  const tokenRegex =
    /(\s+|--[^\n]*|\/\*[\s\S]*?\*\/|'[^']*'|"[^"]*"|::|<=|>=|!=|<>|\|\||[+\-*\/%<>=]|[(),;\[\]{}]|\$[a-zA-Z0-9_]+|\b\d+(?:\.\d+)?\b|[a-zA-Z_][a-zA-Z0-9_.]*|[^\s\w]+)/g;

  const nodes: ReactNode[] = [];
  let m: RegExpExecArray | null;
  let keyIdx = 0;

  while ((m = tokenRegex.exec(code)) !== null) {
    const raw = m[0];
    const key = keyIdx++;

    if (/^\s+$/.test(raw)) {
      nodes.push(<span key={key}>{raw}</span>);
      continue;
    }
    if (raw.startsWith("--") || raw.startsWith("/*")) {
      nodes.push(
        <span key={key} className="hl-sql-cmt">
          {raw}
        </span>,
      );
      continue;
    }
    if (raw.startsWith("'") || raw.startsWith('"')) {
      nodes.push(
        <span key={key} className="hl-sql-str">
          {raw}
        </span>,
      );
      continue;
    }
    if (raw.startsWith("$")) {
      nodes.push(
        <span key={key} className="hl-sql-param">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^\d+(?:\.\d+)?$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-sql-num">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^(<=|>=|!=|<>|::|[+\-*\/%<>=])$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-sql-op">
          {raw}
        </span>,
      );
      continue;
    }
    if (/^[(),;\[\]{}]$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-sql-punct">
          {raw}
        </span>,
      );
      continue;
    }

    const upper = raw.toUpperCase();
    if (upper === "TRUE" || upper === "FALSE" || upper === "NULL") {
      nodes.push(
        <span key={key} className="hl-sql-bool">
          {raw}
        </span>,
      );
      continue;
    }

    const rest = code.slice(tokenRegex.lastIndex).trimStart();
    const isNextParen = rest.startsWith("(");

    if (isNextParen && (SQL_FUNCTIONS.has(upper) || (SQL_TYPES.has(upper) && upper === "UUID"))) {
      nodes.push(
        <span key={key} className="hl-sql-fn">
          {raw}
        </span>,
      );
      continue;
    }

    if (SQL_TYPES.has(upper)) {
      nodes.push(
        <span key={key} className="hl-sql-type">
          {raw}
        </span>,
      );
      continue;
    }

    if (SQL_KEYWORDS.has(upper)) {
      nodes.push(
        <span key={key} className="hl-sql-kw">
          {raw}
        </span>,
      );
      continue;
    }

    if (SQL_FUNCTIONS.has(upper)) {
      nodes.push(
        <span key={key} className="hl-sql-fn">
          {raw}
        </span>,
      );
      continue;
    }

    nodes.push(
      <span key={key} className="hl-sql-id">
        {raw}
      </span>,
    );
  }

  return nodes;
}

function tokenizeShell(code: string): ReactNode[] {
  const lines = code.split("\n");
  return lines.map((line, lineIdx) => (
    <span key={lineIdx}>
      {lineIdx > 0 ? "\n" : null}
      {tokenizeShellLine(line, lineIdx)}
    </span>
  ));
}

function tokenizeShellLine(line: string, lineIdx: number): ReactNode[] {
  if (!line) return [];
  if (/^\s*#/.test(line)) {
    return [<span key={`${lineIdx}-cmt`} className="hl-bash-cmt">{line}</span>];
  }

  const nodes: ReactNode[] = [];
  let rest = line;
  let keyIdx = 0;

  const promptMatch = rest.match(/^(\s*)([$>])(\s+)/);
  if (promptMatch) {
    if (promptMatch[1]) {
      nodes.push(<span key={`${lineIdx}-${keyIdx++}`}>{promptMatch[1]}</span>);
    }
    nodes.push(
      <span key={`${lineIdx}-${keyIdx++}`} className="hl-bash-prompt">
        {promptMatch[2]}
      </span>,
    );
    nodes.push(<span key={`${lineIdx}-${keyIdx++}`}>{promptMatch[3]}</span>);
    rest = rest.slice(promptMatch[0].length);
  }

  const tokenRegex =
    /(\s+|#[^\n]*|'[^']*'|"[^"]*"|\$\{[^}]+\}|\$[a-zA-Z_][a-zA-Z0-9_]*|\$[@*#?$!0-9]|&&|\|\||>>|<<|[|&;><\\]|--[a-zA-Z0-9_-]+|[-+][a-zA-Z0-9]+|[a-zA-Z_][a-zA-Z0-9_]*=(?:'[^']*'|"[^"]*"|\S+)?|https?:\/\/[^\s'")]+|(?:\.?\.?\/|[a-zA-Z0-9_.-]+@)[a-zA-Z0-9_./@+-]+|[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_./@+-]+|\/[a-zA-Z0-9_./-]+|[a-zA-Z0-9_.-]+|[^\s\w]+)/g;

  let match: RegExpExecArray | null;
  let expectCommand = true;

  while ((match = tokenRegex.exec(rest)) !== null) {
    const raw = match[0];
    const key = `${lineIdx}-${keyIdx++}`;

    if (/^\s+$/.test(raw)) {
      nodes.push(<span key={key}>{raw}</span>);
      continue;
    }
    if (raw.startsWith("#")) {
      nodes.push(
        <span key={key} className="hl-bash-cmt">
          {raw}
        </span>,
      );
      continue;
    }
    if (raw.startsWith("'") || raw.startsWith('"')) {
      nodes.push(
        <span key={key} className="hl-bash-str">
          {raw}
        </span>,
      );
      expectCommand = false;
      continue;
    }
    if (raw.startsWith("$")) {
      nodes.push(
        <span key={key} className="hl-bash-var">
          {raw}
        </span>,
      );
      expectCommand = false;
      continue;
    }
    if (raw.includes("=") && /^[a-zA-Z_][a-zA-Z0-9_]*=/.test(raw)) {
      const eqIdx = raw.indexOf("=");
      const varName = raw.slice(0, eqIdx);
      const val = raw.slice(eqIdx + 1);
      nodes.push(
        <span key={`${key}-v`} className="hl-bash-var">
          {varName}
        </span>,
      );
      nodes.push(
        <span key={`${key}-eq`} className="hl-bash-op">
          =
        </span>,
      );
      if (val) {
        if (/^\d+(\.\d+)*$/.test(val)) {
          nodes.push(
            <span key={`${key}-val`} className="hl-bash-num">
              {val}
            </span>,
          );
        } else if (val.startsWith("'") || val.startsWith('"')) {
          nodes.push(
            <span key={`${key}-val`} className="hl-bash-str">
              {val}
            </span>,
          );
        } else if (val.startsWith("/") || val.startsWith("./")) {
          nodes.push(
            <span key={`${key}-val`} className="hl-bash-path">
              {val}
            </span>,
          );
        } else {
          nodes.push(
            <span key={`${key}-val`} className="hl-bash-arg">
              {val}
            </span>,
          );
        }
      }
      continue;
    }
    if (raw === "&&" || raw === "||" || raw === "|" || raw === ";") {
      nodes.push(
        <span key={key} className="hl-bash-op">
          {raw}
        </span>,
      );
      expectCommand = true;
      continue;
    }
    if (raw === ">" || raw === ">>" || raw === "<" || raw === "<<" || raw === "\\") {
      nodes.push(
        <span key={key} className="hl-bash-op">
          {raw}
        </span>,
      );
      continue;
    }
    if (
      raw.startsWith("--") ||
      (raw.startsWith("-") && raw.length > 1) ||
      (raw.startsWith("+") && raw.length > 1)
    ) {
      nodes.push(
        <span key={key} className="hl-bash-flag">
          {raw}
        </span>,
      );
      expectCommand = false;
      continue;
    }
    if (
      raw.startsWith("http://") ||
      raw.startsWith("https://") ||
      raw.startsWith("/") ||
      (raw.startsWith("./") && !SHELL_COMMANDS.has(raw)) ||
      (raw.includes("/") && !expectCommand)
    ) {
      nodes.push(
        <span key={key} className="hl-bash-path">
          {raw}
        </span>,
      );
      expectCommand = false;
      continue;
    }
    if (/^\d+(\.\d+)*$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-bash-num">
          {raw}
        </span>,
      );
      expectCommand = false;
      continue;
    }
    if (expectCommand) {
      nodes.push(
        <span key={key} className="hl-bash-cmd">
          {raw}
        </span>,
      );
      if (raw !== "sudo" && raw !== "env") {
        expectCommand = false;
      }
      continue;
    }
    if (SHELL_SUBCOMMANDS.has(raw.toLowerCase())) {
      nodes.push(
        <span key={key} className="hl-bash-subcmd">
          {raw}
        </span>,
      );
      continue;
    }
    nodes.push(
      <span key={key} className="hl-bash-arg">
        {raw}
      </span>,
    );
  }

  return nodes;
}

const PROTO_OPCODES = new Set([
  "hello",
  "hellook",
  "auth",
  "authok",
  "query",
  "idempotentquery",
  "setreadconsistency",
  "nodestatus",
  "nodestatusresp",
  "prepare",
  "prepareok",
  "execute",
  "closestmt",
  "closeok",
  "flowack",
  "cancel",
  "terminate",
  "rowdesc",
  "databatch",
  "commandcomplete",
  "error",
  "ready",
  "unlock",
  "unlockok",
  "typeunlock",
  "client",
  "server",
]);

const PROTO_TYPES = new Set([
  "u8",
  "u16",
  "u32",
  "u64",
  "i8",
  "i16",
  "i32",
  "i64",
  "f16",
  "f32",
  "f64",
  "string",
  "bytes",
  "byte",
  "bool",
  "boolean",
  "utf8",
  "le",
  "be",
  "little-endian",
  "big-endian",
  "uint8",
  "uint16",
  "uint32",
  "uint64",
  "int8",
  "int16",
  "int32",
  "int64",
]);

const PROTO_FIELDS = new Set([
  "magic",
  "version",
  "type",
  "flags",
  "length",
  "payload",
  "cancel_secret",
  "secret",
  "db_len",
  "database",
  "user_len",
  "user",
  "realm_len",
  "realm",
  "auth_method",
  "accepted_flags",
  "statement_id",
  "sql_len",
  "sql_text",
  "param_count",
  "params",
  "row_count",
  "affected_rows",
  "code_len",
  "code",
  "msg_len",
  "message",
  "public_code",
  "mode",
  "max_staleness",
  "role",
  "has_leader",
  "healthy",
  "applied_lsn",
  "apply_backlog",
  "last_contact_ms",
  "key",
  "idempotency_key",
  "max_packet",
  "columns",
  "column_names",
  "column_types",
  "values",
]);

const PROTO_STATUS = new Set([
  "ok",
  "ready",
  "conflict",
  "exhausted",
  "unauthorized",
  "strong",
  "bounded",
  "stale",
]);

function tokenizeWireProtocol(code: string): ReactNode[] {
  const lines = code.split("\n");
  return lines.map((line, lineIdx) => (
    <span key={lineIdx}>
      {lineIdx > 0 ? "\n" : null}
      {tokenizeWireProtocolLine(line, lineIdx)}
    </span>
  ));
}

function tokenizeWireProtocolLine(line: string, lineIdx: number): ReactNode[] {
  if (!line) return [];

  if (/^\s*(?:#|\/\/)/.test(line)) {
    return [<span key={`${lineIdx}-cmt`} className="hl-proto-cmt">{line}</span>];
  }

  if (/^\s*[+|-]{3,}\s*$/.test(line)) {
    return [<span key={`${lineIdx}-box`} className="hl-proto-punct">{line}</span>];
  }

  const nodes: ReactNode[] = [];
  const tokenRegex =
    /(\s+|#[^\n]*|\/\/[^\n]*|'[^']*'|"[^"]*"|\((?:reserved|optional|default)[^)]*\)|0x[0-9a-fA-F]+|\b\d+(?:,\d+)*(?:\.\d+)?(?:\s*(?:MiB|KiB|GiB|ms|s|B))?\b|C[→↔<-]+S|S[→↔<-]+C|[-=]+>|<[-=]+|--+|==+|\+\+|[a-zA-Z_][a-zA-Z0-9_.-]*|[0-9]+-[0-9]+|[0-9]+\.\.[0-9]+|\.\.|[0-9]+[+-]|[^\s\w]+)/g;

  let match: RegExpExecArray | null;
  let isFirstToken = true;

  while ((match = tokenRegex.exec(line)) !== null) {
    const raw = match[0];
    const key = `${lineIdx}-${match.index}`;

    if (/^\s+$/.test(raw)) {
      nodes.push(raw);
      continue;
    }

    if (raw.startsWith("#") || raw.startsWith("//") || (raw.startsWith("(") && (raw.includes("reserved") || raw.includes("optional") || raw.includes("default")))) {
      nodes.push(
        <span key={key} className="hl-proto-cmt">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if ((raw.startsWith('"') && raw.endsWith('"')) || (raw.startsWith("'") && raw.endsWith("'"))) {
      nodes.push(
        <span key={key} className="hl-proto-str">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (
      /^\d+-\d+$/.test(raw) ||
      /^\d+\.\.\d+$/.test(raw) ||
      /^\d+B$/i.test(raw) ||
      /^\[\d+B\]$/i.test(raw) ||
      raw === ".." ||
      /^\d+\.\.$/.test(raw) ||
      /^\d+-$/.test(raw) ||
      (isFirstToken && /^\d+$/.test(raw))
    ) {
      nodes.push(
        <span key={key} className="hl-proto-offset">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (/^0x[0-9a-fA-F]+$/i.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-num">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (/^(?:C[→↔<-]+S|S[→↔<-]+C|[-=]+>|<[-=]+|--+|==+)$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-op">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    const lower = raw.toLowerCase();

    if (PROTO_OPCODES.has(lower)) {
      nodes.push(
        <span key={key} className="hl-proto-opcode">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (PROTO_TYPES.has(lower) || /^u(8|16|32|64)$/i.test(raw) || /^i(8|16|32|64)$/i.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-type">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (PROTO_STATUS.has(lower) || /^ERR_[A-Z0-9_]+$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-status">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (PROTO_FIELDS.has(lower)) {
      nodes.push(
        <span key={key} className="hl-proto-field">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (/^\d+(?:,\d+)*(?:\.\d+)?(?:\s*(?:MiB|KiB|GiB|ms|s|B))?$/i.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-num">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (/^[=:+*<>/\\|&-]+$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-op">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    if (/^[()[\]{},;.]$/.test(raw)) {
      nodes.push(
        <span key={key} className="hl-proto-punct">
          {raw}
        </span>
      );
      isFirstToken = false;
      continue;
    }

    nodes.push(raw);
    isFirstToken = false;
  }

  return nodes;
}
