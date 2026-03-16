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
    statusText: status === 200 ? "OK" : "Error",
    json: () => Promise.resolve(data),
    text: () => Promise.resolve(JSON.stringify(data)),
  } as unknown as Response);
}

beforeEach(() => {
  mockFetch.mockReset();
  Object.keys(storage).forEach(k => delete storage[k]);
});

describe("client.ts — SDK singleton wiring", () => {
  it("API_BASE is derived from VITE_TEAMAGENTICA_KERNEL_URL env var", async () => {
    const { API_BASE } = await import("../client");
    expect(API_BASE).toBe("http://test-kernel:8080");
  });

  it("apiClient sends Bearer token from localStorage on each request", async () => {
    const { apiClient } = await import("../client");
    storage["teamagentica_token"] = "my-jwt";
    mockFetch.mockReturnValueOnce(jsonResponse({ plugins: [] }));

    await apiClient.plugins.list();

    const [url, opts] = mockFetch.mock.calls[0];
    expect(url).toContain("http://test-kernel:8080");
    expect(opts.headers.Authorization).toBe("Bearer my-jwt");
  });

  it("apiClient sends no Authorization header when no token in localStorage", async () => {
    const { apiClient } = await import("../client");
    mockFetch.mockReturnValueOnce(jsonResponse({ plugins: [] }));

    await apiClient.plugins.list();

    const [, opts] = mockFetch.mock.calls[0];
    expect(opts.headers.Authorization).toBeUndefined();
  });

  it("401 response clears localStorage token", async () => {
    const { apiClient } = await import("../client");
    storage["teamagentica_token"] = "expired-token";
    mockFetch.mockReturnValueOnce(jsonResponse({}, 401));

    await expect(apiClient.plugins.list()).rejects.toThrow("Unauthorized");

    expect(storage["teamagentica_token"]).toBeUndefined();
  });

  it("setOnUnauthorized callback is invoked on 401", async () => {
    const { apiClient, setOnUnauthorized } = await import("../client");
    const onUnauth = vi.fn();
    setOnUnauthorized(onUnauth);
    mockFetch.mockReturnValueOnce(jsonResponse({}, 401));

    await expect(apiClient.plugins.list()).rejects.toThrow("Unauthorized");

    expect(onUnauth).toHaveBeenCalledOnce();
  });
});
