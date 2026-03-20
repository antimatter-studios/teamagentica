import { useState, useEffect, useCallback } from "react";

export type Page = "dashboard" | "chat" | "code" | "files" | "tasks" | "scheduler" | "agents" | "marketplace" | "plugins" | "costs" | "console" | "users";

const VALID_PAGES = new Set<Page>([
  "dashboard", "chat", "code", "files", "tasks", "scheduler",
  "agents", "marketplace", "plugins", "costs", "console", "users",
]);

function parsePath(pathname: string): { page: Page; subpath: string } {
  const stripped = pathname.replace(/^\/+/, "");
  const slashIdx = stripped.indexOf("/");
  const segment = (slashIdx === -1 ? stripped : stripped.slice(0, slashIdx)).toLowerCase();
  const page: Page = VALID_PAGES.has(segment as Page) ? (segment as Page) : "dashboard";
  const subpath = slashIdx === -1 ? "" : stripped.slice(slashIdx + 1);
  return { page, subpath };
}

function buildPath(page: Page, subpath: string): string {
  if (page === "dashboard" && !subpath) return "/";
  if (!subpath) return `/${page}`;
  return `/${page}/${subpath}`;
}

export function useRouter() {
  const [page, setPageState] = useState<Page>(() => parsePath(window.location.pathname).page);
  const [subpath, setSubpathState] = useState<string>(() => parsePath(window.location.pathname).subpath);

  const navigate = useCallback((newPage: Page) => {
    // Navigating to a new page clears the subpath.
    const path = buildPath(newPage, "");
    if (window.location.pathname !== path) {
      window.history.pushState(null, "", path);
    }
    setPageState(newPage);
    setSubpathState("");
  }, []);

  const setSubpath = useCallback((newSubpath: string) => {
    setSubpathState((prev) => {
      if (prev === newSubpath) return prev;
      setPageState((currentPage) => {
        const path = buildPath(currentPage, newSubpath);
        if (window.location.pathname !== path) {
          window.history.replaceState(null, "", path);
        }
        return currentPage;
      });
      return newSubpath;
    });
  }, []);

  // Update page title.
  useEffect(() => {
    const appName = import.meta.env.VITE_APP_NAME || "TeamAgentica";
    const label = page.charAt(0).toUpperCase() + page.slice(1);
    document.title = `${appName} | ${label}`;
  }, [page]);

  // Handle browser back/forward buttons.
  useEffect(() => {
    const onPopState = () => {
      const { page: p, subpath: s } = parsePath(window.location.pathname);
      setPageState(p);
      setSubpathState(s);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  return { page, subpath, navigate, setSubpath } as const;
}
