import { CodeBlock } from "@/components/CodeBlock";

export function CodePanel({
  title,
  lang = "sql",
  children,
}: {
  title: string;
  lang?: string;
  children: string;
}) {
  return <CodeBlock code={children} lang={lang} title={title} />;
}

export const HYBRID_SQL = `CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    tenant_id   UUID NOT NULL,
    name        STRING NOT NULL,
    description TEXT,
    price       DECIMAL(12,2),
    metadata    JSON,
    embedding   VECTOR<F32,1536>,
    location    POINT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX ix_category ON products (metadata.category);
CREATE FULLTEXT INDEX ix_desc ON products (description);
CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW;

SELECT id, name, price
FROM products
WHERE metadata.category = 'headphones'
  AND price <= 15000
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query
LIMIT 20;`;
