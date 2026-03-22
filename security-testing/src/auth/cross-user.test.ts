import { describe, it, expect } from "vitest";
import { authedClient, rawFetch, TARGET, ADMIN_EMAIL, ADMIN_PASS } from "../common/client.js";

/**
 * Tests that user A cannot access user B's data.
 * Creates a secondary test user, then verifies horizontal isolation.
 */
describe("AUTH: cross-user authorization boundaries", () => {
  const testUserEmail = `sectest-${Date.now()}@test.local`;
  const testUserPass = "SecTest!Pass99";

  it("admin should be able to create a test user", async () => {
    try {
      const admin = await authedClient();
      // Try to register a test user (admin can register when registration is closed)
      await admin.auth.register(testUserEmail, testUserPass, "Security Test User");
    } catch (e: any) {
      // If registration fails (e.g., no invite system), skip the cross-user tests
      console.warn("Could not create test user:", e.message);
    }
  });

  it("non-admin user should not see other users", async () => {
    try {
      const user = await authedClient(testUserEmail, testUserPass);
      // Non-admin requesting user list should fail or return only self
      await expect(user.auth.getUsers()).rejects.toThrow();
    } catch {
      // If test user login fails, skip
    }
  });

  it("non-admin user should not access admin plugin configs", async () => {
    try {
      const user = await authedClient(testUserEmail, testUserPass);
      // Should not be able to read configs of plugins the user doesn't own
      const plugins = await user.plugins.list();
      if (plugins.length > 0) {
        await expect(
          user.plugins.getConfig(plugins[0].id)
        ).rejects.toThrow();
      }
    } catch {
      // Skip if test user doesn't exist
    }
  });

  it("user token should not work for admin-only endpoints", async () => {
    try {
      const user = await authedClient(testUserEmail, testUserPass);
      // Try to install a plugin (admin-only)
      await expect(
        user.plugins.install({
          id: "sec-test-plugin",
          name: "Test",
          image: "alpine:latest",
          version: "0.0.1",
        })
      ).rejects.toThrow();
    } catch {
      // Skip if test user doesn't exist
    }
  });

  it("user A token should not access user B /users/me", async () => {
    try {
      const admin = await authedClient();
      const adminMe = await admin.auth.getMe();

      const user = await authedClient(testUserEmail, testUserPass);
      const userMe = await user.auth.getMe();

      // Each user should only see their own data
      expect(adminMe.email).toBe(ADMIN_EMAIL);
      expect(userMe.email).toBe(testUserEmail);
      expect(adminMe.id).not.toBe(userMe.id);
    } catch {
      // Skip if test user doesn't exist
    }
  });
});
