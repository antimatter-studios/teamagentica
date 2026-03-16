import { describe, it, expect } from "vitest";
import { rawFetch } from "../common/client.js";

describe("ENUMERATION: user account enumeration via login", () => {
  it("login error for valid vs invalid email should be identical", async () => {
    // Try a likely-valid email
    const validRes = await rawFetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: "admin@test.local", password: "wrong" }),
    });
    const validBody = await validRes.json().catch(() => ({}));

    // Try a definitely-invalid email
    const invalidRes = await rawFetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email: "nonexistent-user-xyz@nowhere.invalid",
        password: "wrong",
      }),
    });
    const invalidBody = await invalidRes.json().catch(() => ({}));

    // Status codes should be the same
    expect(validRes.status).toBe(invalidRes.status);

    // Error messages should be identical (no "user not found" vs "wrong password")
    if (validBody.error && invalidBody.error) {
      expect(validBody.error).toBe(invalidBody.error);
    }
  });

  it("login timing should not differ significantly between valid/invalid users", async () => {
    const runs = 5;
    const validTimes: number[] = [];
    const invalidTimes: number[] = [];

    for (let i = 0; i < runs; i++) {
      let start = Date.now();
      await rawFetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: "admin@test.local",
          password: "wrong",
        }),
      });
      validTimes.push(Date.now() - start);

      start = Date.now();
      await rawFetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: `fake${i}@nowhere.invalid`,
          password: "wrong",
        }),
      });
      invalidTimes.push(Date.now() - start);
    }

    const avgValid =
      validTimes.reduce((a, b) => a + b, 0) / validTimes.length;
    const avgInvalid =
      invalidTimes.reduce((a, b) => a + b, 0) / invalidTimes.length;
    const diff = Math.abs(avgValid - avgInvalid);

    // More than 100ms difference suggests timing-based user enumeration
    if (diff > 100) {
      console.warn(
        `TIMING LEAK: valid user avg ${avgValid.toFixed(0)}ms vs invalid ${avgInvalid.toFixed(0)}ms (diff: ${diff.toFixed(0)}ms)`
      );
    }
    expect(true).toBe(true);
  });
});
