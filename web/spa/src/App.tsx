import { useEffect, useMemo, useState } from "react";
import { api, type Employee, type Me, type Stats, type ImportResult, type EmployeeDetail, type DestEvent, type ZohoSettings, type AppSettings } from "./api";

const STATUS_LABELS: Record<string, { label: string; cls: string }> = {
  authorized: { label: "Connected", cls: "ok" },
  invited: { label: "Invited", cls: "pending" },
  opened: { label: "Opened", cls: "pending" },
  authorization_started: { label: "Authorizing", cls: "pending" },
  not_invited: { label: "Not invited", cls: "muted" },
  authorization_failed: { label: "Failed", cls: "bad" },
  revoked: { label: "Revoked", cls: "bad" },
  disabled: { label: "Disabled", cls: "muted" },
};

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [emps, setEmps] = useState<Employee[]>([]);
  const [err, setErr] = useState("");
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState("all");
  const [importing, setImporting] = useState(false);
  const [detailId, setDetailId] = useState<number | null>(null);
  const [toast, setToast] = useState("");
  const [view, setView] = useState<"employees" | "calendar" | "settings">("employees");

  async function load() {
    try {
      const [s, e] = await Promise.all([api.stats(), api.employees()]);
      setStats(s); setEmps(e);
    } catch (e: any) { setErr(e.message); }
  }
  useEffect(() => { api.me().then(setMe).then(load).catch((e) => setErr(e.message)); }, []);

  const shown = useMemo(() => {
    const needle = q.toLowerCase();
    return emps.filter((e) => {
      if (filter === "connected" && e.OnboardingStatus !== "authorized") return false;
      if (filter === "pending" && ["authorized", "revoked", "authorization_failed", "disabled"].includes(e.OnboardingStatus)) return false;
      if (filter === "issues" && !["revoked", "authorization_failed"].includes(e.OnboardingStatus)) return false;
      return !needle || e.Email.toLowerCase().includes(needle) || e.Name.toLowerCase().includes(needle);
    });
  }, [emps, q, filter]);

  async function revoke(e: Employee) {
    if (!confirm(`Revoke access for ${e.Email}?`)) return;
    try { await api.revoke(e.ID); await load(); } catch (er: any) { setErr(er.message); }
  }
  async function invite(e: Employee) {
    try { await api.invite(e.ID); setToast(`Invitation sent to ${e.Email}`); await load(); }
    catch (er: any) { setErr(er.message); }
  }

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">📅 Calendar Bridge <span className="tag">Admin</span></div>
        <nav className="nav">
          <button className={"navlink" + (view === "employees" ? " on" : "")} onClick={() => setView("employees")}>Employees</button>
          <button className={"navlink" + (view === "calendar" ? " on" : "")} onClick={() => setView("calendar")}>Fathom Calendar</button>
          <button className={"navlink" + (view === "settings" ? " on" : "")} onClick={() => setView("settings")}>Settings</button>
        </nav>
        <div className="who">{me?.email} · <span className="role">{me?.role}</span>
          <form method="POST" action="/logout" style={{ display: "inline" }}>
            <button className="link" type="submit">Sign out</button>
          </form>
        </div>
      </header>

      {err && <div className="banner bad">{err} <button className="link" onClick={() => setErr("")}>dismiss</button></div>}
      {toast && <div className="toast" onAnimationEnd={() => setToast("")}>{toast}</div>}

      {view === "settings" ? <SettingsView /> : view === "calendar" ? <CalendarView /> : (
      <main>
        <section className="stats">
          <Stat label="Total" value={stats?.Total} />
          <Stat label="Connected" value={stats?.Connected} cls="ok" />
          <Stat label="Pending" value={stats?.Pending} cls="pending" />
          <Stat label="Revoked" value={stats?.Revoked} cls="bad" />
          <Stat label="Failed" value={stats?.Failed} cls="bad" />
        </section>

        <section className="toolbar">
          <input className="search" placeholder="Search name or email…" value={q} onChange={(e) => setQ(e.target.value)} />
          <div className="filters">
            {["all", "connected", "pending", "issues"].map((f) => (
              <button key={f} className={"chip" + (filter === f ? " on" : "")} onClick={() => setFilter(f)}>{f}</button>
            ))}
          </div>
          <button className="btn primary" onClick={() => setImporting(true)}>Import CSV</button>
        </section>

        <section className="card">
          <table>
            <thead>
              <tr><th>Employee</th><th>Department</th><th>Status</th><th className="num">Calendars</th><th className="num">Synced</th><th></th></tr>
            </thead>
            <tbody>
              {shown.map((e) => {
                const s = STATUS_LABELS[e.OnboardingStatus] || { label: e.OnboardingStatus, cls: "muted" };
                return (
                  <tr key={e.ID} className="clickable" onClick={() => setDetailId(e.ID)}>
                    <td><div className="emp"><strong>{e.Email}</strong>{e.Name && <span className="sub">{e.Name}</span>}</div></td>
                    <td>{e.Department || "—"}</td>
                    <td><span className={"badge " + s.cls}>{s.label}</span></td>
                    <td className="num">{e.CalendarCount}</td>
                    <td className="num">{e.MeetingsSynced}</td>
                    <td className="actions" onClick={(ev) => ev.stopPropagation()}>
                      {e.OnboardingStatus === "authorized"
                        ? <button className="btn small danger" onClick={() => revoke(e)}>Revoke</button>
                        : <button className="btn small primary" onClick={() => invite(e)}>
                            {["invited", "opened"].includes(e.OnboardingStatus) ? "Resend" : "Invite"}
                          </button>}
                    </td>
                  </tr>
                );
              })}
              {shown.length === 0 && <tr><td colSpan={6} className="empty">No employees match.</td></tr>}
            </tbody>
          </table>
        </section>
      </main>
      )}

      {detailId !== null && <DetailDrawer id={detailId} onClose={() => setDetailId(null)} />}
      {importing && <ImportPanel onClose={() => setImporting(false)} onDone={() => { setImporting(false); load(); }} />}
    </div>
  );
}

