/**
 * Thin JSON client. Every call resolves to `{ status, ok, body }`; errors are
 * values, never exceptions. State-changing calls carry the CSRF header the
 * agent requires for cookie sessions (a cross-site page cannot add it).
 */
async function req(method, url, body) {
  const opt = { method, headers: { "X-V2dock-Csrf": "1" }, credentials: "same-origin" };
  if (body !== undefined) {
    opt.headers["Content-Type"] = "application/json";
    opt.body = JSON.stringify(body);
  }
  let r;
  try {
    r = await fetch(url, opt);
  } catch (err) {
    return { status: 0, ok: false, body: { ok: false, code: "network", error: "Could not reach v2dock: " + err.message } };
  }
  const ct = r.headers.get("content-type") || "";
  let parsed;
  if (ct.includes("application/json")) {
    parsed = await r.json().catch(() => ({}));
  } else {
    parsed = { error: (await r.text().catch(() => "")) || r.statusText };
  }
  if (r.status === 401 && parsed?.code === "unauthorized" && !url.startsWith("/api/auth/")) {
    window.dispatchEvent(new Event("v2dock:unauthorized"));
  }
  return { status: r.status, ok: r.ok, body: parsed };
}

export const api = {
  get: (u) => req("GET", u),
  post: (u, b) => req("POST", u, b === undefined ? {} : b),
  put: (u, b) => req("PUT", u, b),
  del: (u) => req("DELETE", u),
};

export function errorOf(res, fallback = "Something went wrong.") {
  return (res && res.body && (res.body.error || res.body.message)) || fallback;
}

const q = encodeURIComponent;

export const Auth = {
  status: () => api.get("/api/auth/status"),
  setup: (password, setupToken) => api.post("/api/auth/setup", { password, setupToken }),
  login: (password, code) => api.post("/api/auth/login", { password, code }),
  logout: () => api.post("/api/auth/logout"),
  changePassword: (currentPassword, newPassword) => api.post("/api/auth/password", { currentPassword, newPassword }),
  revokeSessions: () => api.post("/api/auth/sessions/revoke"),
  totpSetup: () => api.post("/api/auth/totp/setup"),
  totpEnable: (code) => api.post("/api/auth/totp/enable", { code }),
  totpDisable: (password, code) => api.post("/api/auth/totp/disable", { password, code }),
};

export const Tokens = {
  list: () => api.get("/api/tokens"),
  create: (name, scope) => api.post("/api/tokens", { name, scope }),
  revoke: (id) => api.del(`/api/tokens/${q(id)}`),
};

export const Subs = {
  list: () => api.get("/api/subscriptions"),
  create: (b) => api.post("/api/subscriptions", b),
  update: (id, b) => api.put(`/api/subscriptions/${q(id)}`, b),
  remove: (id) => api.del(`/api/subscriptions/${q(id)}`),
  refresh: (id) => api.post(`/api/subscriptions/${q(id)}/refresh`),
};

export const Servers = {
  list: (sub) => api.get("/api/servers" + (sub ? `?subscription=${q(sub)}` : "")),
  add: (link, name) => api.post("/api/servers", { link, name }),
  remove: (id) => api.del(`/api/servers/${q(id)}`),
  test: (kind, subscription, ids) => api.post("/api/servers/test", { kind, subscription, ids }),
  testStatus: () => api.get("/api/servers/test"),
  cancelTest: () => api.del("/api/servers/test"),
  connect: (serverId) => api.post("/api/connect", { serverId }),
  connectBest: (subscription) => api.post("/api/connect", { best: true, subscription }),
  disconnect: () => api.post("/api/disconnect"),
};

export const Containers = {
  list: () => api.get("/api/containers"),
  route: (key, quota) => api.put("/api/routes", { key, ...quota }),
  unroute: (key) => api.del(`/api/routes?key=${q(key)}`),
  reset: (key) => api.post("/api/routes/reset", { key }),
};

export const Usage = {
  get: (range, key) => api.get(`/api/usage?range=${q(range)}` + (key ? `&key=${q(key)}` : "")),
};

export const Settings = {
  get: () => api.get("/api/settings"),
  put: (b) => api.put("/api/settings", b),
};

export const Events = {
  list: (limit = 50, before, kind) =>
    api.get(`/api/events?limit=${limit}` + (before ? `&before=${before}` : "") + (kind ? `&kind=${q(kind)}` : "")),
};
