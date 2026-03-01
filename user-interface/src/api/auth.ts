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

const TOKEN_KEY = "roboslop_token";

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
  return res;
}

export async function getMe(): Promise<User> {
  return apiGet<User>("/api/users/me");
}

export async function getUsers(): Promise<User[]> {
  return apiGet<User[]>("/api/users");
}
