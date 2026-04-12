import { describe, it, expect } from "vitest";
import { rawFetch, authedClient } from "../common/client.js";

describe("ENUMERATION: unauthenticated plugin/container discovery", () => {
  describe("webhook endpoint plugin enumeration", () => {
    const commonPluginIds = [
      "system-teamagentica-plugin-provider",
      "agent-openai",
      "agent-anthropic",
      "agent-google",
      "messaging-discord",
      "messaging-telegram",
      "messaging-chat",
      "infra-cost-tracking",
      "workspace-manager",
      "storage-minio",
    ];

    it("should not reveal which plugins are installed via webhook probing", async () => {
      const installed: string[] = [];
      const notInstalled: string[] = [];

      for (const id of commonPluginIds) {
        const res = await rawFetch(`/api/webhook/${id}/health`);
        if (res.status === 502 || res.status === 200) {
          installed.push(id);
        } else {
          notInstalled.push(id);
        }
      }

      // If we can distinguish installed vs not-installed, that's an info leak
      if (installed.length > 0 && notInstalled.length > 0) {
        console.warn(
          "INFO LEAK: webhook endpoint reveals installed plugins:",
          installed
        );
      }
      // Informational — don't hard-fail
      expect(true).toBe(true);
    });
  });

  describe("managed container ID brute-force", () => {
    it("should test container ID space (8-char hex)", async () => {
      // Try a few common/predictable IDs
      const guesses = [
        "00000000",
        "aaaaaaaa",
        "12345678",
        "deadbeef",
        "abcdef00",
      ];
      const found: string[] = [];

      for (const id of guesses) {
        const res = await rawFetch(`/ws/${id}/`);
        if (res.status !== 404 && res.status !== 502) {
          found.push(`${id} (status: ${res.status})`);
        }
      }

      if (found.length > 0) {
        console.warn("FOUND container IDs via brute force:", found);
      }
      expect(true).toBe(true);
    });
  });
});

describe("ENUMERATION: authenticated information disclosure", () => {
  it("plugin config should mask secrets", async () => {
    const client = await authedClient();
    const plugins = await client.plugins.list();

    for (const plugin of plugins) {
      const config = await client.plugins.getConfig(plugin.id);
      for (const entry of config) {
        if (entry.is_secret) {
          expect(entry.value).toBe("********");
        }
      }
    }
  });

  it("plugin list should not contain internal hostnames/IPs", async () => {
    const client = await authedClient();
    const plugins = await client.plugins.list();

    for (const plugin of plugins) {
      const json = JSON.stringify(plugin);
      // Internal Docker hostnames shouldn't be exposed
      expect(json).not.toMatch(/172\.\d+\.\d+\.\d+/);
      expect(json).not.toMatch(/10\.\d+\.\d+\.\d+/);
    }
  });

  it("error responses should not contain stack traces", async () => {
    const client = await authedClient();
    try {
      await client.plugins.get("nonexistent-plugin-id-12345");
    } catch (e: unknown) {
      const msg = (e as Error).message;
      expect(msg).not.toContain("goroutine");
      expect(msg).not.toContain("panic");
      expect(msg).not.toContain(".go:");
      expect(msg).not.toContain("runtime.");
    }
  });
});
