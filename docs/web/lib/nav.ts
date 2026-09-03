export type NavItem = {
  title: string;
  slug: string;
  description: string;
};

export type NavGroup = {
  title: string;
  items: NavItem[];
};

export const docsNav: NavGroup[] = [
  {
    title: "Get started",
    items: [
      {
        title: "Introduction",
        slug: "introduction",
        description: "What NextSQL is, what it is not, and how to read these docs.",
      },
      {
        title: "Install",
        slug: "install",
        description: "Install nextsql and nextsqld, then initialize a data directory.",
      },
      {
        title: "Quick start",
        slug: "quick-start",
        description: "Initialize a data directory, start nextsqld, and run SQL.",
      },
    ],
  },
  {
    title: "SQL",
    items: [
      {
        title: "Dialect",
        slug: "sql",
        description: "Types, statements, identifiers, parameters, and EXPLAIN.",
      },
      {
        title: "Relational",
        slug: "relational",
        description: "Tables, partitioning, indexes, foreign keys, joins, aggregates, and DML.",
      },
      {
        title: "JSON",
        slug: "json",
        description: "Binary NSJB columns, path extract, and path indexes.",
      },
      {
        title: "Full-text search",
        slug: "fulltext",
        description: "Inverted indexes, BM25 ranking, phrases, and English stemming.",
      },
      {
        title: "Vectors",
        slug: "vectors",
        description: "VECTOR<F32,N>, NEAREST, HNSW, and distance functions.",
      },
      {
        title: "Hybrid queries",
        slug: "hybrid",
        description: "One plan for filters, BM25, and ANN with reciprocal rank fusion.",
      },
      {
        title: "Geospatial",
        slug: "geo",
        description: "WGS84 POINT, BOX, distances, and spatial indexes.",
      },
      {
        title: "Transactions",
        slug: "transactions",
        description: "READ COMMITTED, SNAPSHOT, SERIALIZABLE, WAL, and MVCC.",
      },
      {
        title: "Change streams",
        slug: "cdc",
        description: "Committed CDC, SUBSCRIBE, resume tokens, RBAC, and backpressure.",
      },
    ],
  },
  {
    title: "Operate",
    items: [
      {
        title: "Users, roles, and isolation",
        slug: "security",
        description: "Password auth, RBAC, hosted database isolation, and the honest threat model.",
      },
      {
        title: "Command line",
        slug: "cli",
        description: "nextsql and nextsqld flags, env files, and exit codes.",
      },
      {
        title: "Server configuration",
        slug: "config",
        description: "Config file, admission control, budgets, and wire limits.",
      },
      {
        title: "Migrations",
        slug: "migrate",
        description: "Timestamped SQL files applied over NSQL to a running server.",
      },
      {
        title: "TLS and client keys",
        slug: "tls",
        description: "TLS 1.3 off loopback, and --require-client-key unlock.",
      },
      {
        title: "Backup and PITR",
        slug: "backup",
        description: "Encrypted physical backup, verify, restore, and WAL archive.",
      },
      {
        title: "Export and import",
        slug: "export",
        description: "Logical snapshots of schema and committed rows.",
      },
      {
        title: "High availability",
        slug: "ha",
        description: "Three-node Raft, quorum commits, and failover targets.",
      },
      {
        title: "Diagnostics and benches",
        slug: "ops",
        description: "status, diagnose, nextsql-bench --slo, and official tests.",
      },
      {
        title: "System catalog",
        slug: "system-catalog",
        description: "system.* introspection: capabilities, tables, sessions, and live queries.",
      },
    ],
  },
  {
    title: "Drivers",
    items: [
      {
        title: "Overview",
        slug: "drivers",
        description: "Native NSQL v1 drivers. Keys and passwords never go in a URL.",
      },
      {
        title: "Go",
        slug: "drivers-go",
        description: "nextsql.Open, Exec, Query, Prepare, and KeyProvider.",
      },
      {
        title: "Node, Bun, Deno",
        slug: "drivers-js",
        description: "connect(), typed parameters, and TypeScript types.",
      },
      {
        title: "PHP",
        slug: "drivers-php",
        description: "NextSQL\\Client::connect for PHP 8.1+.",
      },
      {
        title: "Python",
        slug: "drivers-python",
        description: "nextsql.connect for Python 3.10+, stdlib only.",
      },
      {
        title: "Ruby",
        slug: "drivers-ruby",
        description: "NextSQL.connect for Ruby 3.0+, stdlib only.",
      },
    ],
  },
  {
    title: "Internals",
    items: [
      {
        title: "Architecture",
        slug: "architecture",
        description: "One engine: parser, optimizer, executor, WAL, encryption.",
      },
      {
        title: "Wire protocol",
        slug: "protocol",
        description: "NSQL v1 framing spoken by nextsqld and official drivers.",
      },
      {
        title: "Limits and gaps",
        slug: "limits",
        description: "Hard limits, unimplemented SQL, and measurement notes.",
      },
    ],
  },
];

export function allDocs(): NavItem[] {
  return docsNav.flatMap((group) => group.items);
}

export function findDoc(slug: string): NavItem | undefined {
  return allDocs().find((item) => item.slug === slug);
}

export function adjacentDocs(slug: string): {
  prev?: NavItem;
  next?: NavItem;
} {
  const items = allDocs();
  const index = items.findIndex((item) => item.slug === slug);
  if (index < 0) return {};
  return {
    prev: index > 0 ? items[index - 1] : undefined,
    next: index < items.length - 1 ? items[index + 1] : undefined,
  };
}

export function docHref(slug: string): string {
  return `/docs/${slug}`;
}
