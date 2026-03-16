import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock localStorage
const storage: Record<string, string> = {};
Object.defineProperty(globalThis, "localStorage", {
  value: {
    getItem: (key: string) => storage[key] ?? null,
    setItem: (key: string, value: string) => { storage[key] = value; },
    removeItem: (key: string) => { delete storage[key]; },
    clear: () => { Object.keys(storage).forEach(k => delete storage[k]); },
  },
});

vi.stubEnv("VITE_TEAMAGENTICA_KERNEL_URL", "http://test-kernel:8080");

const mockFetch = vi.fn();
globalThis.fetch = mockFetch;

function jsonResponse(data: unknown, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    statusText: "OK",
    json: () => Promise.resolve(data),
    text: () => Promise.resolve(JSON.stringify(data)),
  } as unknown as Response);
}

beforeEach(() => {
  mockFetch.mockReset();
  Object.keys(storage).forEach(k => delete storage[k]);
});

describe("auth.ts — localStorage token helpers", () => {
  it("storeToken and getStoredToken round-trip", async () => {
    const { storeToken, getStoredToken } = await import("../auth");
    storeToken("abc123");
    expect(getStoredToken()).toBe("abc123");
  });

  it("clearToken removes the token", async () => {
    const { storeToken, getStoredToken, clearToken } = await import("../auth");
    storeToken("abc123");
    clearToken();
    expect(getStoredToken()).toBeNull();
  });

  it("getStoredToken returns null when no token stored", async () => {
    const { getStoredToken } = await import("../auth");
    expect(getStoredToken()).toBeNull();
  });
});

describe("auth.ts — login/register", () => {
  it("login stores token to localStorage after successful auth", async () => {
    const { login } = await import("../auth");
    mockFetch
      .mockReturnValueOnce(jsonResponse({ token: "jwt-from-server", user: { id: "1", email: "a@b.c" } })) // POST /api/auth/login
      .mockReturnValueOnce(jsonResponse({})); // POST /api/auth/session

    await login("a@b.c", "secret");

    expect(storage["teamagentica_token"]).toBe("jwt-from-server");
  });

  it("login calls createSession after auth", async () => {
    const { login } = await import("../auth");
    mockFetch
      .mockReturnValueOnce(jsonResponse({ token: "jwt", user: { id: "1" } }))
      .mockReturnValueOnce(jsonResponse({}));

    await login("a@b.c", "secret");

    expect(mockFetch).toHaveBeenCalledTimes(2);
    const sessionCall = mockFetch.mock.calls[1];
    expect(sessionCall[0]).toContain("/api/auth/session");
  });

  it("register stores token to localStorage after successful registration", async () => {
    const { register } = await import("../auth");
    mockFetch
      .mockReturnValueOnce(jsonResponse({ token: "new-jwt", user: { id: "2", email: "b@c.d" } }))
      .mockReturnValueOnce(jsonResponse({}));

    await register("b@c.d", "pass", "Bob");

    expect(storage["teamagentica_token"]).toBe("new-jwt");
  });

  it("login returns the auth response", async () => {
    const { login } = await import("../auth");
    mockFetch
      .mockReturnValueOnce(jsonResponse({ token: "jwt", user: { id: "1", email: "a@b.c", display_name: "Alice" } }))
      .mockReturnValueOnce(jsonResponse({}));

    const res = await login("a@b.c", "secret");

    expect(res.token).toBe("jwt");
    expect(res.user.email).toBe("a@b.c");
  });
});
