import Link from "next/link";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";
import { buttonClassName } from "@/components/ui/button";

export default function NotFound() {
  return (
    <div className="min-h-full">
      <SiteHeader />
      <main id="content" className="mx-auto flex max-w-lg flex-col items-center px-5 py-24 text-center">
        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-xl border border-black/[0.07] bg-black/[0.04] dark:border-white/[0.07] dark:bg-white/[0.04]">
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" className="text-slate-400" aria-hidden="true">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 8v4M12 16h.01" />
          </svg>
        </div>
        <p className="text-sm font-semibold text-slate-900 dark:text-white">Page not found</p>
        <p className="mt-1.5 max-w-xs text-sm text-slate-500">That route is not part of the NextSQL docs.</p>
        <div className="mt-6 flex gap-2">
          <Link href="/" className={buttonClassName({ size: "sm" })}>
            Home
          </Link>
          <Link href="/docs/introduction" className={buttonClassName({ variant: "outline", size: "sm" })}>
            Documentation
          </Link>
        </div>
      </main>
      <SiteFooter />
    </div>
  );
}
