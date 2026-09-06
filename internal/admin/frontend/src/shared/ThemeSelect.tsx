import { Select, useTheme } from "@bzync/rui";

const THEME_OPTIONS = [
  { value: "system", label: "System theme" },
  { value: "light", label: "Light theme" },
  { value: "dark", label: "Dark theme" },
];

// Keep all three persisted theme choices explicit. A two-state sun/moon
// toggle cannot return to following the operating-system preference after
// the operator has selected a fixed theme.
export function ThemeSelect() {
  const { theme, setTheme } = useTheme();

  return (
    <Select
      id="nsa-color-theme"
      label="Color theme"
      labelClassName="sr-only"
      value={theme}
      options={THEME_OPTIONS}
      onChange={(next) => {
        if (next === "system" || next === "light" || next === "dark") setTheme(next);
      }}
      wrapperClassName="nsa-theme-select"
      triggerClassName="nsa-theme-trigger"
    />
  );
}
