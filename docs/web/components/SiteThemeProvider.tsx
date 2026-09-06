"use client";

import { ThemeProvider } from "@bzync/rui";
import { useEffect, useState, type ReactNode } from "react";
import { themePalette, themeLightPalette, themeDarkPalette } from "@/lib/theme-palette";

const STORAGE_KEY = "nextsql-theme";

export function SiteThemeProvider({ children }: { children: ReactNode }) {
  // Always start dark so SSR HTML matches the first client render. The saved
  // preference is applied after mount to avoid a hydration mismatch.
  const [theme, setTheme] = useState<"light" | "dark">("dark");

  useEffect(() => {
    const query = new URLSearchParams(window.location.search).get("theme");
    const stored = window.localStorage.getItem(STORAGE_KEY);
    const next = query === "light" || query === "dark" ? query : stored === "light" || stored === "dark" ? stored : "dark";
    setTheme(next);
  }, []);

  return (
    <ThemeProvider
      theme={theme}
      onThemeChange={(next) => {
        if (next === "light" || next === "dark") setTheme(next);
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
