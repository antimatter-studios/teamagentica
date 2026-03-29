export interface ClientConfig {
  /** Full base URL, e.g. "http://api.teamagentica.localhost" or "https://my-instance.com" */
  baseUrl: string;
  /** Static token — if provided, used for all requests. */
  token?: string;
  /** Dynamic token provider — called per-request. Takes precedence over static token. */
  getToken?: () => string | null;
  /** Called on any 401 response before the error is thrown. */
  onUnauthorized?: () => void;
  /** Provides the stored refresh token for silent renewal. */
  getRefreshToken?: () => string | null;
  /** Called after a successful token refresh with new tokens. */
  onTokenRefreshed?: (token: string, refreshToken: string) => void;
}

export class HttpTransport {
  private config: ClientConfig;
  private refreshPromise: Promise<boolean> | null = null;
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

  /** Attempt to silently refresh the access token. Returns true on success.
   *  Deduplicates concurrent refresh attempts into a single request. */
  private tryRefresh(): Promise<boolean> {
    if (this.refreshPromise) return this.refreshPromise;
    this.refreshPromise = (async () => {
      const rt = this.config.getRefreshToken?.();
      if (!rt) return false;
      try {
        const res = await this.doFetch(`${this.config.baseUrl}/api/auth/refresh`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ refresh_token: rt }),
        });
        if (!res.ok) return false;
        const data = await res.json() as { token: string; refresh_token?: string };
        this.setToken(data.token);
        this.config.onTokenRefreshed?.(data.token, data.refresh_token ?? "");
        return true;
      } catch {
        return false;
      } finally {
        this.refreshPromise = null;
      }
    })();
    return this.refreshPromise;
  }

  private async handleResponse<T>(res: Response, retryFn?: () => Promise<Response>): Promise<T> {
    if (res.status === 401 && retryFn) {
      const refreshed = await this.tryRefresh();
      if (refreshed) {
        const retryRes = await retryFn();
        return this.handleResponse<T>(retryRes);
      }
      this.config.onUnauthorized?.();
      throw new Error("Unauthorized");
    }
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
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    const res = await doReq();
    return this.handleResponse<T>(res, doReq);
  }

  async getText(path: string): Promise<string> {
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    const res = await doReq();
    if (res.status === 401) {
      const refreshed = await this.tryRefresh();
      if (refreshed) {
        const retryRes = await doReq();
        if (!retryRes.ok) throw new Error(`HTTP ${retryRes.status}`);
        return retryRes.text();
      }
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
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    const res = await doReq();
    return this.handleResponse<T>(res, doReq);
  }

  /** POST without Authorization header — used for refresh endpoint. */
  async postNoAuth<T>(path: string, body: unknown): Promise<T> {
    const res = await this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return this.handleResponse<T>(res);
  }

  async put<T>(path: string, body: unknown): Promise<T> {
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    const res = await doReq();
    return this.handleResponse<T>(res, doReq);
  }

  async patch<T>(path: string, body: unknown): Promise<T> {
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json", ...this.authHeaders() },
      body: JSON.stringify(body),
    });
    const res = await doReq();
    return this.handleResponse<T>(res, doReq);
  }

  async delete<T = void>(path: string): Promise<T> {
    const doReq = () => this.doFetch(`${this.config.baseUrl}${path}`, {
      method: "DELETE",
      headers: this.authHeaders(),
    });
    const res = await doReq();
    return this.handleResponse<T>(res, doReq);
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
