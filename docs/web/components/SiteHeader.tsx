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

  // Only lock body scroll for the overlay this component owns. In docs mode
  // (`onMenu` set) DocsChrome renders the drawer and owns the scroll lock;
  // running a second save/restore here nests with DocsChrome's and can leave
  // `body { overflow: hidden }` stuck after the menu closes.
  useEffect(() => {
    if (onMenu || !open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = previous;
      window.removeEventListener("keydown", onKey);
    };
  }, [open, onMenu]);

  const showOverlay = !onMenu && open;

  return (
    <>
    <header className="portal-topbar sticky top-0 z-50 border-b pt-[env(safe-area-inset-top)]">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-2 px-4 sm:gap-6 sm:px-5">
        <div className="flex min-w-0 items-center gap-1.5">
          <button
            type="button"
            onClick={openMenu}
            className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-muted transition-colors hover:bg-bg-hover hover:text-foreground lg:hidden"
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
                className="inline-flex h-10 w-10 items-center justify-center rounded-md text-muted transition-colors hover:bg-bg-hover hover:text-foreground md:hidden"
                aria-label="Search documentation"
              >
                <SearchIcon />
              </button>
              <button
                type="button"
                onClick={onSearch}
                className="ml-1 hidden h-8 w-[12.5rem] items-center gap-2 rounded-md border border-line bg-bg-elev px-2.5 text-[12.5px] text-muted transition-colors hover:border-line-strong hover:text-foreground md:inline-flex"
              >
                <span className="flex-1 text-left">Search docs</span>
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
        <div className="flex h-14 items-center justify-between border-b border-line px-4">
          <Logo />
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="inline-flex h-10 w-10 items-center justify-center rounded-md text-muted hover:bg-bg-hover hover:text-foreground"
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
              className="flex min-h-11 items-center rounded-md px-3 text-[15px] text-muted hover:bg-bg-hover hover:text-foreground"
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
    ? "flex h-8 items-center rounded-md px-3 text-sm font-medium text-foreground"
    : "flex h-8 items-center rounded-md px-3 text-sm font-medium text-muted transition-colors hover:bg-bg-hover hover:text-foreground";
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
