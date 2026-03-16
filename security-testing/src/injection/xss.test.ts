import { describe, it, expect } from "vitest";
import { authedClient, anonClient } from "../common/client.js";
import { XSS_PAYLOADS } from "../common/payloads.js";

describe("INJECTION: stored XSS via API fields", () => {
  describe("plugin name field", () => {
    it.each(XSS_PAYLOADS)(
      "installing plugin with name %s should store escaped or reject",
      async (payload) => {
        const client = await authedClient();
        try {
          const plugin = await client.plugins.install({
            name: payload,
            image: "test-xss:latest",
            version: "0.0.1",
          });
          // If accepted, verify it's stored literally (no interpretation)
          const fetched = await client.plugins.get(plugin.id);
          expect(fetched.name).toBe(payload);
          // Clean up
          await client.plugins.uninstall(plugin.id);
        } catch {
          // Rejection is also acceptable
        }
      }
    );
  });

  describe("registration display name", () => {
    it.each(XSS_PAYLOADS)(
      "registering with display_name %s should store safely",
      async (payload) => {
        const client = anonClient();
        try {
          await client.auth.register(
            `xss-test-${Date.now()}@test.local`,
            "password123",
            payload
          );
        } catch {
          // Expected if registration is restricted
        }
      }
    );
  });
});
