import Link from "next/link";
import { SearchHeader } from "@/components/SearchHeader";
import { SiteFooter } from "@/components/SiteFooter";
import { CodePanel, HYBRID_SQL } from "@/components/landing/CodePanel";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@bzync/rui";

const specs = [
  { k: "Crypto", v: "AES-256-GCM envelope" },
  { k: "Wire", v: "NSQL v1 · TLS 1.3" },
  { k: "Page", v: "16 KiB logical" },
  { k: "HA", v: "Raft, 3 voters" },
  { k: "Search", v: "BM25 + HNSW + IVF" },
];

const features = [
  {
    n: "01",
    title: "One system of record",
    body: "Relational columns, JSON, vectors, full-text, and geo live in the same table and the same transaction.",
  },
  {
    n: "02",
    title: "Encrypted by default",
    body: "Pages, WAL, UNDO, indexes, vectors, full-text trees, backups, and spills. Established AES-256-GCM only.",
  },
  {
    n: "03",
    title: "Keys stay off the disk",
    body: "Root unlock is a --key-file you keep off the data volume. Drivers reject keys and passwords in a URL.",
  },
  {
    n: "04",
    title: "Durable writes",
    body: "Group-commit WAL plus fsync before commit is acknowledged. Stolen files stay ciphertext.",
  },
  {
    n: "05",
    title: "Hybrid in one plan",
    body: "Filters, BM25, and ANN share the cost model. Reciprocal rank fusion, then LIMIT. EXPLAIN shows the path.",
  },
  {
    n: "06",
    title: "Honest operations",
    body: "Official benches keep encryption, WAL, fsync, checksums, MVCC, and auth on. Overload returns unavailable.",
  },
];

const models = [
  { name: "Relational", detail: "Clustered B+Tree, FK, RANGE/HASH/LIST" },
  { name: "JSON", detail: "Binary NSJB, path extract, path indexes" },
  { name: "Full-text", detail: "BM25, analyzers, prefix, fuzzy, facets" },
  { name: "Vectors", detail: "F32/F16/I8, sparse, HNSW/IVF/IVF-PQ" },
  { name: "Geo", detail: "WGS84 shapes + GEOMETRY / GEOGRAPHY" },
];

export default function Home() {
  return (
    <div className="relative min-h-full">
      <SearchHeader />
      <main id="content">
        <Hero />
        <SpecStrip />
        <FeatureList />
        <Hybrid />
        <Security />
        <Architecture />
        <Drivers />
        <QuickStart />
        <Status />
      </main>
      <SiteFooter />
    </div>
  );
}

function Hero() {
  return (
    <section>
      <div className="mx-auto grid max-w-6xl items-center gap-10 px-4 pt-12 pb-12 sm:px-5 sm:pt-16 sm:pb-16 lg:grid-cols-2 lg:gap-14 lg:pt-20 lg:pb-20">
        <div className="flex flex-col items-center text-center lg:items-start lg:text-left">
          <p className="kicker">Native multimodel database</p>
          <h1 className="mt-3 max-w-[22ch] text-[2.2rem] font-semibold leading-[1.12] tracking-[-0.028em] sm:text-[2.75rem] lg:text-[3.15rem]">
            One engine for SQL, JSON, vectors, and search.
          </h1>
          <p className="mt-4 max-w-[42ch] text-[16px] leading-relaxed text-muted sm:mt-5 sm:text-[17px]">
            Relational data, native JSON, full-text, vectors, and geo share one WAL,
            one MVCC, and one optimizer. Install the engine and run it.
          </p>
          <div className="mt-7 flex w-full max-w-[22rem] flex-col items-stretch gap-3 sm:mt-8 sm:w-auto sm:max-w-none sm:flex-row sm:flex-wrap sm:items-center sm:justify-center lg:justify-start">
            <Link href="/docs/quick-start" className="btn-cta">
              Get started
              <ArrowIcon />
            </Link>
            <Link href="/download" className="btn-cta-quiet">
              Download
            </Link>
            <Link
              href="/docs/introduction"
              className="inline-flex h-11 items-center justify-center px-1 text-sm font-medium text-muted hover:text-foreground"
            >
              Documentation
            </Link>
          </div>
          <p className="mt-6 max-w-md font-mono text-[12px] leading-5 text-faint">
            Not PostgreSQL, MySQL, MongoDB, or a vector-store compatibility layer.
            Native format, dialect, protocol, and drivers.
          </p>
        </div>
        <div className="min-w-0 max-w-full">
          <CodePanel title="hybrid.sql">{HYBRID_SQL}</CodePanel>
        </div>
      </div>
    </section>
  );
}

