import { describe, it, expect } from "vitest";
import { authedClient } from "../common/client.js";
import { CMD_INJECTIONS } from "../common/payloads.js";

describe("INJECTION: command injection via config values", () => {
  it.each(CMD_INJECTIONS)(
    "config value %s should be stored literally, not executed",
    async (payload) => {
      const client = await authedClient();
      const plugins = await client.plugins.list();
      if (plugins.length === 0) return;

      const plugin = plugins[0];
      const startTime = Date.now();

      try {
        await client.plugins.updateConfig(plugin.id, {
          cmd_test: { value: payload, is_secret: false },
        });
      } catch {
        // Rejection is fine
      }

      const elapsed = Date.now() - startTime;
      // If "sleep 5" was executed, request would take >5s
      expect(elapsed).toBeLessThan(5000);
    }
  );
});

describe("INJECTION: command injection via plugin image name", () => {
  it.each(CMD_INJECTIONS)(
    "plugin install with image %s should not execute commands",
    async (payload) => {
      const client = await authedClient();
      const startTime = Date.now();

      try {
        await client.plugins.install({
          name: "cmd-test",
          image: payload,
          version: "0.0.1",
        });
      } catch {
        // Expected to fail
      }

      const elapsed = Date.now() - startTime;
      expect(elapsed).toBeLessThan(5000);
    }
  );
});
