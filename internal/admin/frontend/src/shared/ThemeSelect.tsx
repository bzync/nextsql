import { useTheme } from "@bzync/rui";
import { Icon } from "./icons";

const THEME_OPTIONS = [
  { value: "system", label: "Use system theme", icon: "device" },
  { value: "light", label: "Use light theme", icon: "sun" },
  { value: "dark", label: "Use dark theme", icon: "moon" },
] as const;

// Theme choice is deliberately a three-state icon group: unlike a two-state
// sun/moon toggle, it can return the operator to following the OS preference.
export function ThemeSelect() {
  const { theme, setTheme } = useTheme();

  return (
    <div className="nsa-theme-toggle" role="group" aria-label="Color theme">
      {THEME_OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          className="nsa-theme-button"
          aria-label={option.label}
          title={option.label}
          aria-pressed={theme === option.value}
          onClick={() => setTheme(option.value)}
        >
          <Icon name={option.icon} size={16} />
        </button>
      ))}
    </div>
  );
}
