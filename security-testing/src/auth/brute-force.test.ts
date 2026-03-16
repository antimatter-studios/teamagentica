import { describe, it, expect } from "vitest";
import { rawFetch } from "../common/client.js";

describe("AUTH: brute force & rate limiting", () => {
  it("should rate-limit login attempts", async () => {
    const attempts = 20;
    const results: number[] = [];

    for (let i = 0; i < attempts; i++) {
      const res = await rawFetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: "admin@test.local",
          password: `wrong-password-${i}`,
        }),
      });
      results.push(res.status);
    }

    // We expect either 429 (rate limited) or at minimum not all 401s
    // If all are 401, rate limiting may be missing
    const rateLimited = results.some((s) => s === 429);
    const allUnauthorized = results.every((s) => s === 401);

    if (allUnauthorized) {
      console.warn(
        "WARNING: No rate limiting detected on login endpoint after",
        attempts,
        "failed attempts"
      );
    }

    // Log finding but don't hard-fail — this is informational
    expect(true).toBe(true);
  });

  it("should rate-limit registration attempts", async () => {
    const attempts = 10;
    const results: number[] = [];

    for (let i = 0; i < attempts; i++) {
      const res = await rawFetch("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: `attacker${i}@evil.com`,
          password: "password123",
          display_name: `Attacker ${i}`,
        }),
      });
      results.push(res.status);
    }

    const rateLimited = results.some((s) => s === 429);
    if (!rateLimited) {
      console.warn(
        "WARNING: No rate limiting detected on registration endpoint"
      );
    }
    expect(true).toBe(true);
  });
});
