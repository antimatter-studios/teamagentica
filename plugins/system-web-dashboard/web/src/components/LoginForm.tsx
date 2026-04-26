import { useRef, useState, type FormEvent } from "react";
import { Loader2 } from "lucide-react";
import { useAuthStore } from "../stores/authStore";
import { useVantaWaves } from "./KoiBackground";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { cn } from "@/lib/utils";

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
    <div ref={pageRef} className="min-h-screen flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        <Card className="bg-card/95 backdrop-blur">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl tracking-widest">
              {(import.meta.env.VITE_APP_NAME || "TeamAgentica").toUpperCase()}
            </CardTitle>
            <p className="text-xs tracking-widest text-muted-foreground">
              AUTOMATION CONTROL PLATFORM
            </p>
          </CardHeader>

          <CardContent className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-2">
              <Button
                type="button"
                variant={mode === "login" ? "default" : "outline"}
                className={cn("tracking-widest")}
                onClick={() => {
                  setMode("login");
                  setError("");
                  setConfirmPassword("");
                }}
              >
                LOGIN
              </Button>
              <Button
                type="button"
                variant={mode === "register" ? "default" : "outline"}
                className={cn("tracking-widest")}
                onClick={() => {
                  setMode("register");
                  setError("");
                }}
              >
                REGISTER
              </Button>
            </div>

            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="email" className="tracking-widest text-xs">
                  EMAIL
                </Label>
                <Input
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
                <div className="flex flex-col gap-2">
                  <Label htmlFor="displayName" className="tracking-widest text-xs">
                    DISPLAY NAME
                  </Label>
                  <Input
                    id="displayName"
                    type="text"
                    value={displayName}
                    onChange={(e) => setDisplayName(e.target.value)}
                    placeholder="Operator handle"
                    required
                  />
                </div>
              )}

              <div className="flex flex-col gap-2">
                <Label htmlFor="password" className="tracking-widest text-xs">
                  PASSWORD
                </Label>
                <Input
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
                <div className="flex flex-col gap-2">
                  <Label htmlFor="confirmPassword" className="tracking-widest text-xs">
                    CONFIRM PASSWORD
                  </Label>
                  <Input
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

              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}

              <Button
                type="submit"
                disabled={loading}
                className="w-full tracking-widest"
              >
                {loading ? (
                  <span className="inline-flex items-center gap-2">
                    <Loader2 className="h-4 w-4 animate-spin" />
                    AUTHENTICATING...
                  </span>
                ) : mode === "login" ? (
                  "ACCESS SYSTEM"
                ) : (
                  "INITIALIZE ACCOUNT"
                )}
              </Button>
            </form>
          </CardContent>

          <CardFooter className="justify-center text-xs tracking-widest text-muted-foreground">
            <span className="mr-2 inline-block h-2 w-2 rounded-full bg-primary" />
            SYSTEM ONLINE
          </CardFooter>
        </Card>
      </div>
    </div>
  );
}
