import type { ThemePalette } from "@bzync/rui";

/**
 * Maps the site's existing brand colors (see app/globals.css) onto rui's
 * token system, so rui components render with the same palette as the
 * rest of the site instead of rui's own defaults.
 */
export const themePalette: ThemePalette = {
  accent: {
    50: "#eff6ff",
    100: "#dbeafe",
    200: "#bfdbfe",
    300: "#93c5fd",
    400: "#60a5fa",
    500: "#3b82f6",
    600: "#2563eb",
    700: "#1d4ed8",
    800: "#1e40af",
    900: "#1e3a8a",
    950: "#172554",
  },
  neutral: {
    50: "#f8fafc",
    100: "#f1f5f9",
    200: "#e2e8f0",
    300: "#cbd5e1",
    400: "#94a3b8",
    500: "#64748b",
    600: "#475569",
    700: "#334155",
    800: "#1e293b",
    900: "#0f172a",
    950: "#020617",
  },
  radius: {
    sm: "0.25rem",
    md: "0.375rem",
    lg: "0.5rem",
    xl: "0.75rem",
    "2xl": "1rem",
    full: "9999px",
  },
  fonts: {
    sans: "var(--font-geist), ui-sans-serif, system-ui, sans-serif",
    mono: "var(--font-geist-mono), ui-monospace, monospace",
    display: "var(--font-brand-face), ui-sans-serif, sans-serif",
  },
  colors: {
    primary: "#2563eb",
    primaryHover: "#3b82f6",
    primaryForeground: "#ffffff",
    danger: "#dc2626",
    dangerForeground: "#ffffff",
    success: "#10b981",
    warning: "#f59e0b",
    info: "#0ea5e9",
    focusRing: "#3b82f6",
  },
};

export const themeLightPalette: ThemePalette = {
  colors: {
    bg: "#f8fafc",
    surface: "#ffffff",
    surfaceRaised: "#ffffff",
    surfaceMuted: "rgba(15, 23, 42, 0.04)",
    border: "rgba(15, 23, 42, 0.08)",
    borderStrong: "rgba(15, 23, 42, 0.14)",
    text: "#0f172a",
    muted: "rgba(15, 23, 42, 0.05)",
    mutedForeground: "#64748b",
  },
};

export const themeDarkPalette: ThemePalette = {
  colors: {
    bg: "#040912",
    surface: "#060c1a",
    surfaceRaised: "#0a1427",
    surfaceMuted: "rgba(255, 255, 255, 0.06)",
    border: "rgba(255, 255, 255, 0.08)",
    borderStrong: "rgba(255, 255, 255, 0.14)",
    text: "#ffffff",
    muted: "rgba(255, 255, 255, 0.07)",
    mutedForeground: "#94a3b8",
  },
};
