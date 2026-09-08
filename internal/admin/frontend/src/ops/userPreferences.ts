import { useEffect, useState } from "react";

export type UserPreferences = {
  density: "compact" | "comfortable";
  autoRefreshInterval: number; // 0 = off, 5, 15, 30, 60 seconds
  defaultPageSize: number; // 10, 25, 50, 100
  confirmDestructive: boolean;
};

export const DEFAULT_PREFERENCES: UserPreferences = {
  density: "compact",
  autoRefreshInterval: 0,
  defaultPageSize: 10,
  confirmDestructive: true,
};

const PREFERENCES_STORAGE_KEY = "nextsql-admin-user-preferences";
const PREFERENCES_CHANGE_EVENT = "nsa-user-preferences-change";

export function getUserPreferences(): UserPreferences {
  try {
    const raw = window.localStorage.getItem(PREFERENCES_STORAGE_KEY);
    if (!raw) return DEFAULT_PREFERENCES;
    const parsed = JSON.parse(raw);
    return {
      density: parsed.density === "comfortable" ? "comfortable" : "compact",
      autoRefreshInterval: typeof parsed.autoRefreshInterval === "number" ? parsed.autoRefreshInterval : 0,
      defaultPageSize: [10, 25, 50, 100].includes(parsed.defaultPageSize) ? parsed.defaultPageSize : 10,
      confirmDestructive: typeof parsed.confirmDestructive === "boolean" ? parsed.confirmDestructive : true,
    };
  } catch {
    return DEFAULT_PREFERENCES;
  }
}

export function setUserPreferences(patch: Partial<UserPreferences>): UserPreferences {
  const current = getUserPreferences();
  const next: UserPreferences = {
    ...current,
    ...patch,
  };
  try {
    window.localStorage.setItem(PREFERENCES_STORAGE_KEY, JSON.stringify(next));
    window.dispatchEvent(new CustomEvent(PREFERENCES_CHANGE_EVENT, { detail: next }));
  } catch {
    // Storage unavailable, in-memory change still propagates
  }
  return next;
}

export function useUserPreferences(): {
  preferences: UserPreferences;
  updatePreferences: (patch: Partial<UserPreferences>) => void;
} {
  const [preferences, setPreferences] = useState<UserPreferences>(getUserPreferences);

  useEffect(() => {
    const handler = (e: Event) => {
      const custom = e as CustomEvent<UserPreferences>;
      if (custom.detail) {
        setPreferences(custom.detail);
      } else {
        setPreferences(getUserPreferences());
      }
    };
    window.addEventListener(PREFERENCES_CHANGE_EVENT, handler);
    return () => window.removeEventListener(PREFERENCES_CHANGE_EVENT, handler);
  }, []);

  return {
    preferences,
    updatePreferences: setUserPreferences,
  };
}
