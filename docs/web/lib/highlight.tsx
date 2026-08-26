import type { ReactNode } from "react";

const SQL_KW =
  /^(CREATE|TABLE|INDEX|UNIQUE|FULLTEXT|VECTOR|SPATIAL|INSERT|INTO|VALUES|SELECT|FROM|WHERE|AND|OR|NOT|GROUP|BY|ORDER|ASC|DESC|LIMIT|JOIN|LEFT|RIGHT|FULL|OUTER|CROSS|ON|UPDATE|SET|DELETE|BEGIN|COMMIT|ROLLBACK|ANALYZE|EXPLAIN|SEARCH|FOR|NEAREST|TO|USING|HNSW|DEFAULT|PRIMARY|KEY|NOT|NULL|REFERENCES|FOREIGN|CONSTRAINT|CASCADE|RESTRICT|UUID|STRING|TEXT|DECIMAL|TIMESTAMPTZ|JSON|POINT|NOW|COSINE|L2|INNER_PRODUCT)$/i;

export function HighlightCode({
  code,
  lang,
}: {
  code: string;
  lang?: string;
}) {
  const kind = (lang || "").toLowerCase();
  if (kind === "sql") return <>{tokenizeSql(code)}</>;
  if (kind === "bash" || kind === "sh" || kind === "shell" || kind === "terminal") {
    return <>{tokenizeShell(code)}</>;
  }
  return <>{code}</>;
}

function tokenizeSql(code: string): ReactNode[] {
  const parts = code.split(/(\s+|[{}(),;<>]|--[^\n]*|'[^']*'|"[^"]*"|\[[^\]]*\]|\$\w+|VECTOR<[^>]+>|\d[\d.]*)/g);
  return parts.map((part, i) => {
    if (!part) return null;
    if (part.startsWith("--")) return <span key={i} className="hl-cmt">{part}</span>;
    if (part.startsWith("'") || part.startsWith('"')) return <span key={i} className="hl-str">{part}</span>;
    if (SQL_KW.test(part) || /^VECTOR</i.test(part)) return <span key={i} className="hl-kw">{part}</span>;
    if (/^\d/.test(part) || part.startsWith("$")) return <span key={i} className="hl-num">{part}</span>;
    return <span key={i}>{part}</span>;
  });
}

function tokenizeShell(code: string): ReactNode[] {
  const lines = code.split("\n");
  return lines.map((line, i) => (
    <span key={i}>
      {i > 0 ? "\n" : null}
      {line.startsWith("#") ? <span className="hl-cmt">{line}</span> : colorShell(line, i)}
    </span>
  ));
}

function colorShell(line: string, key: number): ReactNode {
  const m = /^(printf|\.\/nextsql|\.\/nextsqld|git|cd|go|chmod)\b/.exec(line.trimStart());
  if (!m) return line;
  const idx = line.indexOf(m[1]);
  return (
    <span key={key}>
      {line.slice(0, idx)}
      <span className="hl-kw">{m[1]}</span>
      {line.slice(idx + m[1].length)}
    </span>
  );
}
