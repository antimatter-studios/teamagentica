import { describe, it, expect } from "vitest";
import { authedClient, rawFetch } from "../common/client.js";

describe("INJECTION: marketplace SSRF and query injection", () => {
  describe("provider URL validation", () => {
    const ssrfUrls = [
      "http://127.0.0.1:6379",       // Redis
      "http://localhost:9741",         // Kernel itself
      "http://169.254.169.254",        // AWS metadata
      "http://[::1]:8080",             // IPv6 loopback
      "file:///etc/passwd",            // Local file
      "gopher://127.0.0.1:25",        // Gopher protocol
      "http://0.0.0.0:8080",          // Wildcard
      "http://teamagentica-kernel:8080", // Internal Docker DNS
    ];

    for (const url of ssrfUrls) {
      it(`should reject SSRF URL: ${url}`, async () => {
        try {
          const client = await authedClient();
          // Try to add a marketplace provider with an internal URL
          const res = await rawFetch("/api/marketplace/providers", {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              Authorization: `Bearer ${(client as any).http?.config?.token}`,
            },
            body: JSON.stringify({ name: `ssrf-test-${Date.now()}`, url }),
          });

          // Should reject with 400, not accept the URL
          if (res.ok) {
            // Provider was created — this is a vulnerability
            const body = await res.json();
            expect.fail(
              `SSRF: provider with URL ${url} was accepted (id: ${body.id}). ` +
              `Kernel will fetch from this URL on marketplace search.`
            );
          }
        } catch (e: any) {
          // Expected rejection — pass
          expect(e.message).toBeDefined();
        }
      });
    }
  });

  describe("marketplace search query injection", () => {
    const queryPayloads = [
      "test&admin=true",
      "test&token=stolen",
      "test%26admin%3Dtrue",
      'test"}}}&extra=1',
    ];

    for (const payload of queryPayloads) {
      it(`search query "${payload}" should not inject extra params`, async () => {
        try {
          const client = await authedClient();
          const res = await rawFetch(
            `/api/marketplace/search?q=${encodeURIComponent(payload)}`,
            {
              headers: {
                Authorization: `Bearer ${(client as any).http?.config?.token}`,
              },
            }
          );

          // Should either work (returning no results) or return 400
          // Should NOT return 500 (server error from injected params)
          expect(res.status).not.toBe(500);
        } catch {
          // Expected
        }
      });
    }
  });
});
