import { describe, it, expect } from "vitest";
import { authedClient, rawFetch } from "../common/client.js";
import { PATH_TRAVERSALS } from "../common/payloads.js";

describe("INJECTION: path traversal via plugin routing", () => {
  describe("plugin route proxy", () => {
    it.each(PATH_TRAVERSALS)(
      "routed path %s should not escape plugin scope",
      async (payload) => {
        const client = await authedClient();
        const plugins = await client.plugins.list();
        const running = plugins.find((p) => p.status === "running");
        if (!running) return;

        try {
          // Access via the raw HTTP transport to inject arbitrary paths
          await client.http.get(`/api/route/${running.id}/${payload}`);
        } catch (e: unknown) {
          const msg = (e as Error).message;
          // Should never return actual file contents
          expect(msg).not.toContain("root:");
          expect(msg).not.toContain("/bin/bash");
        }
      }
    );
  });

  describe("plugin logs endpoint", () => {
    it.each(PATH_TRAVERSALS)(
      "plugin ID %s in logs endpoint should not traverse",
      async (payload) => {
        const client = await authedClient();
        try {
          await client.plugins.getLogs(payload);
        } catch (e: unknown) {
          const msg = (e as Error).message;
          expect(msg).not.toContain("root:");
          expect(msg).not.toContain("/bin/bash");
        }
      }
    );
  });

  describe("webhook path traversal", () => {
    it.each(PATH_TRAVERSALS)(
      "webhook with path %s should not leak files",
      async (payload) => {
        const res = await rawFetch(`/api/webhook/fake-plugin/${payload}`);
        const body = await res.text();
        expect(body).not.toContain("root:");
        expect(body).not.toContain("/bin/bash");
      }
    );
  });
});
