import { TeamAgenticaClient } from "@teamagentica/api-client";

export const API_BASE =
  import.meta.env.VITE_TEAMAGENTICA_KERNEL_URL ||
  `//${import.meta.env.VITE_TEAMAGENTICA_KERNEL_HOST || "api.teamagentica.localhost"}`;

const TOKEN_KEY = "teamagentica_token";
const REFRESH_TOKEN_KEY = "teamagentica_refresh_token";

let onUnauthorizedCb: (() => void) | null = null;

export function setOnUnauthorized(cb: () => void): void {
  onUnauthorizedCb = cb;
}

export const apiClient = new TeamAgenticaClient({
  baseUrl: API_BASE,
  getToken: () => localStorage.getItem(TOKEN_KEY),
  getRefreshToken: () => localStorage.getItem(REFRESH_TOKEN_KEY),
  onTokenRefreshed: (token: string, refreshToken: string) => {
    localStorage.setItem(TOKEN_KEY, token);
    if (refreshToken) localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken);
  },
  onUnauthorized: () => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
    onUnauthorizedCb?.();
  },
});
