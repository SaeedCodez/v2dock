import * as React from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { BrandMark } from "@/components/brand";
import { Auth, errorOf } from "@/lib/api";
import { useAuth } from "@/lib/auth";

function AuthFrame({ title, subtitle, children }) {
  return (
    <div className="grid min-h-dvh place-items-center bg-background px-5 py-12">
      <div className="w-full max-w-[420px]">
        <div className="mb-8 flex flex-col items-center text-center">
          <BrandMark className="size-12 rounded-[14px]" />
          <h1 className="display mt-5 text-2xl font-semibold">{title}</h1>
          {subtitle ? <p className="mt-2 text-base text-muted-foreground">{subtitle}</p> : null}
        </div>
        <div className="rounded-2xl border border-border bg-card p-6 shadow-[0_1px_2px_rgb(0_0_0/0.04)] sm:p-7">{children}</div>
        <p className="mt-6 text-center text-xs text-muted-foreground">v2dock · single admin · traffic routing for Docker</p>
      </div>
    </div>
  );
}

function FormError({ children }) {
  if (!children) return null;
  return <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{children}</p>;
}

export function Login() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [password, setPassword] = React.useState("");
  const [code, setCode] = React.useState("");
  const [needCode, setNeedCode] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  if (!auth.loading && auth.setupRequired) return <Navigate to="/setup" replace />;
  if (!auth.loading && auth.authenticated) return <Navigate to="/overview" replace />;

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const res = await Auth.login(password, needCode ? code : "");
    setBusy(false);
    if (res.ok) {
      await auth.refresh();
      navigate("/overview", { replace: true });
      return;
    }
    if (res.body?.code === "totp_required") {
      setNeedCode(true);
      return;
    }
    setError(errorOf(res, "Login failed"));
  };

  return (
    <AuthFrame title="Sign in to v2dock" subtitle={needCode ? "Enter the 6-digit code from your authenticator app." : "Enter the admin password."}>
      <form onSubmit={submit} className="space-y-5">
        {!needCode ? (
          <div className="space-y-2">
            <Label htmlFor="pw">Password</Label>
            <Input id="pw" type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} className="h-11" />
          </div>
        ) : (
          <div className="space-y-2">
            <Label htmlFor="code">Authentication code</Label>
            <Input id="code" inputMode="numeric" autoComplete="one-time-code" autoFocus maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} className="h-11 text-center font-mono text-lg tracking-[0.4em]" />
          </div>
        )}
        <FormError>{error}</FormError>
        <Button type="submit" size="lg" className="w-full" disabled={busy || !password || (needCode && code.length !== 6)}>
          {busy ? <Loader2 className="animate-spin" /> : <KeyRound />}
          {needCode ? "Verify" : "Sign in"}
        </Button>
      </form>
    </AuthFrame>
  );
}

export function Setup() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [token, setToken] = React.useState("");
  const [pw, setPw] = React.useState("");
  const [pw2, setPw2] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  if (!auth.loading && !auth.setupRequired) return <Navigate to="/login" replace />;

  const submit = async (e) => {
    e.preventDefault();
    if (pw !== pw2) {
      setError("The passwords do not match.");
      return;
    }
    setBusy(true);
    setError("");
    const res = await Auth.setup(pw, token);
    setBusy(false);
    if (!res.ok) {
      setError(errorOf(res));
      return;
    }
    await auth.refresh();
    navigate("/overview", { replace: true });
  };

  return (
    <AuthFrame title="Set up v2dock" subtitle="Choose the admin password. This happens once.">
      <form onSubmit={submit} className="space-y-5">
        <div className="space-y-2">
          <Label htmlFor="token">Setup token</Label>
          <Input id="token" autoFocus value={token} onChange={(e) => setToken(e.target.value)} className="h-11 font-mono" placeholder="from the agent log" />
          <p className="text-xs text-muted-foreground">
            Find it with <code className="font-mono">docker compose logs agent | grep setupToken</code>. It proves you run this server.
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="pw">New password</Label>
          <Input id="pw" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} className="h-11" />
          <p className="text-xs text-muted-foreground">At least 12 characters. A long passphrase is best.</p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="pw2">Repeat password</Label>
          <Input id="pw2" type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} className="h-11" />
        </div>
        <FormError>{error}</FormError>
        <Button type="submit" size="lg" className="w-full" disabled={busy || pw.length < 12 || !token}>
          {busy ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
          Save and continue
        </Button>
      </form>
    </AuthFrame>
  );
}