function DC_BASES(dc: string) {
  const m: Record<string, [string, string]> = {
    com: ["https://accounts.zoho.com", "https://calendar.zoho.com/api/v1"],
    eu: ["https://accounts.zoho.eu", "https://calendar.zoho.eu/api/v1"],
    in: ["https://accounts.zoho.in", "https://calendar.zoho.in/api/v1"],
    "com.au": ["https://accounts.zoho.com.au", "https://calendar.zoho.com.au/api/v1"],
    jp: ["https://accounts.zoho.jp", "https://calendar.zoho.jp/api/v1"],
  };
  return m[dc] || m.com;
}

function SettingsView() {
  const [s, setS] = useState<ZohoSettings | null>(null);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [dc, setDc] = useState("com");
  const [scopes, setScopes] = useState("");
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.zohoSettings().then((z) => {
      setS(z); setClientId(z.client_id); setScopes(z.scopes);
      // infer DC from accounts_base
      const dcs = ["eu", "in", "com.au", "jp", "com"];
      setDc(dcs.find((d) => z.accounts_base.includes("zoho." + d)) || "com");
    }).catch((e) => setErr(e.message));
  }, []);

  async function save() {
    setErr(""); setSaved(false);
    const [accounts, calendar] = DC_BASES(dc);
    try {
      await api.saveZohoSettings({ client_id: clientId, client_secret: secret,
        accounts_base: accounts, calendar_base: calendar, scopes });
      setSecret(""); setSaved(true); setS(await api.zohoSettings());
    } catch (e: any) { setErr(e.message); }
  }

  return (
    <main>
      <section className="card connect-card" style={{ textAlign: "left", maxWidth: 640, margin: "0 auto" }}>
        <h3 style={{ textAlign: "center" }}>Zoho integration</h3>
        <p className="muted" style={{ textAlign: "center" }}>Enter your organization's Zoho OAuth app so employees can connect their calendars. Register a Server-based app in the Zoho API Console.</p>

        {s && <div className="fathom-bar" style={{ marginTop: 8 }}>
          <span>Status: {s.configured ? <b style={{ color: "var(--ok)" }}>configured</b> : <b>not configured</b>}</span>
        </div>}
        <div className="fathom-bar" style={{ marginBottom: 18 }}>
          <span>Register this redirect URI in the Zoho console:<br /><code>{s?.redirect_uri}</code></span>
        </div>

        <label className="field"><span>Client ID</span>
          <input value={clientId} onChange={(e) => setClientId(e.target.value)} placeholder="1000.XXXXXXXX" /></label>
        <label className="field"><span>Client Secret {s?.has_secret && <em className="muted">(leave blank to keep current)</em>}</span>
          <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder={s?.has_secret ? "••••••••" : "client secret"} /></label>
        <div className="field-row">
          <label className="field"><span>Data center</span>
            <select value={dc} onChange={(e) => setDc(e.target.value)} style={{ padding: "9px 12px", borderRadius: 8, border: "1px solid var(--border)", background: "var(--panel)", color: "var(--text)" }}>
              <option value="com">.com (US)</option><option value="eu">.eu (Europe)</option>
              <option value="in">.in (India)</option><option value="com.au">.com.au (Australia)</option><option value="jp">.jp (Japan)</option>
            </select></label>
          <label className="field"><span>Scopes</span>
            <input value={scopes} onChange={(e) => setScopes(e.target.value)} placeholder="ZohoCalendar.calendar.READ,..." /></label>
        </div>

        {err && <div className="banner bad" style={{ marginTop: 14 }}>{err}</div>}
        {saved && <div className="banner" style={{ marginTop: 14, background: "var(--ok-bg)", color: "var(--ok)" }}>Saved. Employees can now connect their Zoho calendars.</div>}
        <div className="modal-actions"><button className="btn primary" disabled={!clientId} onClick={save}>Save Zoho settings</button></div>
      </section>
      <AppSettingsCard />
    </main>
  );
}

