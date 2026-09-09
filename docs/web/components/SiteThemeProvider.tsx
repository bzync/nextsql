"use client";

import { ThemeProvider } from "@bzync/rui";
import { useSyncExternalStore, type ReactNode } from "react";
import { themePalette, themeLightPalette, themeDarkPalette } from "@/lib/theme-palette";

const STORAGE_KEY = "nextsql-theme";
const THEME_CHANGE_EVENT = "nextsql-theme-change";

function resolveTheme(): "light" | "dark" {
  const query = new URLSearchParams(window.location.search).get("theme");
  const stored = window.localStorage.getItem(STORAGE_KEY);
  return query === "light" || query === "dark" ? query : stored === "light" || stored === "dark" ? stored : "dark";
}

function subscribeTheme(change: () => void) {
  window.addEventListener("storage", change);
  window.addEventListener("popstate", change);
  window.addEventListener(THEME_CHANGE_EVENT, change);
  return () => {
    window.removeEventListener("storage", change);
    window.removeEventListener("popstate", change);
    window.removeEventListener(THEME_CHANGE_EVENT, change);
  };
}

export function SiteThemeProvider({ children }: { children: ReactNode }) {
  // The server snapshot keeps SSR deterministic. React refreshes from the
  // browser snapshot immediately after hydration without a setState effect.
  const theme = useSyncExternalStore<"light" | "dark">(subscribeTheme, resolveTheme, () => "dark");

  return (
    <ThemeProvider
      theme={theme}
      onThemeChange={(next) => {
        if (next !== "light" && next !== "dark") return;
        window.localStorage.setItem(STORAGE_KEY, next);
        window.dispatchEvent(new Event(THEME_CHANGE_EVENT));
      }}
      defaultTheme="dark"
      storageKey={STORAGE_KEY}
      applyToRoot
      palette={themePalette}
      lightPalette={themeLightPalette}
      darkPalette={themeDarkPalette}
      className="contents"
      suppressHydrationWarning
    >
      {children}
    </ThemeProvider>
  );
}
