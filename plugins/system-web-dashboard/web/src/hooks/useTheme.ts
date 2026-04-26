import { create } from "zustand";

/**
 * Theming model:
 *   - `mode` toggles light / dark (presence of `.dark` class on <html>)
 *   - `baseColor` is null (use default palette in :root / .dark) or a custom
 *     theme id (overrides via `[data-theme="<id>"]` rules injected at runtime)
 *
 * Default palette lives in src/index.css under `:root` and `.dark`. Custom
 * themes are stored in localStorage and injected via a <style id="ta-custom-themes">
 * element appended to <head>.
 */

export type BaseColor = string | null;
export type Mode = "light" | "dark";

export interface CustomTheme {
  id: string;
  label: string;
  /** CSS variable declarations only (no selector wrapper), e.g. "--background: 0 0% 100%; ..." */
  lightVars: string;
  darkVars: string;
}

const COLOR_KEY = "teamagentica-base-color";
const MODE_KEY = "teamagentica-mode";
const CUSTOM_KEY = "teamagentica-custom-themes";
const STYLE_ID = "ta-custom-themes";

function loadCustomThemes(): CustomTheme[] {
  try {
    const raw = localStorage.getItem(CUSTOM_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (t): t is CustomTheme =>
        t && typeof t.id === "string" && typeof t.label === "string" &&
        typeof t.lightVars === "string" && typeof t.darkVars === "string"
    );
  } catch {
    return [];
  }
}

function persistCustomThemes(themes: CustomTheme[]) {
  localStorage.setItem(CUSTOM_KEY, JSON.stringify(themes));
}

function injectCustomThemes(themes: CustomTheme[]) {
  let style = document.getElementById(STYLE_ID) as HTMLStyleElement | null;
  if (!style) {
    style = document.createElement("style");
    style.id = STYLE_ID;
    document.head.appendChild(style);
  }
  style.textContent = themes
    .map(
      (t) =>
        `[data-theme="${t.id}"] { ${t.lightVars} }\n` +
        `[data-theme="${t.id}"].dark { ${t.darkVars} }`
    )
    .join("\n");
}

/**
 * Parse a pasted shadcn theme CSS block into light + dark variable bodies.
 *
 * Accepts the full block tweakcn / ui.shadcn.com copy: ` :root { ... } .dark { ... } `.
 * Returns the inner declarations only (no selectors, no braces) so we can wrap
 * them in [data-theme="..."] selectors of our own.
 */
export function parseThemeCss(css: string): { lightVars: string; darkVars: string } {
  const grab = (selectorRe: RegExp): string => {
    const m = css.match(selectorRe);
    if (!m) return "";
    return m[1].trim().replace(/\s+/g, " ");
  };
  const lightVars = grab(/:root\s*\{([^}]*)\}/) || grab(/\.light\s*\{([^}]*)\}/);
  const darkVars = grab(/\.dark\s*\{([^}]*)\}/);
  return { lightVars, darkVars };
}

const KEYS_WE_USE = new Set([
  "background", "foreground",
  "card", "card-foreground",
  "popover", "popover-foreground",
  "primary", "primary-foreground",
  "secondary", "secondary-foreground",
  "muted", "muted-foreground",
  "accent", "accent-foreground",
  "destructive", "destructive-foreground",
  "border", "input", "ring",
]);

function stylesObjectToVars(styles: Record<string, unknown>): string {
  return Object.entries(styles)
    .filter(([k, v]) => KEYS_WE_USE.has(k) && typeof v === "string" && v.length > 0)
    .map(([k, v]) => `--${k}: ${v};`)
    .join(" ");
}

/**
 * Parse a tweakcn theme HTML page. Tweakcn embeds the theme in the streamed
 * Next.js payload as escaped JSON: `\"name\":\"...\",...,\"styles\":{\"light\":{...},\"dark\":{...}}`.
 * We unescape and grab the styles blocks.
 */