function AppSettingsCard() {
  const [a, setA] = useState<AppSettings | null>(null);
  const [f, setF] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => { api.appSettings().then((x) => { setA(x); setF({
    smtp_host: x.smtp_host, smtp_port: x.smtp_port, smtp_user: x.smtp_user, email_from: x.email_from,
    company_name: x.company_name, app_name: x.app_name, support_addr: x.support_addr,
    privacy_url: x.privacy_url, terms_url: x.terms_url,
    google_oauth_client_id: x.google_oauth_client_id,
  }); }).catch((e) => setErr(e.message)); }, []);

  const on = (k: string) => (e: any) => setF({ ...f, [k]: e.target.value });
  const Field = ({ k, label, ph, type }: { k: string; label: string; ph?: string; type?: string }) =>
    <label className="field"><span>{label}</span><input type={type || "text"} value={f[k] ?? ""} onChange={on(k)} placeholder={ph} /></label>;

  async function save() {
    setErr(""); setSaved(false);
    try { await api.saveAppSettings(f); setF({ ...f, smtp_pass: "", fathom_api_key: "", google_oauth_client_secret: "" }); setSaved(true); setA(await api.appSettings()); }
    catch (e: any) { setErr(e.message); }
  }

  return (
    <section className="card connect-card" style={{ textAlign: "left", maxWidth: 640, margin: "18px auto 0" }}>
      <h3 style={{ textAlign: "center" }}>Email, branding &amp; Fathom</h3>

      <h4>Email server (SMTP)</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>Used to send onboarding invitations. Leave blank to only log emails (dev).</p>
      <div className="field-row"><Field k="smtp_host" label="SMTP host" ph="smtp.zoho.com" /><Field k="smtp_port" label="Port" ph="587" /></div>
      <div className="field-row"><Field k="smtp_user" label="Username" /><label className="field"><span>Password {a?.has_smtp_pass && <em className="muted">(set)</em>}</span><input type="password" value={f.smtp_pass ?? ""} onChange={on("smtp_pass")} placeholder={a?.has_smtp_pass ? "••••••••" : ""} /></label></div>
      <Field k="email_from" label="From address" ph="calendar-integration@company.com" />

      <h4>Branding</h4>
      <div className="field-row"><Field k="company_name" label="Company name" ph="Your Company" /><Field k="app_name" label="App name" ph="Calendar Bridge" /></div>
      <Field k="support_addr" label="Support email" ph="support@company.com" />
      <div className="field-row"><Field k="privacy_url" label="Privacy URL" /><Field k="terms_url" label="Terms URL" /></div>

      <h4>Fathom</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>API key to register the recording webhook (Fathom Calendar tab).</p>
      <label className="field"><span>Fathom API key {a?.has_fathom_key && <em className="muted">(set)</em>}</span><input type="password" value={f.fathom_api_key ?? ""} onChange={on("fathom_api_key")} placeholder={a?.has_fathom_key ? "••••••••" : ""} /></label>

      <h4>Google Calendar (destination)</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>OAuth client for the “Connect Google Calendar” button (Fathom Calendar tab). Create it in Google Cloud Console → Credentials.</p>
      <div className="field-row">
        <Field k="google_oauth_client_id" label="Client ID" ph="…apps.googleusercontent.com" />
        <label className="field"><span>Client secret {a?.has_google_secret && <em className="muted">(set)</em>}</span><input type="password" value={f.google_oauth_client_secret ?? ""} onChange={on("google_oauth_client_secret")} placeholder={a?.has_google_secret ? "••••••••" : ""} /></label>
      </div>
      {a?.google_redirect_uri && <p className="muted" style={{ margin: "2px 0 0" }}>Register this redirect URI in Google Cloud Console:<br /><code>{a.google_redirect_uri}</code></p>}

      {err && <div className="banner bad" style={{ marginTop: 14 }}>{err}</div>}
      {saved && <div className="banner" style={{ marginTop: 14, background: "var(--ok-bg)", color: "var(--ok)" }}>Settings saved.</div>}
      <div className="modal-actions"><button className="btn primary" onClick={save}>Save settings</button></div>
    </section>
  );
}

