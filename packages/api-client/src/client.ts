export interface ClientConfig {
  /** Full base URL, e.g. "http://api.teamagentica.localhost" or "https://my-instance.com" */
  baseUrl: string;
  /** Static token — if provided, used for all requests. */
  token?: string;
  /** Dynamic token provider — called per-request. Takes precedence over static token. */
  getToken?: () => string | null;
  /** Called on any 401 response before the error is thrown. */
  onUnauthorized?: () => void;
}

export class HttpTransport {
  private config: ClientConfig;
  constructor(config: ClientConfig) { this.config = config; }

  get baseUrl(): string {
    return this.config.baseUrl;
  }

  /** Update the static token (e.g. after login). */
  setToken(token: string | null): void {
    if (token) {
      this.config.token = token;
    } else {
      delete this.config.token;
    }
  }

  private resolveToken(): string | null {
    if (this.config.getToken) return this.config.getToken();
    return this.config.token ?? null;
  }

  private authHeaders(): Record<string, string> {
    const token = this.resolveToken();
    return token ? { Authorization: `Bearer ${token}` } : {};
  }

  /** Wrap fetch to turn network errors into actionable messages. */
  private async doFetch(url: string, init: RequestInit): Promise<Response> {
    try {
      return await fetch(url, init);
    } catch (err) {
      const base = this.config.baseUrl;
      if (err instanceof TypeError && err.message === "Failed to fetch") {
        throw new Error(`Cannot reach server at ${base} — is it running?`);
      }
      throw new Error(`Network error contacting ${base}: ${(err as Error).message}`);
    }
  }

  private async handleResponse<T>(res: Response): Promise<T> {
    if (res.status === 401) {
      this.config.onUnauthorized?.();
      throw new Error("Unauthorized");
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(body.error || body.message || `HTTP ${res.status}`);
    }
    if (res.status === 204) return undefined as T;
    return res.json() as Promise<T>;
  }

  async get<T>(path: string): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    return this.handleResponse<T>(res);
  }

  async getText(path: string): Promise<string> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    if (res.status === 401) {
      this.config.onUnauthorized?.();
      throw new Error("Unauthorized");
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(body.error || body.message || `HTTP ${res.status}`);
    }
    return res.text();
  }

  async post<T>(path: string, body: unknown): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    return this.handleResponse<T>(res);
  }

  async put<T>(path: string, body: unknown): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    return this.handleResponse<T>(res);
  }

  async patch<T>(path: string, body: unknown): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    return this.handleResponse<T>(res);
  }

  async delete<T = void>(path: string): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "DELETE",
      headers: this.authHeaders(),
    });
    return this.handleResponse<T>(res);
  }

  async putRaw(path: string, body: BodyInit, contentType: string): Promise<void> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "PUT",
      headers: { "Content-Type": contentType, ...this.authHeaders() },
      body,
    });
    if (!res.ok) throw new Error(`Upload failed: ${res.status}`);
  }

  async getRaw(path: string): Promise<Response> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "GET",
      headers: this.authHeaders(),
      cache: "no-store",
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res;
  }

  async deleteRaw(path: string): Promise<void> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "DELETE",
      headers: this.authHeaders(),
    });
    if (!res.ok) throw new Error(`Delete failed: ${res.status}`);
  }

  async postFormData<T>(path: string, formData: FormData): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "POST",
      headers: this.authHeaders(),
      body: formData,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(body.error || `HTTP ${res.status}`);
    }
    return res.json() as Promise<T>;
  }
}
