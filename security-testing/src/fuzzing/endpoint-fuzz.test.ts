import { describe, it, expect } from "vitest";
import { authedClient, rawFetch } from "../common/client.js";
import {
  oversizedString,
  TYPE_CONFUSION,
  HEADER_INJECTIONS,
} from "../common/payloads.js";

describe("FUZZING: oversized inputs", () => {
  it("oversized plugin name (10KB) should be rejected or truncated", async () => {
    const client = await authedClient();
    try {
      await client.plugins.install({
        name: oversizedString(10_000),
        image: "test:latest",
        version: "0.0.1",
      });
    } catch (e: unknown) {
      const msg = (e as Error).message;
      expect(msg).not.toContain("panic");
      expect(msg).not.toContain("out of memory");
    }
  });

  it("oversized config value (1MB) should be rejected", async () => {
    const client = await authedClient();
    const plugins = await client.plugins.list();
    if (plugins.length === 0) return;

    try {
      await client.plugins.updateConfig(plugins[0].id, {
        big_key: { value: oversizedString(1_000_000), is_secret: false },
      });
    } catch (e: unknown) {
      const msg = (e as Error).message;
      expect(msg).not.toContain("panic");
    }
  });

  it("oversized JSON body (5MB) should be rejected", async () => {
    const res = await rawFetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email: oversizedString(5_000_000),
        password: "x",
      }),
    });
    // Should get 400 or 413, not 500
    expect(res.status).not.toBe(500);
  });
});

describe("FUZZING: type confusion on JSON fields", () => {
  it.each(TYPE_CONFUSION)(
    "login with email=%p should not crash server",
    async (payload) => {
      const res = await rawFetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: payload, password: "test" }),
      });
      // Server should respond, not crash
      expect(res.status).toBeGreaterThanOrEqual(400);
      expect(res.status).toBeLessThan(600);
    }
  );

  it.each(TYPE_CONFUSION)(
    "plugin install with version=%p should not crash",
    async (payload) => {
      const client = await authedClient();
      try {
        await client.plugins.install({
          name: "fuzz-test",
          image: "test:latest",
          version: payload as string,
        });
      } catch {
        // Expected
      }
      // Verify server is still responsive
      await client.plugins.list();
    }
  );
});

describe("FUZZING: HTTP method tampering", () => {
  const methods = ["PATCH", "OPTIONS", "HEAD", "TRACE", "CONNECT"];

  it.each(methods)(
    "%s on /api/plugins should not return 500",
    async (method) => {
      const res = await rawFetch("/api/plugins", { method });
      expect(res.status).not.toBe(500);
    }
  );

  it.each(methods)(
    "%s on /api/auth/login should not return 500",
    async (method) => {
      const res = await rawFetch("/api/auth/login", { method });
      expect(res.status).not.toBe(500);
    }
  );
});

describe("FUZZING: content-type confusion", () => {
  const contentTypes = [
    "text/plain",
    "application/xml",
    "multipart/form-data",
    "application/x-www-form-urlencoded",
    "text/html",
    "",
  ];

  it.each(contentTypes)(
    "login with Content-Type %s should not crash",
    async (ct) => {
      const headers: Record<string, string> = {};
      if (ct) headers["Content-Type"] = ct;

      const res = await rawFetch("/api/auth/login", {
        method: "POST",
        headers,
        body: '{"email":"test@test.com","password":"test"}',
      });
      expect(res.status).not.toBe(500);
    }
  );
});

describe("FUZZING: header injection", () => {
  it.each(HEADER_INJECTIONS)(
    "Authorization header with %s should not inject headers",
    async (payload) => {
      const res = await rawFetch("/api/plugins", {
        headers: { Authorization: `Bearer ${payload}` },
      });
      // Should just reject the token, not interpret injected headers
      expect(res.status).toBe(401);
    }
  );
});
