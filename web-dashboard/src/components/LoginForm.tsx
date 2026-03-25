import { useRef, useState, type FormEvent } from "react";
import { useAuthStore } from "../stores/authStore";
import { useVantaWaves } from "./KoiBackground";

export default function LoginForm() {
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const pageRef = useRef<HTMLDivElement>(null);
  useVantaWaves(pageRef);
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);

    try {
      if (mode === "register" && password !== confirmPassword) {
        setError("Passwords do not match");
        setLoading(false);
        return;
      }
      if (mode === "login") {
        await login(email, password);
      } else {
        await register(email, password, displayName);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="login-page" ref={pageRef}>
      <div className="login-container">
        <div className="login-card">
          <div className="login-header">
            <h1 className="login-title">{(import.meta.env.VITE_APP_NAME || "TeamAgentica").toUpperCase()}</h1>
            <p className="login-subtitle">AUTOMATION CONTROL PLATFORM</p>
          </div>

          <div className="login-tabs">
            <button
              className={`login-tab ${mode === "login" ? "active" : ""}`}
              onClick={() => {
                setMode("login");
                setError("");
                setConfirmPassword("");
              }}
              type="button"
            >
              LOGIN
            </button>
            <button
              className={`login-tab ${mode === "register" ? "active" : ""}`}
              onClick={() => {
                setMode("register");
                setError("");
              }}
              type="button"
            >
              REGISTER
            </button>
          </div>

          <form onSubmit={handleSubmit} className="login-form">
            <div className="form-field">
              <label htmlFor="email">EMAIL</label>
              <input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="operator@teamagentica.io"
                required
                autoComplete="email"
              />
            </div>

            {mode === "register" && (
              <div className="form-field">
                <label htmlFor="displayName">DISPLAY NAME</label>
                <input
                  id="displayName"
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  placeholder="Operator handle"
                  required
                />
              </div>
            )}

            <div className="form-field">
              <label htmlFor="password">PASSWORD</label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••••"
                required
                autoComplete={
                  mode === "login" ? "current-password" : "new-password"
                }
              />
            </div>

            {mode === "register" && (
              <div className="form-field">
                <label htmlFor="confirmPassword">CONFIRM PASSWORD</label>
                <input
                  id="confirmPassword"
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  placeholder="••••••••••"
                  required
                  autoComplete="new-password"
                />
              </div>
            )}

            {error && <div className="form-error">{error}</div>}

            <button
              type="submit"
              className="login-submit"
              disabled={loading}
            >
              {loading ? (
                <span className="loading-text">
                  <span className="spinner" />
                  AUTHENTICATING...
                </span>
              ) : mode === "login" ? (
                "ACCESS SYSTEM"
              ) : (
                "INITIALIZE ACCOUNT"
              )}
            </button>
          </form>

          <div className="login-footer">
            <span className="status-dot" />
            SYSTEM ONLINE
          </div>
        </div>
      </div>
    </div>
  );
}
