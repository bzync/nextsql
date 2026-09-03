import Link from "next/link";
import { Button, EmptyState } from "@bzync/rui";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";

export default function NotFound() {
  return (
    <div className="min-h-full">
      <SiteHeader />
      <main id="content" className="mx-auto flex max-w-lg flex-col items-center px-5 py-24">
        <EmptyState
          icon={
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 8v4M12 16h.01" />
            </svg>
          }
          title="Page not found"
          description="That route is not part of the NextSQL docs."
          action={
            <div className="flex gap-2">
              <Button asChild size="sm">
                <Link href="/">Home</Link>
              </Button>
              <Button asChild variant="outline" size="sm">
                <Link href="/docs/introduction">Documentation</Link>
              </Button>
            </div>
          }
        />
      </main>
      <SiteFooter />
    </div>
  );
}
