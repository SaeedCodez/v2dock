import * as React from "react";
import { toast } from "sonner";
import { Copy, KeyRound, Loader2, LogOut, Plus, ShieldCheck, ShieldOff, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Mono, Page, PageHeader } from "@/components/page";
import { useConfirm } from "@/components/confirm";
import { Auth, Settings as SettingsApi, Tokens, errorOf } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { ago } from "@/lib/format";
import { useLive } from "@/lib/live";
import { usePoll } from "@/lib/use-api";

function Field({ label, hint, children }) {
  return (
    <div className="grid gap-2 sm:grid-cols-[220px_1fr] sm:items-start sm:gap-6">
      <div className="pt-2">
        <Label>{label}</Label>
      </div>
      <div className="space-y-1.5">
        {children}
        {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      </div>
    </div>
  );
}

function ToggleRow({ title, description, checked, onChange }) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-6">
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-sm text-muted-foreground">{description}</span>
      </span>
      <Switch checked={checked} onCheckedChange={onChange} className="mt-1" />
    </label>
  );
}

function RoutingSettings() {
  const { data, reload } = usePoll(() => SettingsApi.get(), []);
  const { reload: reloadStatus } = useLive();
  const [form, setForm] = React.useState(null);
  const [busy, setBusy] = React.useState(false);
  React.useEffect(() => {
    if (data) setForm(data);
  }, [data]);
  if (!form) return <Card className="h-64" />;

  const save = async (patch) => {
    setBusy(true);
    const res = await SettingsApi.put(patch);
    setBusy(false);
    if (!res.ok) {
      toast.error(errorOf(res));
      return false;
    }
    toast.success("Saved");
    reload();
    reloadStatus();
    return true;
  };
  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target ? e.target.value : e }));
  const dirty = data && ["remoteDns", "domesticDns", "testUrl", "failoverThreshold", "failoverScope", "healthIntervalSec"].some((k) => String(form[k]) !== String(data[k]));

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Routing &amp; failover</CardTitle>
          <CardDescription>How routed containers reach the internet and what happens when the server dies.</CardDescription>
        </div>
      </CardHeader>
      <CardContent className="space-y-6">
        <ToggleRow
          title="Iranian sites go direct"
          description="Domains under .ir, the geosite:ir list and Iranian IP ranges skip the VPN."
          checked={form.bypassIran}
          onChange={(v) => save({ bypassIran: v })}
        />
        <ToggleRow
          title="Automatic failover"
          description="When the active server fails several health checks in a row, test the others and switch to the fastest one that works."
          checked={form.autoFailover}
          onChange={(v) => save({ autoFailover: v })}
        />
        <div className="space-y-5 border-t border-border pt-6">
          <Field label="Fail after" hint="Consecutive failed health checks before switching.">
            <Input type="number" min={1} max={20} value={form.failoverThreshold} onChange={set("failoverThreshold")} className="max-w-[140px]" />
          </Field>
          <Field label="Check every" hint="Seconds between health checks of the active server (10–600).">
            <Input type="number" min={10} max={600} value={form.healthIntervalSec} onChange={set("healthIntervalSec")} className="max-w-[140px]" />
          </Field>
          <Field label="Switch to">
            <Select value={form.failoverScope} onValueChange={set("failoverScope")}>
              <SelectTrigger className="max-w-[320px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">Any server (same subscription first)</SelectItem>
                <SelectItem value="subscription">Only the same subscription</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Test URL" hint="Fetched through a server to measure real delay. Must answer quickly (HTTP 204 is ideal).">
            <Input value={form.testUrl} onChange={set("testUrl")} className="font-mono text-sm" />
          </Field>
          <Field label="Remote DNS" hint="Resolver for international names, queried through the VPN (https://, tcp:// or an IP).">
            <Input value={form.remoteDns} onChange={set("remoteDns")} className="font-mono text-sm" />
          </Field>
          <Field label="Domestic DNS" hint="Resolver for Iranian names, queried directly. 'localhost' uses the server's own resolver.">
            <Input value={form.domesticDns} onChange={set("domesticDns")} className="font-mono text-sm" />
          </Field>
        </div>
      </CardContent>
      <CardFooter className="justify-end">
        <Button variant="ghost" disabled={!dirty || busy} onClick={() => setForm(data)}>
          Reset
        </Button>
        <Button
          disabled={!dirty || busy}
          onClick={() =>
            save({
              remoteDns: form.remoteDns,
              domesticDns: form.domesticDns,
              testUrl: form.testUrl,
              failoverThreshold: Number(form.failoverThreshold),
              failoverScope: form.failoverScope,
              healthIntervalSec: Number(form.healthIntervalSec),
            })
          }
        >
          {busy ? <Loader2 className="animate-spin" /> : null} Save changes
        </Button>
      </CardFooter>
    </Card>
  );
}