function SpecStrip() {
  return (
    <section className="border-y border-line">
      <div className="mx-auto grid max-w-6xl grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
        {specs.map((spec) => (
          <div
            key={spec.k}
            className="border-line border-b border-r px-4 py-4 even:border-r-0 sm:px-5 sm:py-5 sm:even:border-r sm:[&:nth-child(3n)]:border-r-0 lg:border-b-0 lg:even:border-r lg:[&:nth-child(5n)]:border-r-0"
          >
            <p className="kicker">{spec.k}</p>
            <p className="mt-1.5 text-sm font-medium tracking-tight">{spec.v}</p>
          </div>
        ))}
      </div>
    </section>
  );
}

function FeatureList() {
  return (
    <section className="border-b border-line">
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <div className="max-w-2xl">
          <p className="kicker">Platform</p>
          <h2 className="mt-3 text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
            Built as one engine. Operated with the safety still on.
          </h2>
        </div>
        <ol className="mt-10 grid gap-x-10 gap-y-0 md:grid-cols-2">
          {features.map((feature) => (
            <li key={feature.n} className="border-t border-line py-5">
              <span className="font-mono text-[11px] text-faint">{feature.n}</span>
              <h3 className="mt-1.5 text-[1.02rem] font-semibold tracking-tight">{feature.title}</h3>
              <p className="mt-1.5 max-w-prose text-sm leading-6 text-muted">{feature.body}</p>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

function Hybrid() {
  return (
    <section className="border-b border-line">
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <div className="overflow-hidden rounded-lg border border-line lg:grid lg:grid-cols-2">
          <div className="border-line px-4 py-10 sm:px-8 sm:py-14 lg:border-r">
            <p className="kicker">Multimodel</p>
            <h2 className="mt-3 text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
              Five models. One physical plan.
            </h2>
            <p className="mt-4 max-w-md text-sm leading-6 text-muted">
              That hybrid SELECT is not a federated query. Filters, BM25, and ANN
              participate in the same cost model. The write path is the same WAL,
              MVCC, and encryption as a DECIMAL update.
            </p>
            <ul className="mt-8 space-y-0">
              {models.map((model) => (
                <li
                  key={model.name}
                  className="flex flex-col gap-0.5 border-t border-line py-3 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4"
                >
                  <span className="text-sm font-medium">{model.name}</span>
                  <span className="font-mono text-[11px] text-faint sm:text-right">{model.detail}</span>
                </li>
              ))}
            </ul>
            <Link
              href="/docs/hybrid"
              className="mt-8 inline-flex text-sm font-medium text-link hover:underline"
            >
              Hybrid queries
            </Link>
          </div>
          <div className="code-stage flex flex-col justify-end overflow-x-auto px-5 py-10 font-mono text-[12.5px] leading-7 sm:px-8 sm:py-12 sm:text-[13px]">
            <p className="text-faint"># same table, same transaction</p>
            <p>
              <span className="hl-kw">SELECT</span> id, name
            </p>
            <p>
              <span className="hl-kw">FROM</span> products
            </p>
            <p>
              <span className="hl-kw">WHERE</span> metadata.category ={" "}
              <span className="hl-str">&apos;headphones&apos;</span>
            </p>
            <p>
              <span className="hl-kw">SEARCH</span> description <span className="hl-kw">FOR</span>{" "}
              <span className="hl-str">&apos;noise cancelling&apos;</span>
            </p>
            <p>
              <span className="hl-kw">NEAREST</span> embedding <span className="hl-kw">TO</span> $query
            </p>
            <p>
              <span className="hl-kw">LIMIT</span> 20;
            </p>
            <p className="mt-8 text-faint">EXPLAIN → Candidates, Rerank bm25+vector</p>
          </div>
        </div>
      </div>
    </section>
  );
}

function Security() {
  return (
    <section className="border-b border-line">
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <p className="kicker">Threat model</p>
        <h2 className="mt-3 max-w-2xl text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
          Encryption protects files at rest. It does not hide plaintext from a live process.
        </h2>
        <div className="mt-10">
          <Table>
            <TableHeader>
              <tr>
                <TableHead>Attacker</TableHead>
                <TableHead>Gets</TableHead>
                <TableHead>Does not get</TableHead>
              </tr>
            </TableHeader>
            <TableBody>
              <TableRow>
                <TableCell className="font-medium text-foreground">Stolen disks, WAL, backups, trees</TableCell>
                <TableCell>Ciphertext, wrapped DEKs, key IDs</TableCell>
                <TableCell>Plaintext without the root unlock key</TableCell>
              </TableRow>
              <TableRow>
                <TableCell className="font-medium text-foreground">Network observer (remote)</TableCell>
                <TableCell>TLS 1.3 records</TableCell>
                <TableCell>SQL, passwords, unlock material</TableCell>
              </TableRow>
              <TableRow>
                <TableCell className="font-medium text-foreground">Live unlocked nextsqld</TableCell>
                <TableCell>Keys, pages, and rows in RAM</TableCell>
                <TableCell>Nothing. The process decrypts to run SQL.</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <p className="mt-5 max-w-2xl text-sm leading-6 text-muted">
          Envelope: external root → KEK → database master → separate DEKs for pages,
          WAL, UNDO, backup, vector, full-text, temp, and replication.{" "}
          <Link href="/docs/security" className="text-link underline underline-offset-3">
            Security docs
          </Link>
        </p>
      </div>
    </section>
  );
}

function Architecture() {
  const layers = [
    "Native wire protocol → TLS 1.3 → authn → RBAC / realm",
    "SQL parser → binder / catalog → planner → cost optimizer",
    "Vectorized executor: relational · JSON · vector · full-text · geo · collections",
    "MVCC + row/range locks + UNDO",
    "REDO WAL (group commit, fsync)",
    "Buffer manager → AES-256-GCM sealed pages",
  ];
  return (
    <section className="border-b border-line bg-bg-elev">
      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-12 sm:gap-10 sm:px-5 sm:py-16 lg:grid-cols-12 lg:py-20">
        <div className="lg:col-span-5">
          <p className="kicker">Architecture</p>
          <h2 className="mt-3 text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
            Catalog, HNSW, and inverted postings go through the same WAL.
          </h2>
          <div className="mt-6 flex flex-wrap gap-x-5 gap-y-2 text-sm">
            <Link href="/docs/architecture" className="text-link underline underline-offset-3">
              Architecture
            </Link>
            <Link href="/docs/protocol" className="text-muted hover:text-foreground">
              Wire protocol
            </Link>
            <Link href="/docs/sql" className="text-muted hover:text-foreground">
              SQL dialect
            </Link>
          </div>
        </div>
        <ol className="lg:col-span-7">
          {layers.map((layer, i) => (
            <li key={layer} className="grid grid-cols-[2.2rem_1fr] gap-3 border-t border-line py-3 font-mono text-[12px] leading-6 sm:grid-cols-[2.4rem_1fr] sm:text-[12.5px]">
              <span className="text-faint">{String(i + 1).padStart(2, "0")}</span>
              <span>{layer}</span>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

function Drivers() {
  const rows = [
    { runtime: "Go", path: "go get …/drivers/go", open: "nextsql.Open(nextsql.Config{…})" },
    { runtime: "Node.js 18+", path: "npm i @bzync/nextsql", open: "connect({ address, user, password, tls })" },
    { runtime: "Bun", path: "drivers/bun (repo)", open: "same shape as Node" },
    { runtime: "PHP 8.1+", path: "composer require bzync/nextsql", open: "NextSQL\\Client::connect([…])" },
    { runtime: "Python 3.10+", path: "pip install bzync-nextsql", open: "nextsql.connect(nextsql.Config(…))" },
    { runtime: "Ruby 3.0+", path: "gem install bzync-nextsql", open: "NextSQL.connect(NextSQL::Config.new(…))" },
  ];
  return (
    <section className="border-b border-line">
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <p className="kicker">Drivers</p>
            <h2 className="mt-3 text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
              Native NSQL. No keys in the URL.
            </h2>
          </div>
          <Link href="/docs/drivers" className="text-sm text-link underline underline-offset-3">
            Driver docs
          </Link>
        </div>
        <p className="mt-3 max-w-xl text-sm leading-6 text-muted">
          Official drivers speak NSQL v1. TLS 1.3 is required off loopback.
          <code className="mx-1 rounded bg-bg-hover px-1 font-mono text-[12px]">--insecure</code>
          is loopback-only.
        </p>
        <div className="mt-8">
          <Table>
            <TableHeader>
              <tr>
                <TableHead>Runtime</TableHead>
                <TableHead>Install</TableHead>
                <TableHead>Open</TableHead>
              </tr>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.runtime}>
                  <TableCell className="font-medium text-foreground">{row.runtime}</TableCell>
                  <TableCell className="font-mono text-xs">{row.path}</TableCell>
                  <TableCell className="font-mono text-xs">{row.open}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>
    </section>
  );
}

function QuickStart() {
  const steps = `go install github.com/bzync/nextsql/cmd/nextsql@latest
go install github.com/bzync/nextsql/cmd/nextsqld@latest

printf 'secret\\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

nextsql init --data-dir /var/lib/nextsql \\
  --key-file /etc/nextsql/root.key \\
  --user app --password-file /tmp/nextsql.pw

nextsqld --data-dir /var/lib/nextsql \\
  --key-file /etc/nextsql/root.key \\
  --listen 127.0.0.1:7210 \\
  --user app --password-file /tmp/nextsql.pw`;

  return (
    <section className="border-b border-line">
      <div className="mx-auto grid max-w-6xl lg:grid-cols-2">
        <div className="border-line px-4 py-12 sm:px-5 sm:py-16 lg:border-r lg:py-20">
          <p className="kicker">Install</p>
          <h2 className="mt-3 text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
            Install, init, serve.
          </h2>
          <p className="mt-4 max-w-md text-sm leading-6 text-muted">
            Install the <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsql</code> and{" "}
            <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsqld</code> binaries, then
            initialize a data directory. Keep the root unlock key off the data volume.
            Loopback may run without TLS; any other bind needs{" "}
            <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">--tls-cert</code> and{" "}
            <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">--tls-key</code>.
          </p>
          <Link href="/docs/quick-start" className="btn-cta mt-8 w-fit">
            Full walkthrough
            <ArrowIcon />
          </Link>
        </div>
        <div className="min-w-0 px-4 pb-12 sm:px-5 lg:p-6 lg:px-0 lg:pb-0">
          <CodePanel title="sh" lang="bash">
            {steps}
          </CodePanel>
        </div>
      </div>
    </section>
  );
}

function Status() {
  return (
    <section>
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <p className="kicker">Status</p>
        <h2 className="mt-3 max-w-2xl text-[1.7rem] font-semibold tracking-[-0.025em] sm:text-[2rem]">
          The database is built. Run it on your machine.
        </h2>
        <p className="mt-4 max-w-2xl text-sm leading-6 text-muted">
          Storage, WAL, MVCC, SQL, optimizer, protocol, JSON, full-text, vectors,
          hybrid plans, workflows, CDC, partitioning, security 2.0, backup/PITR/export,
          and Raft HA ship in{" "}
          <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsql</code> and{" "}
          <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsqld</code>.
          NextSQL Admin covers Setup and Operations; Studio is in progress.
          Install the binaries, initialize a data directory, and start serving NSQL.
        </p>
        <div className="mt-8 flex flex-wrap gap-2">
          <Link href="/docs/quick-start" className="btn-cta btn-cta-sm">
            Get started
            <ArrowIcon />
          </Link>
          <Link href="/docs/install" className="btn-cta-quiet btn-cta-sm">
            Install
          </Link>
          <Link href="/docs/limits" className="btn-cta-quiet btn-cta-sm">
            Limits
          </Link>
        </div>
      </div>
    </section>
  );
}

function ArrowIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="5" y1="12" x2="19" y2="12" />
      <polyline points="12 5 19 12 12 19" />
    </svg>
  );
}