function CalendarView() {
  const [status, setStatus] = useState<{ connected: boolean; calendar_id: string; connected_email: string; connect_url: string } | null>(null);
  const [data, setData] = useState<{ events: DestEvent[] } | null>(null);
  const [err, setErr] = useState("");
  const [adding, setAdding] = useState(false);
  const [fathom, setFathom] = useState<{ api_key_configured: boolean; registered: boolean; destination_url: string } | null>(null);
  const [fmsg, setFmsg] = useState("");

  async function load() {
    try {
      const st = await api.destStatus();
      setStatus(st);
      if (st.connected) setData(await api.destEvents());
      try { setFathom(await api.fathomStatus()); } catch { /* ignore */ }
    } catch (e: any) { setErr(e.message); }
  }
  useEffect(() => { load(); }, []);

  const fmt = (t?: string) => t ? new Date(t).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "—";

  if (status && !status.connected) {
    return (
      <main>
        <section className="card connect-card">
          <div className="connect-emoji">📅</div>
          <h3>Connect the Fathom calendar</h3>
          <p className="muted">Authorize the Google Calendar that Fathom watches. Qualifying meetings are synced here, and you can add events manually.</p>
          <a className="btn primary big" href={status.connect_url}>Connect Google Calendar</a>
        </section>
      </main>
    );
  }

  return (
    <main>
      <section className="toolbar">
        <div style={{ flex: 1 }}>
          <strong>Fathom-watched calendar</strong>
          {status && <span className="muted"> · {status.calendar_id}{status.connected_email ? ` · connected as ${status.connected_email}` : ""}</span>}
        </div>
        <a className="btn" href={status?.connect_url || "#"}>Reconnect</a>
        <button className="btn primary" onClick={() => setAdding(true)}>Add event</button>
      </section>
      {err && <div className="banner bad">{err} <button className="link" onClick={() => { setErr(""); load(); }}>retry</button></div>}
      {fathom && (
        <div className={"fathom-bar " + (fathom.registered ? "ok" : "")}>
          <span>🎥 Fathom recording linkback:{" "}
            {!fathom.api_key_configured ? <b>set FATHOM_API_KEY to enable</b>
              : fathom.registered ? <><b>connected</b> — webhook active at <code>{fathom.destination_url}</code></>
              : <>not registered — <b>connect Fathom</b> to link recordings to meetings</>}
          </span>
          {fathom.api_key_configured && !fathom.registered &&
            <button className="btn small primary" onClick={async () => {
              try { const r = await api.fathomRegister(); setFmsg("Fathom webhook registered (" + r.webhook_id + ")"); setFathom(await api.fathomStatus()); }
              catch (e: any) { setErr(e.message); }
            }}>Connect Fathom</button>}
        </div>
      )}
      {fmsg && <div className="banner" style={{ background: "var(--ok-bg)", color: "var(--ok)" }}>{fmsg}</div>}
      <section className="card">
        <table>
          <thead><tr><th>Event</th><th>Start</th><th>End</th><th>Status</th></tr></thead>
          <tbody>
            {data?.events.map((e) => (
              <tr key={e.id}>
                <td><div className="emp"><strong>{e.summary || "(untitled)"}</strong>{e.location && <a className="sub mini-link" href={e.location} target="_blank" rel="noreferrer">{e.location}</a>}</div></td>
                <td>{fmt(e.start.dateTime)}</td>
                <td>{fmt(e.end.dateTime)}</td>
                <td>{e.htmlLink ? <a className="mini-link" href={e.htmlLink} target="_blank" rel="noreferrer">open ↗</a> : e.status}</td>
              </tr>
            ))}
            {data && data.events.length === 0 && <tr><td colSpan={4} className="empty">No events in the destination calendar.</td></tr>}
            {!data && !err && <tr><td colSpan={4} className="empty">Loading…</td></tr>}
          </tbody>
        </table>
      </section>
      {adding && <AddEventModal onClose={() => setAdding(false)} onDone={() => { setAdding(false); load(); }} />}
    </main>
  );
}

function AddEventModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const now = new Date();
  const iso = (d: Date) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  const [summary, setSummary] = useState("");
  const [location, setLocation] = useState("");
  const [start, setStart] = useState(iso(new Date(now.getTime() + 3600000)));
  const [end, setEnd] = useState(iso(new Date(now.getTime() + 7200000)));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit() {
    setBusy(true); setErr("");
    try {
      await api.createDestEvent({
        summary, location, description: location ? "Join: " + location : "",
        start: new Date(start).toISOString(), end: new Date(end).toISOString(),
      });
      onDone();
    } catch (e: any) { setErr(e.message); setBusy(false); }
  }

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <h3>Add event to the Fathom calendar</h3>
        <label className="field"><span>Title</span><input value={summary} onChange={(e) => setSummary(e.target.value)} placeholder="Client meeting" /></label>
        <label className="field"><span>Meeting URL (location)</span><input value={location} onChange={(e) => setLocation(e.target.value)} placeholder="https://meet.google.com/…" /></label>
        <div className="field-row">
          <label className="field"><span>Start</span><input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} /></label>
          <label className="field"><span>End</span><input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} /></label>
        </div>
        {err && <div className="banner bad">{err}</div>}
        <div className="modal-actions">
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={!summary || busy} onClick={submit}>{busy ? "Adding…" : "Add event"}</button>
        </div>
      </div>
    </div>
  );
}

function Stat({ label, value, cls }: { label: string; value?: number; cls?: string }) {
  return (
    <div className={"stat " + (cls || "")}>
      <div className="statval">{value ?? "—"}</div>
      <div className="statlabel">{label}</div>
    </div>
  );
}

function DetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const [d, setD] = useState<EmployeeDetail | null>(null);
  const [err, setErr] = useState("");
  const [sent, setSent] = useState(false);
  useEffect(() => { api.detail(id).then(setD).catch((e) => setErr(e.message)); }, [id]);

  const fmt = (t: string | null) => t ? new Date(t).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "—";

  return (
    <div className="drawer-bg" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <header className="drawer-head">
          <div>
            <h3>{d?.employee.email || "…"}</h3>
            {d?.employee.name && <div className="muted">{d.employee.name}{d.employee.department ? ` · ${d.employee.department}` : ""}</div>}
          </div>
          <div className="drawer-actions">
            {d && d.employee.onboarding_status !== "authorized" &&
              <button className="btn small primary" disabled={sent} onClick={async () => {
                try { await api.invite(d.employee.id); setSent(true); } catch (e: any) { setErr(e.message); }
              }}>{sent ? "Invitation sent ✓" : (["invited","opened"].includes(d.employee.onboarding_status) ? "Resend invitation" : "Send invitation")}</button>}
            <button className="link" onClick={onClose}>Close ✕</button>
          </div>
        </header>
        {err && <div className="banner bad">{err}</div>}

        <h4>Calendars</h4>
        {d && d.calendars.length === 0 && <p className="muted">No calendars discovered yet.</p>}
        <ul className="callist">
          {d?.calendars.map((c) => (
            <li key={c.UID}>
              <span className={"dot " + (c.Enabled ? "on" : "off")} />
              {c.Name || c.UID}
              {c.IsPersonal && <span className="mini">personal</span>}
              <span className="mini">{c.Enabled ? "monitored" : "off"}</span>
            </li>
          ))}
        </ul>

        <h4>Synced meetings <span className="muted">({d?.meetings.length ?? 0})</span></h4>
        {d && d.meetings.length === 0 && <p className="muted">No meetings synced yet.</p>}
        {d && d.meetings.length > 0 && (
          <table className="mini-table">
            <thead><tr><th>Meeting</th><th>When</th><th>Provider</th><th>Status</th><th>Recording</th></tr></thead>
            <tbody>
              {d.meetings.map((m) => (
                <tr key={m.source_event_id}>
                  <td>{m.title || "(untitled)"}<br /><a className="mini-link" href={m.meeting_url} target="_blank" rel="noreferrer">{m.meeting_url}</a></td>
                  <td>{fmt(m.starts_at)}</td>
                  <td>{m.meeting_provider.replace("_", " ")}</td>
                  <td>{m.cancelled ? <span className="badge bad">Cancelled</span> : m.synced ? <span className="badge ok">Synced</span> : <span className="badge pending">Pending</span>}</td>
                  <td>{m.recording_url
                    ? <span className="rec"><a className="mini-link" href={m.recording_url} target="_blank" rel="noreferrer">▶ Recording</a>{m.has_transcript && <span className="mini">transcript</span>}{m.has_summary && <span className="mini">summary</span>}</span>
                    : <span className="muted">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </aside>
    </div>
  );
}

function ImportPanel({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit() {
    if (!file) return;
    setBusy(true); setErr("");
    try { setResult(await api.importCSV(file)); } catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  }

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <h3>Import employees (CSV)</h3>
        <p className="muted">Header row: <code>email,name,department,status</code>. New active employees are invited; inactive are offboarded.</p>
        {!result ? (
          <>
            <input type="file" accept=".csv" onChange={(e) => setFile(e.target.files?.[0] || null)} />
            {err && <div className="banner bad">{err}</div>}
            <div className="modal-actions">
              <button className="btn" onClick={onClose}>Cancel</button>
              <button className="btn primary" disabled={!file || busy} onClick={submit}>{busy ? "Uploading…" : "Upload & sync"}</button>
            </div>
          </>
        ) : (
          <>
            <div className="result">
              <b>Parsed {result.parsed}</b> · Created {result.created} · Updated {result.updated} · Disabled {result.disabled} · Reactivated {result.reactivated}
              {result.conflicts.length > 0 && <><h4>Conflicts</h4><ul>{result.conflicts.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
              {result.row_errors.length > 0 && <><h4>Skipped rows</h4><ul>{result.row_errors.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
            </div>
            <div className="modal-actions"><button className="btn primary" onClick={onDone}>Done</button></div>
          </>
        )}
      </div>
    </div>
  );
}