function PasswordCard() {
  const [cur, setCur] = React.useState("");
  const [next, setNext] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    const res = await Auth.changePassword(cur, next);
    setBusy(false);
    if (!res.ok) {
      toast.error(errorOf(res));
      return;
    }
    toast.success("Password changed. Other sessions were signed out.");
    setCur("");
    setNext("");
  };
  return (
    <Card>
      <form onSubmit={submit}>
        <CardHeader>
          <div>
            <CardTitle>Password</CardTitle>
            <CardDescription>Changing it signs out every other browser.</CardDescription>
          </div>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="cur">Current password</Label>
            <Input id="cur" type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new">New password</Label>
            <Input id="new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
            <p className="text-xs text-muted-foreground">At least 12 characters.</p>
          </div>
        </CardContent>
        <CardFooter className="justify-end">
          <Button type="submit" disabled={busy || !cur || next.length < 12}>
            {busy ? <Loader2 className="animate-spin" /> : <KeyRound />} Change password
          </Button>
        </CardFooter>
      </form>
    </Card>
  );
}

function TwoFactorCard() {
  const auth = useAuth();
  const [setup, setSetup] = React.useState(null);
  const [code, setCode] = React.useState("");
  const [disable, setDisable] = React.useState(false);
  const [pw, setPw] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const begin = async () => {
    const res = await Auth.totpSetup();
    if (!res.ok) return toast.error(errorOf(res));
    setCode("");
    setSetup(res.body);
  };
  const enable = async (e) => {
    e.preventDefault();
    setBusy(true);
    const res = await Auth.totpEnable(code);
    setBusy(false);
    if (!res.ok) return toast.error(errorOf(res));
    toast.success("Two-factor authentication is on");
    setSetup(null);
    auth.refresh();
  };
  const off = async (e) => {
    e.preventDefault();
    setBusy(true);
    const res = await Auth.totpDisable(pw, code);
    setBusy(false);
    if (!res.ok) return toast.error(errorOf(res));
    toast.success("Two-factor authentication is off");
    setDisable(false);
    setPw("");
    setCode("");
    auth.refresh();
  };

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle className="flex items-center gap-2.5">
            Two-factor authentication
            {auth.totpEnabled ? <Badge variant="success">on</Badge> : <Badge variant="secondary">off</Badge>}
          </CardTitle>
          <CardDescription>Ask for a code from an authenticator app (Google Authenticator, Aegis, 1Password…) at every login.</CardDescription>
        </div>
        {auth.totpEnabled ? (
          <Button variant="outline" onClick={() => setDisable(true)}>
            <ShieldOff /> Turn off
          </Button>
        ) : (
          <Button onClick={begin}>
            <ShieldCheck /> Turn on
          </Button>
        )}
      </CardHeader>

      <Dialog open={!!setup} onOpenChange={(o) => !o && setSetup(null)}>
        <DialogContent className="sm:max-w-[460px]">
          <form onSubmit={enable} className="space-y-5">
            <DialogHeader>
              <DialogTitle>Scan with your authenticator</DialogTitle>
              <DialogDescription>Then enter the 6-digit code it shows.</DialogDescription>
            </DialogHeader>
            {setup?.qr ? <img src={setup.qr} alt="QR code for the authenticator app" className="mx-auto size-56 rounded-xl bg-white p-2" /> : null}
            <div className="space-y-1.5">
              <Label>Or enter this key</Label>
              <Mono className="block rounded-lg bg-secondary px-3 py-2">{setup?.secret}</Mono>
            </div>
            <Input inputMode="numeric" autoFocus maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} placeholder="123456" className="h-11 text-center font-mono text-lg tracking-[0.4em]" />
            <DialogFooter>
              <Button type="submit" disabled={busy || code.length !== 6}>
                {busy ? <Loader2 className="animate-spin" /> : null} Turn on
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={disable} onOpenChange={setDisable}>
        <DialogContent className="sm:max-w-[440px]">
          <form onSubmit={off} className="space-y-5">
            <DialogHeader>
              <DialogTitle>Turn off two-factor authentication</DialogTitle>
              <DialogDescription>Confirm with your password and a current code.</DialogDescription>
            </DialogHeader>
            <Input type="password" autoComplete="current-password" placeholder="Password" value={pw} onChange={(e) => setPw(e.target.value)} />
            <Input inputMode="numeric" maxLength={6} placeholder="Code" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} className="font-mono" />
            <DialogFooter>
              <Button type="submit" variant="destructive" disabled={busy || !pw || code.length !== 6}>
                Turn off
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

