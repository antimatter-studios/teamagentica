import { TeamAgenticaClient } from "@teamagentica/api-client";

const TARGET = process.env.TARGET_URL || "http://api.teamagentica.localhost";
const ADMIN_EMAIL = process.env.ADMIN_EMAIL || "admin@test.local";
const ADMIN_PASS = process.env.ADMIN_PASSWORD || "admin123";

/** Unauthenticated client — no token. */
export function anonClient(): TeamAgenticaClient {
  return new TeamAgenticaClient({ baseUrl: TARGET });
}

/** Authenticated client — logs in and stores token. */
export async function authedClient(
  email = ADMIN_EMAIL,
  password = ADMIN_PASS
): Promise<TeamAgenticaClient> {
  const client = anonClient();
  await client.auth.login(email, password);
  return client;
}

/** Client with an arbitrary/forged token. */
export function forgedTokenClient(token: string): TeamAgenticaClient {
  return new TeamAgenticaClient({ baseUrl: TARGET, token });
}

/** Raw fetch helper for endpoints not covered by the API client. */
export async function rawFetch(
  path: string,
  opts: RequestInit = {}
): Promise<Response> {
  return fetch(`${TARGET}${path}`, opts);
}

/** Raw fetch with auth token. */
export async function rawFetchAuthed(
  path: string,
  token: string,
  opts: RequestInit = {}
): Promise<Response> {
  return fetch(`${TARGET}${path}`, {
    ...opts,
    headers: {
      ...opts.headers,
      Authorization: `Bearer ${token}`,
    },
  });
}

export { TARGET, ADMIN_EMAIL, ADMIN_PASS };
