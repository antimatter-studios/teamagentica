import { useCallback, useEffect, useState } from "react";

export const THEMES = [
  { id: "soft-dark", label: "Soft Dark" },
  { id: "midnight", label: "Midnight Blue" },
  { id: "slate", label: "Slate" },
  { id: "dracula", label: "Dracula" },
  { id: "high-contrast", label: "High Contrast" },
  { id: "light", label: "Light" },
] as const;

export type ThemeId = (typeof THEMES)[number]["id"];

const STORAGE_KEY = "teamagentica-theme";

export function useTheme() {
  const [theme, setThemeState] = useState<ThemeId>(() => {
    const saved = localStorage.getItem(STORAGE_KEY);
    return (saved as ThemeId) || "soft-dark";
  });

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem(STORAGE_KEY, theme);
  }, [theme]);

  const setTheme = useCallback((id: ThemeId) => {
    setThemeState(id);
  }, []);

  return { theme, setTheme, themes: THEMES };
}