function TokensCard() {
  const { data, reload } = usePoll(() => Tokens.list(), []);
  const confirm = useConfirm();
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [scope, setScope] = React.useState("read");
  const [created, setCreated] = React.useState("");

  const create = async (e) => {
    e.preventDefault();
    const res = await Tokens.create(name, scope);
    if (!res.ok) return toast.error(errorOf(res));
    setCreated(res.body.token);
    setName("");
    reload();
  };
  const revoke = async (t) => {
    const ok = await confirm({ title: `Revoke "${t.name}"?`, description: "Anything using this token loses access immediately.", confirmLabel: "Revoke", destructive: true });
    if (!ok) return;
    const res = await Tokens.revoke(t.id);
    if (!res.ok) toast.error(errorOf(res));
    reload();
  };

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>API tokens</CardTitle>
          <CardDescription>
            For the CLI or an AI agent on another machine: <Mono>v2dock --url https://… --token …</Mono>. Read-only tokens can look but not change anything; no token can manage passwords or tokens.
          </CardDescription>
        </div>
        <Button
          variant="outline"
          onClick={() => {
            setCreated("");
            setOpen(true);
          }}
        >
          <Plus /> New token
        </Button>
      </CardHeader>
      <CardContent className="px-0 pb-2">
        {data?.tokens?.length ? (
          <div className="divide-y divide-border border-t border-border">
            {data.tokens.map((t) => (
              <div key={t.id} className="flex items-center justify-between gap-4 px-6 py-3">
                <div>
                  <div className="flex items-center gap-2 font-medium">
                    {t.name} <Badge variant={t.scope === "admin" ? "warning" : "secondary"}>{t.scope === "admin" ? "full access" : "read-only"}</Badge>
                  </div>
                  <div className="text-xs text-muted-foreground">
                    created {ago(t.createdAt)} · last used {ago(t.lastUsedAt)}
                  </div>
                </div>
                <Button variant="ghost" size="icon-sm" aria-label="Revoke" onClick={() => revoke(t)}>
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        ) : (
          <p className="border-t border-border px-6 py-4 text-sm text-muted-foreground">No tokens.</p>
        )}
      </CardContent>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-[480px]">
          {created ? (
            <div className="space-y-5">
              <DialogHeader>
                <DialogTitle>Copy your token now</DialogTitle>
                <DialogDescription>It is shown only once. Only its hash is stored.</DialogDescription>
              </DialogHeader>
              <Mono className="block rounded-lg bg-secondary px-3 py-2.5">{created}</Mono>
              <DialogFooter>
                <Button
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(created);
                      toast.success("Copied");
                    } catch {
                      toast.error("Copy failed; select the text instead");
                    }
                  }}
                >
                  <Copy /> Copy
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={create} className="space-y-5">
              <DialogHeader>
                <DialogTitle>New API token</DialogTitle>
              </DialogHeader>
              <div className="space-y-2">
                <Label htmlFor="tname">Name</Label>
                <Input id="tname" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. claude-agent" maxLength={60} />
              </div>
              <div className="space-y-2">
                <Label>Access</Label>
                <Select value={scope} onValueChange={setScope}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="read">Read-only (status, servers, usage)</SelectItem>
                    <SelectItem value="admin">Full access (connect, route, settings)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <DialogFooter>
                <Button type="submit" disabled={!name.trim()}>
                  Create
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </Card>
  );
}

function SessionsCard() {
  const confirm = useConfirm();
  const auth = useAuth();
  const revoke = async () => {
    const ok = await confirm({ title: "Sign out everywhere?", description: "Every browser session ends, including this one.", confirmLabel: "Sign out everywhere", destructive: true });
    if (!ok) return;
    await Auth.revokeSessions();
    auth.refresh();
  };
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Sessions</CardTitle>
          <CardDescription>Sessions last 7 days and end after 24 hours without use.</CardDescription>
        </div>
        <Button variant="outline" onClick={revoke}>
          <LogOut /> Sign out everywhere
        </Button>
      </CardHeader>
    </Card>
  );
}

export default function SettingsPage() {
  const { status } = useLive();
  return (
    <Page>
      <PageHeader title="Settings" description="Routing behaviour and access to this dashboard." />
      <div className="space-y-6">
        <RoutingSettings />
        <h2 className="display pt-4 text-lg font-semibold">Security</h2>
        <PasswordCard />
        <TwoFactorCard />
        <TokensCard />
        <SessionsCard />
        <p className="pt-2 text-center text-xs text-muted-foreground">v2dock {status?.version}</p>
      </div>
    </Page>
  );
}
