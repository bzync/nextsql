"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { Logo } from "./Logo";
import { ThemeToggle } from "./ThemeToggle";
import { Button, Kbd } from "@bzync/rui";

export function SiteHeader({
  onSearch,
  onMenu,
  menuOpen: menuOpenProp,
}: {
  onSearch?: () => void;
  onMenu?: () => void;
  menuOpen?: boolean;
}) {
  const pathname = usePathname();
  const inDocs = pathname.startsWith("/docs");
  const inDownload = pathname.startsWith("/download");
  const [open, setOpen] = useState(false);
  const menuOpen = onMenu ? !!menuOpenProp : open;

  const openMenu = () => {
    if (onMenu) onMenu();
    else setOpen(true);
  };

  useEffect(() => {
    if (!menuOpen) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !onMenu) setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = previous;
      window.removeEventListener("keydown", onKey);
    };
  }, [menuOpen, onMenu]);

  const showOverlay = !onMenu && open;

  return (
    <>
    <header className="portal-topbar sticky top-0 z-50 border-b border-black/[0.07] pt-[env(safe-area-inset-top)] dark:border-white/[0.06]">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-2 px-4 sm:gap-6 sm:px-5">
        <div className="flex min-w-0 items-center gap-1.5">
          <button
            type="button"
            onClick={openMenu}
            className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-slate-600 transition-colors hover:bg-black/5 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/6 dark:hover:text-white lg:hidden"
            aria-label="Open menu"
            aria-expanded={menuOpen}
          >
            <MenuIcon />
          </button>
          <Logo />
        </div>
        <nav className="flex items-center gap-0.5 text-[13px] sm:gap-1">
          <div className="hidden items-center gap-0.5 lg:flex">
            <NavLink href="/docs/introduction" active={inDocs}>
              Docs
            </NavLink>
            <NavLink href="/download" active={inDownload}>
              Download
            </NavLink>
            <NavLink href="/docs/drivers">Drivers</NavLink>
          </div>
          {onSearch ? (
            <>
              <button
                type="button"
                onClick={onSearch}
                className="inline-flex h-10 w-10 items-center justify-center rounded-lg text-slate-600 transition-colors hover:bg-black/5 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/6 dark:hover:text-white sm:hidden"
                aria-label="Search documentation"
              >
                <SearchIcon />
              </button>
              <button
                type="button"
                onClick={onSearch}
                className="ml-1 hidden h-8 items-center gap-2 rounded-lg border border-slate-300/80 bg-white px-2.5 text-[12px] text-slate-600 transition-all hover:border-slate-400/80 hover:text-slate-900 dark:border-white/[0.09] dark:bg-white/[0.045] dark:text-slate-400 dark:hover:border-white/[0.16] dark:hover:text-slate-200 sm:inline-flex"
              >
                Search
                <Kbd keys="/" />
              </button>
            </>
          ) : null}
          <ThemeToggle />
          <div className="ml-1 hidden sm:block">
            <Button asChild size="sm">
              <Link href="/docs/quick-start">Get started</Link>
            </Button>
          </div>
        </nav>
      </div>
    </header>
    {showOverlay ? (
      <div className="fixed inset-0 z-[60] bg-bg pt-[env(safe-area-inset-top)] lg:hidden">
        <div className="flex h-14 items-center justify-between border-b border-black/[0.07] px-4 dark:border-white/[0.06]">
          <Logo />
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="inline-flex h-10 w-10 items-center justify-center rounded-lg text-slate-500 hover:bg-black/5 hover:text-slate-900 dark:hover:bg-white/6 dark:hover:text-white"
            aria-label="Close menu"
          >
            <CloseIcon />
          </button>
        </div>
        <nav className="flex flex-col px-3 py-4">
          {[
            ["/docs/introduction", "Documentation"],
            ["/download", "Download"],
            ["/docs/quick-start", "Get started"],
            ["/docs/install", "Install"],
            ["/docs/drivers", "Drivers"],
          ].map(([href, label]) => (
            <Link
              key={href}
              href={href}
              onClick={() => setOpen(false)}
              className="flex min-h-11 items-center rounded-lg px-3 text-[15px] text-slate-700 hover:bg-black/6 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-white/6 dark:hover:text-white"
            >
              {label}
            </Link>
          ))}
        </nav>
      </div>
    ) : null}
    </>
  );
}

function NavLink({
  href,
  children,
  active,
  external,
}: {
  href: string;
  children: React.ReactNode;
  active?: boolean;
  external?: boolean;
}) {
  const className = active
    ? "flex h-8 items-center rounded-lg px-3 text-sm font-medium bg-black/[0.06] text-slate-900 dark:bg-white/[0.08] dark:text-white"
    : "flex h-8 items-center rounded-lg px-3 text-sm font-medium text-slate-600 transition-colors hover:bg-black/5 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/5 dark:hover:text-white";
  if (external) {
    return (
      <a href={href} className={className} target="_blank" rel="noreferrer">
        {children}
      </a>
    );
  }
  return (
    <Link href={href} className={className}>
      {children}
    </Link>
  );
}

function MenuIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="11" cy="11" r="8" />
      <path d="m21 21-4.3-4.3" />
    </svg>
  );
}
