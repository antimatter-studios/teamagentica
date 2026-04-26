import { useState } from "react";
import { Trash2, Check, Plus, Eye, Download, Loader2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  useTheme,
  parseThemeCss,
  parseTweakcnHtml,
  fetchTweakcnHtml,
  slugify,
} from "@/hooks/useTheme";

export default function ThemeManager() {
  const { baseColor, setBaseColor, customThemes, addTheme, removeTheme } = useTheme();

  const [label, setLabel] = useState("");
  const [css, setCss] = useState("");
  const [url, setUrl] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [fetching, setFetching] = useState(false);

  const reset = () => {
    setLabel("");
    setCss("");
    setUrl("");
    setError(null);
    setInfo(null);
  };

  const handleFetch = async () => {
    setError(null);
    setInfo(null);
    const trimmed = url.trim();
    if (!trimmed) { setError("Paste a tweakcn theme URL."); return; }
    if (!/^https?:\/\//.test(trimmed)) { setError("URL must start with http:// or https://"); return; }
    setFetching(true);
    try {
      const html = await fetchTweakcnHtml(trimmed);
      const parsed = parseTweakcnHtml(html);
      if (!parsed) {
        setError("Couldn't find theme data on that page. Use the Paste CSS tab as a fallback.");
        return;
      }
      if (!label.trim()) setLabel(parsed.label);
      const block =
        `:root {\n  ${parsed.lightVars.split("; ").join(";\n  ")}\n}\n\n` +
        `.dark {\n  ${parsed.darkVars.split("; ").join(";\n  ")}\n}`;
      setCss(block);
      setInfo(`Imported "${parsed.label}". Review below and click Install.`);
    } catch (e) {
      setError(`Fetch failed: ${e instanceof Error ? e.message : String(e)}. Use Paste CSS instead.`);
    } finally {
      setFetching(false);
    }
  };

  const handleInstall = () => {
    setError(null);
    setInfo(null);
    const trimmedLabel = label.trim();
    if (!trimmedLabel) { setError("Please give the theme a name."); return; }
    const id = slugify(trimmedLabel);
    if (!id) { setError("Theme name must contain alphanumeric characters."); return; }
    if (customThemes.some((b) => b.id === id)) {
      setError(`A theme with id "${id}" already exists. Choose a different name.`);
      return;
    }
    const { lightVars, darkVars } = parseThemeCss(css);
    if (!lightVars && !darkVars) {
      setError('No CSS variables found. Expected ":root { ... } .dark { ... }".');
      return;
    }
    addTheme({ id, label: trimmedLabel, lightVars, darkVars });
    setBaseColor(id);
    setInfo(`Installed "${trimmedLabel}". Now active.`);
    setLabel("");
    setCss("");
    setUrl("");
  };

  return (
    <div className="flex w-full flex-col gap-4 p-6">
      <div>
        <h1 className="text-xl font-semibold">Themes</h1>
        <p className="text-sm text-muted-foreground">
          Install themes from{" "}
          <a href="https://tweakcn.com" target="_blank" rel="noreferrer" className="underline">tweakcn.com</a>{" "}
          or{" "}
          <a href="https://ui.shadcn.com/themes" target="_blank" rel="noreferrer" className="underline">ui.shadcn.com/themes</a>.
          Custom themes are stored in your browser only.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Installed themes</CardTitle>
          <CardDescription>
            {customThemes.length + 1} total. Click to switch. Trash icon removes a custom theme.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ul className="flex flex-col gap-1">
            <li className="group flex items-center justify-between rounded-md px-3 py-2 hover:bg-accent">
              <button
                type="button"
                onClick={() => setBaseColor(null)}
                className="flex flex-1 items-center gap-2 text-left"
              >
                {baseColor === null
                  ? <Check className="h-4 w-4 text-primary" />
                  : <Eye className="h-4 w-4 opacity-40" />}
                <span>Default</span>
                {baseColor === null && <Badge variant="secondary" className="text-[10px]">Active</Badge>}
              </button>
            </li>
            {customThemes.map((t) => {
              const active = t.id === baseColor;
              return (
                <li
                  key={t.id}
                  className="group flex items-center justify-between rounded-md px-3 py-2 hover:bg-accent"
                >
                  <button
                    type="button"
                    onClick={() => setBaseColor(t.id)}
                    className="flex flex-1 items-center gap-2 text-left"
                  >
                    {active ? <Check className="h-4 w-4 text-primary" /> : <Eye className="h-4 w-4 opacity-40" />}
                    <span>{t.label}</span>
                    {active && <Badge variant="secondary" className="text-[10px]">Active</Badge>}
                  </button>
                  <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="opacity-0 group-hover:opacity-100"
                          title="Delete theme"
                        >
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Delete "{t.label}"?</AlertDialogTitle>
                          <AlertDialogDescription>
                            This removes the theme from your browser. You can re-install it any time.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction onClick={() => removeTheme(t.id)}>Delete</AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                </li>
              );
            })}
          </ul>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Add a theme</CardTitle>
          <CardDescription>
            Paste a tweakcn URL (easy) or paste the CSS variables block (advanced fallback).
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Tabs defaultValue="url">
            <TabsList>
              <TabsTrigger value="url">From URL</TabsTrigger>
              <TabsTrigger value="css">Paste CSS</TabsTrigger>
            </TabsList>

            <TabsContent value="url" className="flex flex-col gap-3 pt-3">
              <div className="flex flex-col gap-2">
                <Label htmlFor="theme-url">Theme URL</Label>
                <div className="flex gap-2">
                  <Input
                    id="theme-url"
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                    placeholder="https://tweakcn.com/themes/cmllfu8oc000004l1a0tidj2g"
                    disabled={fetching}
                  />
                  <Button onClick={handleFetch} disabled={fetching}>
                    {fetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                    Fetch
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">
                  Fetched server-side via the dashboard plugin's /api/fetch endpoint to bypass browser CORS. If the fetch fails, switch to Paste CSS.
                </p>
              </div>
            </TabsContent>

            <TabsContent value="css" className="pt-3">
              <p className="text-xs text-muted-foreground mb-2">
                On tweakcn / ui.shadcn.com, click their "Copy CSS Variables" button and paste the full block below.
              </p>
            </TabsContent>
          </Tabs>

          <div className="flex flex-col gap-2">
            <Label htmlFor="theme-label">Theme name</Label>
            <Input
              id="theme-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. Sunset Pop"
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="theme-css">CSS variables block</Label>
            <Textarea
              id="theme-css"
              value={css}
              onChange={(e) => setCss(e.target.value)}
              placeholder=":root { --background: hsl(0 0% 100%); ... }&#10;.dark { --background: hsl(...); ... }"
              className="min-h-[260px] font-mono text-xs"
            />
          </div>

          {info && <Alert><AlertDescription>{info}</AlertDescription></Alert>}
          {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}

          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={reset}>Reset</Button>
            <Button onClick={handleInstall} disabled={!css.trim() || !label.trim()}>
              <Plus className="h-4 w-4" /> Install theme
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