export function parseTweakcnHtml(html: string): { label: string; lightVars: string; darkVars: string } | null {
  const unescaped = html.replace(/\\"/g, '"').replace(/\\n/g, " ");
  const labelMatch = unescaped.match(/"name":"([^"]{1,80})","[^"]*styles":/);
  const stylesMatch = unescaped.match(/"styles":\{("light":\{[^}]*\},"dark":\{[^}]*\})\}/);
  if (!stylesMatch) return null;
  let parsed: { light?: Record<string, unknown>; dark?: Record<string, unknown> };
  try {
    parsed = JSON.parse("{" + stylesMatch[1] + "}");
  } catch {
    return null;
  }
  const lightVars = parsed.light ? stylesObjectToVars(parsed.light) : "";
  const darkVars = parsed.dark ? stylesObjectToVars(parsed.dark) : "";
  if (!lightVars && !darkVars) return null;
  return {
    label: labelMatch ? labelMatch[1] : "Imported theme",
    lightVars,
    darkVars,
  };
}

/**
 * Fetch a tweakcn theme URL via the system-web-dashboard plugin's /api/fetch
 * endpoint. Browsers cannot fetch tweakcn directly because of CORS; the plugin
 * runs the request server-side and returns the HTML to us.
 */
export async function fetchTweakcnHtml(url: string): Promise<string> {
  const { API_BASE } = await import("@/api/client");
  const proxyUrl = `${API_BASE}/api/fetch?url=${encodeURIComponent(url)}`;
  const res = await fetch(proxyUrl);
  if (!res.ok) {
    let detail = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      if (body?.error) detail = body.error;
    } catch { /* not json */ }
    throw new Error(`Server-side fetch failed: ${detail}`);
  }
  const text = await res.text();
  if (text.length < 1000 || !(text.includes('\\"styles\\":') || text.includes('"styles":'))) {
    throw new Error("Fetched page did not contain a theme payload (URL might not be a tweakcn theme page).");
  }
  return text;
}

export function slugify(label: string): string {
  return label.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

interface ThemeStore {
  baseColor: BaseColor;
  mode: Mode;
  customThemes: CustomTheme[];
  setBaseColor: (id: BaseColor) => void;
  setMode: (m: Mode) => void;
  toggleMode: () => void;
  addTheme: (t: CustomTheme) => void;
  removeTheme: (id: string) => void;
}

const initialBaseColor = ((): BaseColor => {
  const saved = localStorage.getItem(COLOR_KEY);
  if (!saved) return null;
  const customIds = new Set(loadCustomThemes().map((t) => t.id));
  return customIds.has(saved) ? saved : null;
})();

const initialMode: Mode = ((): Mode => {
  const saved = localStorage.getItem(MODE_KEY);
  if (saved === "light" || saved === "dark") return saved;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
})();

const useThemeStore = create<ThemeStore>((set, get) => ({
  baseColor: initialBaseColor,
  mode: initialMode,
  customThemes: loadCustomThemes(),

  setBaseColor: (id) => set({ baseColor: id }),
  setMode: (m) => set({ mode: m }),
  toggleMode: () => set({ mode: get().mode === "dark" ? "light" : "dark" }),

  addTheme: (t) => {
    const next = [...get().customThemes.filter((p) => p.id !== t.id), t];
    persistCustomThemes(next);
    set({ customThemes: next });
  },

  removeTheme: (id) => {
    const next = get().customThemes.filter((p) => p.id !== id);
    persistCustomThemes(next);
    const patch: Partial<ThemeStore> = { customThemes: next };
    if (get().baseColor === id) patch.baseColor = null;
    set(patch);
  },
}));

// Apply DOM side effects whenever store state changes. Subscribe once at module
// load so the html element stays in sync regardless of which component is mounted.
useThemeStore.subscribe((state, prev) => {
  if (state.customThemes !== prev.customThemes) injectCustomThemes(state.customThemes);
  if (state.baseColor !== prev.baseColor) {
    if (state.baseColor) {
      document.documentElement.dataset.theme = state.baseColor;
      localStorage.setItem(COLOR_KEY, state.baseColor);
    } else {
      delete document.documentElement.dataset.theme;
      localStorage.removeItem(COLOR_KEY);
    }
  }
  if (state.mode !== prev.mode) {
    document.documentElement.classList.toggle("dark", state.mode === "dark");
    localStorage.setItem(MODE_KEY, state.mode);
  }
});

// Apply initial state to the DOM on module load.
injectCustomThemes(useThemeStore.getState().customThemes);
if (initialBaseColor) document.documentElement.dataset.theme = initialBaseColor;
document.documentElement.classList.toggle("dark", initialMode === "dark");

export const useTheme = useThemeStore;
