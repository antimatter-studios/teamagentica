import { getStoredToken } from "../api/auth";

// Derive the code editor subdomain from the API host.
// api.teamagentica.localhost → code.teamagentica.localhost
const apiHost =
  import.meta.env.VITE_TEAMAGENTICA_KERNEL_HOST || "api.teamagentica.localhost";
const baseDomain = apiHost.replace(/^[^.]+\./, "");
const CODE_HOST = `//${import.meta.env.VITE_CODE_EDITOR_HOST || `code.${baseDomain}`}`;

export default function CodeEditor() {
  const token = getStoredToken();
  // Pass the token as a one-time query param so the code editor's auth
  // middleware can set its own cookie (cross-subdomain cookies don't work on .localhost).
  const src = token ? `${CODE_HOST}/?tkn=${encodeURIComponent(token)}` : `${CODE_HOST}/`;

  return (
    <div className="code-editor-container">
      <iframe
        src={src}
        className="code-editor-iframe"
        title="Code Editor"
        allow="clipboard-read; clipboard-write"
      />
    </div>
  );
}
