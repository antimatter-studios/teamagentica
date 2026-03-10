import { apiPost, apiGet } from "./client";

export interface User {
  id: string;
  email: string;
  display_name: string;
  role: string;
  created_at: string;
}

interface AuthResponse {
  token: string;
  user: User;
}

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

export async function login(
  email: string,
  password: string
): Promise<AuthResponse> {
  const res = await apiPost<AuthResponse>("/api/auth/login", {
    email,
    password,
  });
  storeToken(res.token);
  // Create session cookie for same-origin iframe auth (workspace proxy).
  await createSession();
  return res;
}

export async function register(
  email: string,
  password: string,
  displayName: string
): Promise<AuthResponse> {
  const res = await apiPost<AuthResponse>("/api/auth/register", {
    email,
    password,
    display_name: displayName,
  });
  storeToken(res.token);
  await createSession();
  return res;
}

async function createSession(): Promise<void> {
  try {
    await apiPost("/api/auth/session", {});
  } catch {
    // Non-fatal — session cookie is a convenience for iframe auth.
  }
}

export async function getMe(): Promise<User> {
  const res = await apiGet<{ user: User }>("/api/users/me");
  return res.user;
}

export async function getUsers(): Promise<User[]> {
  return apiGet<User[]>("/api/users");
}
