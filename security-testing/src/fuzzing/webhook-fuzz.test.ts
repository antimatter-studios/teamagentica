import { describe, it, expect } from "vitest";
import { rawFetch } from "../common/client.js";
import { SQL_INJECTIONS, PATH_TRAVERSALS, CMD_INJECTIONS } from "../common/payloads.js";

describe("FUZZING: webhook endpoint (unauthenticated)", () => {
  describe("plugin ID fuzzing", () => {
    const payloads = [
      ...SQL_INJECTIONS.slice(0, 3),
      ...PATH_TRAVERSALS.slice(0, 3),
      ...CMD_INJECTIONS.slice(0, 3),
      // Special chars
      "../../kernel",
      "plugin%00id",       // null byte
      "plugin\nid",        // newline
      "plugin\rid",        // carriage return
      "<plugin>",          // XML/HTML
      "${env.SECRET}",     // template injection
      "{{.Secret}}",       // Go template
    ];

    it.each(payloads)(
      "webhook with plugin_id %s should not crash or leak",
      async (payload) => {
        const res = await rawFetch(
          `/api/webhook/${encodeURIComponent(payload)}/test`
        );
        const body = await res.text();

        // Should never leak internal details
        expect(body).not.toContain("goroutine");
        expect(body).not.toContain("panic");
        expect(body).not.toContain("root:");
        expect(body).not.toContain("/bin/");
        expect(res.status).not.toBe(500);
      }
    );
  });

  describe("large POST body to webhook", () => {
    it("10MB body should be rejected", async () => {
      const res = await rawFetch("/api/webhook/test-plugin/hook", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "x".repeat(10_000_000),
      });
      // Should be rejected, not OOM
      expect([400, 413, 404, 502]).toContain(res.status);
    });
  });

  describe("concurrent webhook spam", () => {
    it("100 concurrent requests should not crash the server", async () => {
      const promises = Array.from({ length: 100 }, (_, i) =>
        rawFetch(`/api/webhook/nonexistent-${i}/test`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: '{"test": true}',
        })
      );
      const results = await Promise.allSettled(promises);
      const fulfilled = results.filter((r) => r.status === "fulfilled");
      // At least most should get responses (not connection refused)
      expect(fulfilled.length).toBeGreaterThan(50);
    });
  });
});
