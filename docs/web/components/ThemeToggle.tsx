"use client";

import { useSyncExternalStore } from "react";
import { ThemeToggle as RuiThemeToggle } from "@bzync/rui";

const subscribeHydration = () => () => {};

export function ThemeToggle({ className = "" }: { className?: string }) {
  const mounted = useSyncExternalStore(subscribeHydration, () => true, () => false);

  if (!mounted) {
    return <span className="inline-flex h-10 w-10 sm:h-9 sm:w-9" aria-hidden="true" />;
  }

  return (
    <RuiThemeToggle
      lightIcon={<SunIcon />}
      darkIcon={<MoonIcon />}
      showLabel={false}
      lightLabel="Toggle theme"
      darkLabel="Toggle theme"
      className={`h-10 w-10 rounded-md border-transparent bg-transparent px-0 text-muted shadow-none hover:border-transparent hover:bg-bg-hover hover:text-foreground sm:h-9 sm:w-9 ${className}`}
    />
  );
}

function SunIcon({ className }: { className?: string }) {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
    </svg>
  );
}

function MoonIcon({ className }: { className?: string }) {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d="M21 14.5A8.5 8.5 0 1 1 9.5 3 7 7 0 0 0 21 14.5z" />
    </svg>
  );
}
