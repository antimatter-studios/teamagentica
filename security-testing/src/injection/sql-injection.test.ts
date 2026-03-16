import { describe, it, expect } from "vitest";
import { authedClient } from "../common/client.js";
import { SQL_INJECTIONS } from "../common/payloads.js";

describe("INJECTION: SQL injection via API parameters", () => {
  describe("plugin ID parameter", () => {
    it.each(SQL_INJECTIONS)(
      "GET /api/plugins/%s should not cause SQL error",
      async (payload) => {
        const client = await authedClient();
        try {
          await client.plugins.get(payload);
        } catch (e: unknown) {
          const msg = (e as Error).message.toLowerCase();
          // Should get "not found" or "bad request", never SQL error details
          expect(msg).not.toContain("sql");
          expect(msg).not.toContain("syntax error");
          expect(msg).not.toContain("sqlite");
          expect(msg).not.toContain("mysql");
          expect(msg).not.toContain("postgres");
          expect(msg).not.toContain("column");
          expect(msg).not.toContain("table");
        }
      }
    );
  });

  describe("plugin config values", () => {
    it.each(SQL_INJECTIONS)(
      "PUT config with SQL payload %s should be stored safely",
      async (payload) => {
        const client = await authedClient();
        const plugins = await client.plugins.list();
        if (plugins.length === 0) return;

        const plugin = plugins[0];
        try {
          await client.plugins.updateConfig(plugin.id, {
            test_key: { value: payload, is_secret: false },
          });
          // If it succeeds, read it back and verify it's stored literally
          const config = await client.plugins.getConfig(plugin.id);
          const entry = config.find((c) => c.key === "test_key");
          if (entry) {
            expect(entry.value).toBe(payload);
          }
        } catch (e: unknown) {
          const msg = (e as Error).message.toLowerCase();
          expect(msg).not.toContain("sql");
          expect(msg).not.toContain("syntax");
        }
      }
    );
  });

  describe("marketplace search", () => {
    it.each(SQL_INJECTIONS)(
      "marketplace browse with query %s should not leak SQL",
      async (payload) => {
        const client = await authedClient();
        try {
          await client.marketplace.browse(payload);
        } catch (e: unknown) {
          const msg = (e as Error).message.toLowerCase();
          expect(msg).not.toContain("sql");
          expect(msg).not.toContain("syntax");
          expect(msg).not.toContain("column");
        }
      }
    );
  });

  describe("login credentials", () => {
    it.each(SQL_INJECTIONS)(
      "login with email %s should not leak SQL details",
      async (payload) => {
        const client = (await import("../common/client.js")).anonClient();
        try {
          await client.auth.login(payload, "password");
        } catch (e: unknown) {
          const msg = (e as Error).message.toLowerCase();
          expect(msg).not.toContain("sql");
          expect(msg).not.toContain("syntax");
          expect(msg).not.toContain("sqlite");
        }
      }
    );
  });
});
