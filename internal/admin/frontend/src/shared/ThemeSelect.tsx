import { ToggleGroup, ToggleGroupItem, useTheme } from "@bzync/rui";
import { Icon } from "./icons";

const THEME_OPTIONS = [
  { value: "system", label: "Use system theme", icon: "device" },
  { value: "light", label: "Use light theme", icon: "sun" },
  { value: "dark", label: "Use dark theme", icon: "moon" },
] as const;

type ThemeChoice = (typeof THEME_OPTIONS)[number]["value"];

function isThemeChoice(value: string): value is ThemeChoice {
  return THEME_OPTIONS.some((option) => option.value === value);
}

// Theme choice is a three-state group: unlike a two-state sun/moon toggle,
// it can return the operator to following the OS preference.
export function ThemeSelect() {
  const { theme, setTheme } = useTheme();

  return (
    <ToggleGroup
      className="nsa-theme-toggle"
      type="single"
      size="icon"
      variant="outline"
      value={theme}
      aria-label="Color theme"
      onValueChange={(next) => {
        if (isThemeChoice(next)) setTheme(next);
      }}
    >
      {THEME_OPTIONS.map((option) => (
        <ToggleGroupItem
          key={option.value}
          value={option.value}
          className="nsa-theme-button"
          aria-label={option.label}
          title={option.label}
        >
          <Icon name={option.icon} size={16} />
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
