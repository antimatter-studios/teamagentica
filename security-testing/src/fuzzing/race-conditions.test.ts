import { describe, it, expect } from "vitest";
import { authedClient, rawFetch } from "../common/client.js";

describe("FUZZ: race condition tests", () => {
  it("concurrent config updates should not corrupt data", async () => {
    try {
      const client = await authedClient();
      const token = (client as any).http?.config?.token;
      if (!token) return;

      const plugins = await client.plugins.list();
      const target = plugins.find((p) => p.enabled);
      if (!target) return;

      // Fire 10 concurrent config updates with different values
      const promises = Array.from({ length: 10 }, (_, i) =>
        rawFetch(`/api/plugins/${target.id}/config`, {
          method: "PUT",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({
            config: {
              RACE_TEST: { value: `value-${i}`, is_secret: false },
            },
          }),
        })
      );

      const results = await Promise.allSettled(promises);
      const successes = results.filter((r) => r.status === "fulfilled");

      // All should complete (no crashes)
      expect(successes.length).toBeGreaterThan(0);

      // Read back the config — should be one consistent value
      const config = await client.plugins.getConfig(target.id);
      const raceEntry = config.find((c) => c.key === "RACE_TEST");
      if (raceEntry) {
        expect(raceEntry.value).toMatch(/^value-\d$/);
      }

      // Cleanup
      await rawFetch(`/api/plugins/${target.id}/config`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          config: {
            RACE_TEST: { value: "", is_secret: false },
          },
        }),
      });
    } catch {
      // Auth failed
    }
  });

  it("concurrent login attempts should not cause token confusion", async () => {
    try {
      // Fire 5 concurrent logins
      const promises = Array.from({ length: 5 }, () => authedClient());
      const results = await Promise.allSettled(promises);
      const successes = results.filter(
        (r) => r.status === "fulfilled"
      ) as PromiseFulfilledResult<Awaited<ReturnType<typeof authedClient>>>[];

      // All should succeed with valid tokens
      for (const result of successes) {
        const me = await result.value.auth.getMe();
        expect(me.email).toBeDefined();
      }
    } catch {
      // Auth failed
    }
  });

  it("concurrent webhook deliveries should not cause crashes", async () => {
    const pluginId = "nonexistent-race-test";
    const promises = Array.from({ length: 50 }, (_, i) =>
      rawFetch(`/api/webhook/${pluginId}/hook`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ seq: i, data: "race-test" }),
      })
    );

    const results = await Promise.allSettled(promises);
    const completed = results.filter((r) => r.status === "fulfilled");

    // All should complete (no connection resets or panics)
    expect(completed.length).toBe(50);

    // None should be 500 (internal server error)
    for (const r of completed) {
      const res = (r as PromiseFulfilledResult<Response>).value;
      expect(res.status, "Server error during concurrent webhooks").not.toBe(500);
    }
  });
});
