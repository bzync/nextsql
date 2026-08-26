import Link from "next/link";
import { SearchHeader } from "@/components/SearchHeader";
import { SiteFooter } from "@/components/SiteFooter";
import { CodePanel, HYBRID_SQL } from "@/components/landing/CodePanel";
import { Badge } from "@/components/ui/badge";
import { buttonClassName } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const specs = [
  { k: "Crypto", v: "AES-256-GCM envelope" },
  { k: "Wire", v: "NSQL v1 · TLS 1.3" },
  { k: "Page", v: "16 KiB logical" },
  { k: "HA", v: "Raft, 3 voters" },
  { k: "Search", v: "BM25 + HNSW" },
  { k: "Engine", v: "Phases 0–15 built" },
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
  { name: "Relational", detail: "Clustered B+Tree, FK, joins, GROUP BY" },
  { name: "JSON", detail: "Binary NSJB, path extract, path indexes" },
  { name: "Full-text", detail: "Inverted index, BM25, phrases" },
  { name: "Vectors", detail: "VECTOR<F32,N>, flat + HNSW" },
  { name: "Geo", detail: "WGS84 POINT / BOX / LINESTRING" },
];

export default function Home() {
  return (
    <div className="relative min-h-full overflow-x-clip">
      <div className="pointer-events-none absolute inset-0 -z-10 overflow-hidden" aria-hidden="true">
        <div className="absolute inset-0 bg-grid-subtle [mask-image:linear-gradient(to_bottom,black,transparent_75%)]" />
      </div>
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
    <section className="relative overflow-hidden">
      <div
        className="pointer-events-none absolute inset-0 opacity-[0.06]"
        style={{
          backgroundImage:
            "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='100' height='100'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='2' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100' height='100' filter='url(%23n)' opacity='0.55'/%3E%3C/svg%3E\")",
          backgroundSize: "100px 100px",
          maskImage: "radial-gradient(ellipse 85% 65% at 50% 15%, black 20%, transparent 100%)",
          WebkitMaskImage: "radial-gradient(ellipse 85% 65% at 50% 15%, black 20%, transparent 100%)",
        }}
      />
      <div className="relative mx-auto grid max-w-6xl items-center gap-10 px-4 pt-12 pb-12 sm:px-5 sm:pt-16 sm:pb-16 lg:grid-cols-2 lg:gap-12 lg:pt-24 lg:pb-20">
        <div className="flex flex-col items-center text-center lg:items-start lg:text-left">
          <Badge variant="default" dot pulse>
            Encrypted by default
          </Badge>
          <h1 className="mt-5 max-w-[560px] text-[2.35rem] font-bold leading-[1.08] tracking-[-0.035em] sm:mt-6 sm:text-5xl sm:leading-[1.06] lg:text-[64px]">
            One engine for SQL, JSON,{" "}
            <span className="text-blue-600 dark:text-blue-400">vectors, and search.</span>
          </h1>
          <p className="mt-4 max-w-[480px] text-[16px] leading-relaxed text-muted sm:mt-5 sm:text-[17px]">
            Relational data, native JSON, full-text, vectors, and geo share one WAL,
            one MVCC, and one optimizer. Install the engine and run it.
          </p>
          <div className="mt-7 grid w-full max-w-[22rem] grid-cols-2 gap-3 sm:mt-8 sm:flex sm:w-auto sm:max-w-none sm:flex-wrap sm:justify-center lg:justify-start">
            <Link href="/docs/quick-start" className={buttonClassName({ size: "lg", className: "col-span-2 w-full sm:w-auto" })}>
              Get started
              <ArrowIcon />
            </Link>
            <Link href="/download" className={buttonClassName({ variant: "outline", size: "lg", className: "w-full sm:w-auto" })}>
              Download
            </Link>
            <Link href="/docs/introduction" className={buttonClassName({ variant: "outline", size: "lg", className: "w-full sm:w-auto" })}>
              Documentation
            </Link>
          </div>
          <p className="mt-6 max-w-md font-mono text-[12px] leading-5 text-faint">
            Not PostgreSQL, MySQL, MongoDB, or a vector-store compatibility layer.
            Native format, dialect, protocol, and drivers.
          </p>
        </div>
        <div className="min-w-0 max-w-full shadow-xl shadow-blue-900/20">
          <CodePanel title="hybrid.sql">{HYBRID_SQL}</CodePanel>
        </div>
      </div>
    </section>
  );
}

