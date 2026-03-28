/** Normalise an alias/name: lowercase, trimmed, all @ removed. */
export function sanitizeAlias(s: string): string {
  return s.trim().toLowerCase().replaceAll("@", "");
}
