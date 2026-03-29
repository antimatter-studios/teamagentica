/** Normalise a name/alias/id: lowercase, trim, strip @,
 *  replace any non-alphabetical character with underscore,
 *  collapse consecutive underscores, trim leading/trailing underscores. */
export function sanitizeAlias(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .replaceAll("@", "")
    .replace(/[^a-z]/g, "_")
    .replace(/_+/g, "_")
    .replace(/^_|_$/g, "");
}
