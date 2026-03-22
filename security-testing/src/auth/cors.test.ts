import { describe, it, expect } from "vitest";
import { rawFetch, rawFetchAuthed, authedClient, TARGET } from "../common/client.js";

describe("AUTH: CORS origin validation", () => {
  describe("origin reflection", () => {
    const maliciousOrigins = [
      "https://evil.com",
      "https://attacker.example.org",
      "http://localhost:9999",
      "null", // data: URI origin
    ];

    for (const origin of maliciousOrigins) {
      it(`should NOT reflect untrusted origin: ${origin}`, async () => {
        const res = await rawFetch("/api/health", {
          headers: { Origin: origin },
        });
        const allowedOrigin = res.headers.get("Access-Control-Allow-Origin");

        // Origin should NOT be reflected back — either absent, or a specific trusted origin
        expect(
          allowedOrigin,
          `Origin ${origin} was reflected — attacker site can exfiltrate tokens`
        ).not.toBe(origin);
      });
    }

    it("should not allow credentials with wildcard origin", async () => {
      const res = await rawFetch("/api/health", {
        headers: { Origin: "https://evil.com" },
      });
      const allowCreds = res.headers.get("Access-Control-Allow-Credentials");
      const allowOrigin = res.headers.get("Access-Control-Allow-Origin");

      // If credentials are allowed, origin MUST NOT be wildcard or reflected
      if (allowCreds === "true") {
        expect(
          allowOrigin,
          "Credentials allowed with reflected origin — CSRF/token exfiltration possible"
        ).not.toBe("https://evil.com");
        expect(allowOrigin).not.toBe("*");
      }
    });
  });

  describe("preflight (OPTIONS)", () => {
    it("should respond to OPTIONS with proper CORS headers", async () => {
      const res = await rawFetch("/api/plugins", {
        method: "OPTIONS",
        headers: {
          Origin: TARGET,
          "Access-Control-Request-Method": "GET",
        },
      });
      // Should respond (200 or 204), not error
      expect(res.status).toBeLessThan(400);
    });

    it("should reject OPTIONS from untrusted origin", async () => {
      const res = await rawFetch("/api/plugins", {
        method: "OPTIONS",
        headers: {
          Origin: "https://evil.com",
          "Access-Control-Request-Method": "DELETE",
        },
      });
      const allowed = res.headers.get("Access-Control-Allow-Methods");
      // Should not grant DELETE to untrusted origin
      if (allowed) {
        expect(allowed).not.toContain("DELETE");
      }
    });
  });
});
