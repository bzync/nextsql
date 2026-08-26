import Link from "next/link";
import { Logo } from "./Logo";
import { site } from "@/lib/site";

export function SiteFooter() {
  return (
    <footer className="border-t border-line">
      <div className="mx-auto grid max-w-6xl gap-10 px-4 py-10 sm:px-5 sm:py-12 md:grid-cols-12">
        <div className="md:col-span-6">
          <Logo />
          <p className="mt-4 max-w-sm text-sm leading-6 text-muted">
            A multimodel database. SQL, JSON, vectors, full-text, and geo in one ACID
            engine — encrypted by default.
          </p>
        </div>
        <div className="md:col-span-3">
          <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">Docs</p>
          <ul className="mt-3 space-y-2 text-sm">
            <li>
              <Link href="/docs/introduction" className="text-muted transition-colors hover:text-foreground">
                Introduction
              </Link>
            </li>
            <li>
              <Link href="/docs/quick-start" className="text-muted transition-colors hover:text-foreground">
                Quick start
              </Link>
            </li>
            <li>
              <Link href="/docs/drivers" className="text-muted transition-colors hover:text-foreground">
                Drivers
              </Link>
            </li>
            <li>
              <Link href="/docs/architecture" className="text-muted transition-colors hover:text-foreground">
                Architecture
              </Link>
            </li>
          </ul>
        </div>
        <div className="md:col-span-3">
          <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">Product</p>
          <ul className="mt-3 space-y-2 text-sm">
            <li>
              <Link href="/download" className="text-muted transition-colors hover:text-foreground">
                Download
              </Link>
            </li>
            <li>
              <Link href="/docs/install" className="text-muted transition-colors hover:text-foreground">
                Install from source
              </Link>
            </li>
            <li>
              <Link href="/docs/quick-start" className="text-muted transition-colors hover:text-foreground">
                Quick start
              </Link>
            </li>
            <li>
              <Link href="/docs/sql" className="text-muted transition-colors hover:text-foreground">
                SQL dialect
              </Link>
            </li>
            <li className="font-mono text-xs text-faint">{site.version}</li>
          </ul>
        </div>
      </div>
      <div className="border-t border-line">
        <div className="mx-auto flex max-w-6xl flex-col gap-1 px-5 py-4 font-mono text-[11px] text-faint sm:flex-row sm:justify-between">
          <p>Not PostgreSQL · not a vector-store wrapper · NSQL v1</p>
          <p>{site.module}</p>
        </div>
      </div>
    </footer>
  );
}
