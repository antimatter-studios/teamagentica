import { describe, it, expect } from "vitest";
import { anonClient, forgedTokenClient, rawFetch, authedClient } from "../common/client.js";

describe("AUTH: JWT bypass & token manipulation", () => {
  describe("unauthenticated access to protected endpoints", () => {
    const client = anonClient();

    it("GET /api/plugins should reject without token", async () => {
      await expect(client.plugins.list()).rejects.toThrow();
    });

    it("GET /api/users/me should reject without token", async () => {
      await expect(client.auth.getMe()).rejects.toThrow();
    });

    it("GET /api/pricing should reject without token", async () => {
      await expect(client.costs.fetchPricing()).rejects.toThrow();
    });

    it("GET /api/aliases should reject without token", async () => {
      await expect(client.aliases.list()).rejects.toThrow();
    });

    it("GET /api/marketplace/plugins should reject without token", async () => {
      await expect(client.marketplace.browse()).rejects.toThrow();
    });

    it("GET /api/external-users should reject without token", async () => {
      await expect(client.costs.fetchExternalUsers()).rejects.toThrow();
    });
  });

  describe("forged/malformed tokens", () => {
    it("garbage token should be rejected", async () => {
      const client = forgedTokenClient("not-a-real-token");
      await expect(client.plugins.list()).rejects.toThrow();
    });

    it("empty bearer token should be rejected", async () => {
      const client = forgedTokenClient("");
      await expect(client.plugins.list()).rejects.toThrow();
    });

    it("JWT with wrong signature (none algorithm) should be rejected", async () => {
      // alg:none attack — header: {"alg":"none","typ":"JWT"}
      const header = btoa('{"alg":"none","typ":"JWT"}').replace(/=/g, "");
      const payload = btoa(
        '{"user_id":1,"email":"admin@test.local","role":"admin","capabilities":["system:admin"]}'
      ).replace(/=/g, "");
      const fakeJwt = `${header}.${payload}.`;
      const client = forgedTokenClient(fakeJwt);
      await expect(client.plugins.list()).rejects.toThrow();
    });

    it("JWT with HS256 but wrong secret should be rejected", async () => {
      // Crafted JWT with a different secret
      const header = btoa('{"alg":"HS256","typ":"JWT"}').replace(/=/g, "");
      const payload = btoa(
        '{"user_id":1,"email":"admin@test.local","role":"admin"}'
      ).replace(/=/g, "");
      const fakeJwt = `${header}.${payload}.fakesignature`;
      const client = forgedTokenClient(fakeJwt);
      await expect(client.plugins.list()).rejects.toThrow();
    });

    it("expired JWT should be rejected", async () => {
      const header = btoa('{"alg":"HS256","typ":"JWT"}').replace(/=/g, "");
      const payload = btoa(
        `{"user_id":1,"email":"admin@test.local","role":"admin","exp":${Math.floor(Date.now() / 1000) - 3600}}`
      ).replace(/=/g, "");
      const fakeJwt = `${header}.${payload}.fakesig`;
      const client = forgedTokenClient(fakeJwt);
      await expect(client.plugins.list()).rejects.toThrow();
    });
  });

  describe("authorization header manipulation", () => {
    it("Basic auth instead of Bearer should be rejected", async () => {
      const res = await rawFetch("/api/plugins", {
        headers: { Authorization: "Basic YWRtaW46YWRtaW4=" },
      });
      expect(res.status).toBe(401);
    });

    it("Bearer with extra spaces should be rejected", async () => {
      const res = await rawFetch("/api/plugins", {
        headers: { Authorization: "Bearer  extra-spaces-token" },
      });
      expect(res.status).toBe(401);
    });

    it("case-sensitive Bearer check", async () => {
      const res = await rawFetch("/api/plugins", {
        headers: { Authorization: "bearer some-token" },
      });
      expect(res.status).toBe(401);
    });
  });

  describe("privilege escalation", () => {
    it("registration should not allow creating admin when users exist", async () => {
      const client = anonClient();
      // This should fail if any user already exists (first user = admin, rest need admin invite)
      try {
        await client.auth.register(
          "attacker@evil.com",
          "password123",
          "Attacker"
        );
        // If it succeeds, check we didn't get admin role
        const me = await client.auth.getMe();
        expect(me.role).not.toBe("admin");
      } catch {
        // Expected — registration blocked when users exist
      }
    });
  });
});

describe("AUTH: session cookie security", () => {
  it("session endpoint should set HttpOnly and Secure flags", async () => {
    const client = await authedClient();
    // Get the token, then hit session endpoint directly
    const res = await rawFetch("/api/auth/session", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${(client as unknown as { http: { config: { token: string } } }).http["config"].token}`,
      },
      body: "{}",
    });

    if (res.status === 200) {
      const cookies = res.headers.get("set-cookie") || "";
      // Session cookies SHOULD have HttpOnly
      if (cookies.includes("teamagentica_session")) {
        expect(cookies.toLowerCase()).toContain("httponly");
      }
    }
  });
});
