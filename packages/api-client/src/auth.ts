import type { HttpTransport } from "./client.js";

export interface User {
  id: string;
  email: string;
  display_name: string;
  role: string;
  created_at: string;
}

export interface AuthResponse {
  token: string;
  user: User;
}

export class AuthAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async login(email: string, password: string): Promise<AuthResponse> {
    const res = await this.http.post<AuthResponse>("/api/auth/login", {
      email,
      password,
    });
    this.http.setToken(res.token);
    return res;
  }

  async register(
    email: string,
    password: string,
    displayName: string
  ): Promise<AuthResponse> {
    const res = await this.http.post<AuthResponse>("/api/auth/register", {
      email,
      password,
      display_name: displayName,
    });
    this.http.setToken(res.token);
    return res;
  }

  async createSession(): Promise<void> {
    try {
      await this.http.post("/api/auth/session", {});
    } catch {
      // Non-fatal — session cookie is a convenience for iframe auth.
    }
  }

  async getMe(): Promise<User> {
    const res = await this.http.get<{ user: User }>("/api/users/me");
    return res.user;
  }

  async getUsers(): Promise<User[]> {
    return this.http.get<User[]>("/api/users");
  }
}
