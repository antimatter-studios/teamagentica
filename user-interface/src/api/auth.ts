import { apiClient } from "./client";

export type { User, AuthResponse } from "@teamagentica/api-client";

const TOKEN_KEY = "teamagentica_token";

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function storeToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

export async function login(email: string, password: string) {
  const res = await apiClient.auth.login(email, password);
  storeToken(res.token);
  await apiClient.auth.createSession();
  return res;
}

export async function register(email: string, password: string, displayName: string) {
  const res = await apiClient.auth.register(email, password, displayName);
  storeToken(res.token);
  await apiClient.auth.createSession();
  return res;
}

export const getMe = () => apiClient.auth.getMe();
export const getUsers = () => apiClient.auth.getUsers();
