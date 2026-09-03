"use client";

import { ThemeProvider } from "@bzync/rui";
import type { ReactNode } from "react";
import { themePalette, themeLightPalette, themeDarkPalette } from "@/lib/theme-palette";

const STORAGE_KEY = "nextsql-theme";

export function SiteThemeProvider({ children }: { children: ReactNode }) {
  return (
    <ThemeProvider
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