function SpecStrip() {
  return (
    <section className="border-y border-line">
      <div className="mx-auto grid max-w-6xl grid-cols-2 sm:grid-cols-3 lg:grid-cols-6">
        {specs.map((spec) => (
          <div
            key={spec.k}
            className="border-line border-b border-r px-4 py-4 even:border-r-0 sm:px-5 sm:py-5 sm:even:border-r sm:[&:nth-child(3n)]:border-r-0 lg:border-b-0 lg:[&:nth-child(6n)]:border-r-0"
          >
            <p className="font-mono text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">{spec.k}</p>
            <p className="mt-1.5 text-sm font-semibold tracking-tight">{spec.v}</p>
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
          <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
            Platform
          </p>
          <h2 className="mt-3 text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
            Built as one engine. Operated with the safety still on.
          </h2>
        </div>
        <ol className="mt-10 grid gap-4 md:grid-cols-2">
          {features.map((feature) => (
            <li key={feature.n}>
              <Card variant="bordered" className="h-full p-4 transition-colors hover:border-blue-500/40 sm:p-5 dark:hover:border-blue-400/40">
                <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500/80 dark:text-blue-400/80">
                  {feature.n}
                </span>
                <h3 className="mt-2 text-[1.02rem] font-semibold tracking-tight">{feature.title}</h3>
                <p className="mt-1.5 text-sm leading-6 text-muted">{feature.body}</p>
              </Card>
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
        <div className="overflow-hidden rounded-lg border border-black/[0.09] dark:border-white/[0.09] lg:grid lg:grid-cols-2">
          <div className="border-line px-4 py-10 sm:px-8 sm:py-14 lg:border-r">
            <p className="font-mono text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
              multimodel
            </p>
            <h2 className="mt-3 text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
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
              className="mt-8 inline-flex text-sm font-medium text-blue-600 hover:underline dark:text-blue-400"
            >
              Hybrid queries
            </Link>
          </div>
          <div className="flex flex-col justify-end overflow-x-auto bg-[#0a1427] px-5 py-10 font-mono text-[12.5px] leading-7 text-slate-200 sm:px-8 sm:py-12 sm:text-[13px] dark:bg-navy-800">
            <p className="text-slate-500"># same table, same transaction</p>
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
            <p className="mt-8 text-slate-500">EXPLAIN → Candidates, Rerank bm25+vector</p>
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
        <h2 className="max-w-2xl text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
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
    "Native wire protocol → TLS 1.3 → authn → authz",
    "SQL parser → binder / catalog → planner → cost optimizer",
    "Vectorized executor: relational · JSON · vector · full-text · geo",
    "MVCC + row/range locks + UNDO",
    "REDO WAL (group commit, fsync)",
    "Buffer manager → AES-256-GCM sealed pages",
  ];
  return (
    <section className="border-b border-line bg-bg-elev">
      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-12 sm:gap-10 sm:px-5 sm:py-16 lg:grid-cols-12 lg:py-20">
        <div className="lg:col-span-5">
          <h2 className="text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
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
              <span className="text-blue-500 dark:text-blue-400">{String(i + 1).padStart(2, "0")}</span>
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
    { runtime: "Go", path: "drivers/go", open: "nextsql.Open(nextsql.Config{…})" },
    { runtime: "Node.js 18+", path: "drivers/node", open: "connect({ address, user, password, tls })" },
    { runtime: "Bun", path: "drivers/bun", open: "same shape as Node" },
    { runtime: "Deno", path: "drivers/deno", open: 'import { connect } from "./mod.ts"' },
    { runtime: "PHP 8.1+", path: "drivers/php", open: "NextSQL\\Client::connect([…])" },
  ];
  return (
    <section className="border-b border-line">
      <div className="mx-auto max-w-6xl px-4 py-12 sm:px-5 sm:py-16 lg:py-20">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <h2 className="text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
            Native NSQL. No keys in the URL.
          </h2>
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
                <TableHead>Path</TableHead>
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
          <h2 className="text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
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
          <Link href="/docs/quick-start" className={buttonClassName({ size: "lg", className: "mt-8" })}>
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
        <Card variant="elevated" className="border-blue-500/20 px-5 py-10 sm:px-10 sm:py-12">
          <Badge variant="info" dot pulse>
            Engine complete
          </Badge>
          <h2 className="mt-5 max-w-2xl text-[1.85rem] font-bold tracking-[-0.035em] sm:text-[2.15rem]">
            The database is built. Run it on your machine.
          </h2>
          <p className="mt-4 max-w-2xl text-sm leading-6 text-muted">
            Storage, WAL, MVCC, SQL, optimizer, protocol, JSON, full-text, vectors,
            hybrid plans, security, backup/PITR/export, and Raft HA ship in{" "}
            <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsql</code> and{" "}
            <code className="rounded bg-bg-hover px-1 font-mono text-[12px]">nextsqld</code>.
            Install the binaries, initialize a data directory, and start serving NSQL.
          </p>
          <div className="mt-8 flex flex-wrap gap-2">
            <Link href="/docs/quick-start" className={buttonClassName({ size: "sm" })}>
              Get started
              <ArrowIcon />
            </Link>
            <Link href="/docs/install" className={buttonClassName({ variant: "outline", size: "sm" })}>
              Install
            </Link>
            <Link href="/docs/limits" className={buttonClassName({ variant: "outline", size: "sm" })}>
              Limits
            </Link>
          </div>
        </Card>
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
