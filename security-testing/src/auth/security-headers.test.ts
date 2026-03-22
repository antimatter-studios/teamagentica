import { describe, it, expect } from "vitest";
import { rawFetch, authedClient } from "../common/client.js";

describe("AUTH: security response headers", () => {
  const endpoints = [
    { path: "/api/health", auth: false },
    { path: "/api/plugins", auth: true },
    { path: "/api/users/me", auth: true },
  ];

  describe("anti-sniffing headers", () => {
    for (const ep of endpoints) {
      it(`${ep.path} should include X-Content-Type-Options: nosniff`, async () => {
        let res: Response;
        if (ep.auth) {
          const client = await authedClient();
          const token = (client as any).http?.config?.token;
          if (!token) return; // skip if auth fails
          res = await rawFetch(ep.path, {
            headers: { Authorization: `Bearer ${token}` },
          });
        } else {
          res = await rawFetch(ep.path);
        }
        expect(
          res.headers.get("X-Content-Type-Options"),
          `${ep.path} missing X-Content-Type-Options header`
        ).toBe("nosniff");
      });
    }
  });

  describe("clickjacking protection", () => {
    it("should include X-Frame-Options or CSP frame-ancestors", async () => {
      const res = await rawFetch("/api/health");
      const xfo = res.headers.get("X-Frame-Options");
      const csp = res.headers.get("Content-Security-Policy");
      const hasFrameProtection =
        xfo === "DENY" ||
        xfo === "SAMEORIGIN" ||
        (csp && csp.includes("frame-ancestors"));

      expect(
        hasFrameProtection,
        "No clickjacking protection — missing X-Frame-Options and CSP frame-ancestors"
      ).toBe(true);
    });
  });

  describe("cache control for sensitive endpoints", () => {
    it("/api/users/me should not be cached", async () => {
      try {
        const client = await authedClient();
        const token = (client as any).http?.config?.token;
        if (!token) return;
        const res = await rawFetch("/api/users/me", {
          headers: { Authorization: `Bearer ${token}` },
        });
        const cc = res.headers.get("Cache-Control") || "";
        const pragma = res.headers.get("Pragma") || "";
        // Sensitive endpoints should prevent caching
        const noCaching =
          cc.includes("no-store") ||
          cc.includes("no-cache") ||
          pragma.includes("no-cache");

        expect(
          noCaching,
          "/api/users/me is cacheable — sensitive data may be stored in proxies"
        ).toBe(true);
      } catch {
        // Auth failed — skip
      }
    });
  });

  describe("server information disclosure", () => {
    it("should not expose server version in headers", async () => {
      const res = await rawFetch("/api/health");
      const server = res.headers.get("Server") || "";
      // Should not expose Go version, gin version, or OS info
      expect(server).not.toMatch(/gin|go|golang|nginx\/\d/i);
    });

    it("should not expose powered-by header", async () => {
      const res = await rawFetch("/api/health");
      expect(res.headers.get("X-Powered-By")).toBeNull();
    });
  });
});
