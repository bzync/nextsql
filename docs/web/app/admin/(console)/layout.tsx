import Link from "next/link";
import { redirect } from "next/navigation";
import { Logo } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/components/ThemeToggle";
import { isAdmin } from "@/lib/admin-auth";
import { logoutAction } from "@/app/admin/actions";

export const metadata = {
  title: "Admin",
  robots: { index: false, follow: false },
};

export default async function AdminConsoleLayout({ children }: { children: React.ReactNode }) {
  if (!(await isAdmin())) redirect("/admin/login");

  return (
    <div className="min-h-full">
      <header className="sticky top-0 z-40 border-b border-black/[0.07] bg-bg/90 backdrop-blur pt-[env(safe-area-inset-top)] dark:border-white/[0.06]">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4 sm:px-5">
          <div className="flex min-w-0 items-center gap-3">
            <Logo />
            <span className="hidden rounded-full border border-black/10 px-2 py-0.5 text-[11px] font-medium text-muted sm:inline dark:border-white/10">
              Admin
            </span>
          </div>
          <div className="flex items-center gap-1">
            <Link href="/download" className="hidden px-3 text-sm text-muted hover:text-foreground sm:inline">
              Downloads
            </Link>
            <Link href="/" className="hidden px-3 text-sm text-muted hover:text-foreground sm:inline">
              Site
            </Link>
            <ThemeToggle />
            <form action={logoutAction}>
              <Button type="submit" variant="ghost" size="sm">
                Sign out
              </Button>
            </form>
          </div>
        </div>
      </header>
      <main id="content" className="mx-auto max-w-6xl px-4 py-8 sm:px-5 sm:py-10">
        {children}
      </main>
    </div>
  );
}
