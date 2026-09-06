import { useEffect, useMemo, useState } from "react";
import { api, type Employee, type Me, type Stats, type ImportResult, type EmployeeDetail, type DestEvent, type ZohoSettings, type AppSettings, type RecMeeting } from "./api";

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
  const [invitingId, setInvitingId] = useState<number | null>(null);
  const [view, setView] = useState<"employees" | "calendar" | "recordings" | "settings">("employees");

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
    setErr(""); setInvitingId(e.ID);
    try { await api.invite(e.ID); setToast(`✓ Invitation sent to ${e.Email}`); await load(); }
    catch (er: any) { setErr(er.message); }
    finally { setInvitingId(null); }
  }

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">📅 Calendar Bridge <span className="tag">Admin</span></div>
        <nav className="nav">
          <button className={"navlink" + (view === "employees" ? " on" : "")} onClick={() => setView("employees")}>Employees</button>
          <button className={"navlink" + (view === "calendar" ? " on" : "")} onClick={() => setView("calendar")}>Fathom Calendar</button>
          <button className={"navlink" + (view === "recordings" ? " on" : "")} onClick={() => setView("recordings")}>Recordings</button>
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

      {view === "settings" ? <SettingsView /> : view === "recordings" ? <RecordingsView /> : view === "calendar" ? <CalendarView /> : (
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
                        : <button className="btn small primary" disabled={invitingId === e.ID} onClick={() => invite(e)}>
                            {invitingId === e.ID
                              ? <span className="spin">⏳ Sending…</span>
                              : (["invited", "opened"].includes(e.OnboardingStatus) ? "Resend" : "Invite")}
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
    google_oauth_client_id: x.google_oauth_client_id, sync_interval: x.sync_interval,
  }); }).catch((e) => setErr(e.message)); }, []);

  const on = (k: string) => (e: any) => setF({ ...f, [k]: e.target.value });
  const Field = ({ k, label, ph, type }: { k: string; label: string; ph?: string; type?: string }) =>
    <label className="field"><span>{label}</span><input type={type || "text"} value={f[k] ?? ""} onChange={on(k)} placeholder={ph} /></label>;

  async function save() {
    setErr(""); setSaved(false);
    try { await api.saveAppSettings(f); setF({ ...f, smtp_pass: "", fathom_api_key: "", fireflies_api_key: "", google_oauth_client_secret: "" }); setSaved(true); setA(await api.appSettings()); }
    catch (e: any) { setErr(e.message); }
  }

  return (
    <section className="card connect-card" style={{ textAlign: "left", maxWidth: 640, margin: "18px auto 0" }}>
      <h3 style={{ textAlign: "center" }}>Email, branding &amp; Fathom</h3>

      <h4>Email server (SMTP)</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>Used to send onboarding invitations. Leave blank to only log emails (dev).</p>
      <div className="field-row">{Field({ k: "smtp_host", label: "SMTP host", ph: "smtp.zoho.com" })}{Field({ k: "smtp_port", label: "Port", ph: "587" })}</div>
      <div className="field-row">{Field({ k: "smtp_user", label: "Username" })}<label className="field"><span>Password {a?.has_smtp_pass && <em className="muted">(set)</em>}</span><input type="password" value={f.smtp_pass ?? ""} onChange={on("smtp_pass")} placeholder={a?.has_smtp_pass ? "••••••••" : ""} /></label></div>
      {Field({ k: "email_from", label: "From address", ph: "calendar-integration@company.com" })}

      <h4>Branding</h4>
      <div className="field-row">{Field({ k: "company_name", label: "Company name", ph: "Your Company" })}{Field({ k: "app_name", label: "App name", ph: "Calendar Bridge" })}</div>
      {Field({ k: "support_addr", label: "Support email", ph: "support@company.com" })}
      <div className="field-row">{Field({ k: "privacy_url", label: "Privacy URL" })}{Field({ k: "terms_url", label: "Terms URL" })}</div>

      <h4>Fathom</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>API key to register the recording webhook (Fathom Calendar tab).</p>
      <label className="field"><span>Fathom API key {a?.has_fathom_key && <em className="muted">(set)</em>}</span><input type="password" value={f.fathom_api_key ?? ""} onChange={on("fathom_api_key")} placeholder={a?.has_fathom_key ? "••••••••" : ""} /></label>

      <h4>Fireflies.ai</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>API key (Fireflies → Settings → Developer). Webhook endpoint to set in Fireflies: <code>{a?.google_redirect_uri ? a.google_redirect_uri.replace("/oauth/google/callback","/webhooks/fireflies") : "https://<your-domain>/webhooks/fireflies"}</code></p>
      <label className="field"><span>Fireflies API key {a?.has_fireflies_key && <em className="muted">(set)</em>}</span><input type="password" value={f.fireflies_api_key ?? ""} onChange={on("fireflies_api_key")} placeholder={a?.has_fireflies_key ? "••••••••" : ""} /></label>

      <h4>Sync</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>How often calendars are pulled from Zoho and pushed to the Fathom calendar (e.g. 5m, 15m, 1h; min 1m). Blank = default.</p>
      {Field({ k: "sync_interval", label: "Sync interval", ph: "5m" })}

      <h4>Google Calendar (destination)</h4>
      <p className="muted" style={{ margin: "0 0 8px" }}>OAuth client for the “Connect Google Calendar” button (Fathom Calendar tab). Create it in Google Cloud Console → Credentials.</p>
      <div className="field-row">
        {Field({ k: "google_oauth_client_id", label: "Client ID", ph: "…apps.googleusercontent.com" })}
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
      <CalendarGrid events={data?.events ?? []} loading={!data && !err} />
      {adding && <AddEventModal onClose={() => setAdding(false)} onDone={() => { setAdding(false); load(); }} />}
    </main>
  );
}

// ---- Calendar grid (Google Calendar-style month / week view) ----

type CalMode = "month" | "week" | "list";
const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

const startOfDay = (d: Date) => { const x = new Date(d); x.setHours(0, 0, 0, 0); return x; };
const addDays = (d: Date, n: number) => { const x = new Date(d); x.setDate(x.getDate() + n); return x; };
const startOfWeek = (d: Date) => addDays(startOfDay(d), -startOfDay(d).getDay()); // week starts Sunday
const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
const hhmm = (d: Date) => d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

interface CalEv { id: string; title: string; start: Date; end: Date; from: string[]; link: string; location: string; }

function toCalEvents(events: DestEvent[]): CalEv[] {
  return events
    .filter((e) => e.start?.dateTime)
    .map((e) => {
      const start = new Date(e.start.dateTime!);
      const end = e.end?.dateTime ? new Date(e.end.dateTime) : new Date(start.getTime() + 30 * 60000);
      return { id: e.id, title: e.summary || "(untitled)", start, end, from: e.source_emails ?? [], link: e.htmlLink, location: e.location };
    })
    .sort((a, b) => a.start.getTime() - b.start.getTime());
}

function CalendarGrid({ events, loading }: { events: DestEvent[]; loading: boolean }) {
  const [mode, setMode] = useState<CalMode>("month");
  const [cursor, setCursor] = useState<Date>(startOfDay(new Date()));
  const [sel, setSel] = useState<CalEv | null>(null);
  const evs = useMemo(() => toCalEvents(events), [events]);

  const step = (dir: number) => {
    if (mode === "month") { const x = new Date(cursor); x.setMonth(x.getMonth() + dir); setCursor(startOfDay(x)); }
    else if (mode === "week") setCursor(addDays(cursor, dir * 7));
    else setCursor(addDays(cursor, dir));
  };

  const title = mode === "week"
    ? (() => { const s = startOfWeek(cursor); const e = addDays(s, 6); return `${MONTHS[s.getMonth()].slice(0, 3)} ${s.getDate()} – ${MONTHS[e.getMonth()].slice(0, 3)} ${e.getDate()}, ${e.getFullYear()}`; })()
    : `${MONTHS[cursor.getMonth()]} ${cursor.getFullYear()}`;

  return (
    <section className="card cal-wrap">
      <div className="cal-head">
        <div className="cal-nav">
          <button className="btn small" onClick={() => setCursor(startOfDay(new Date()))}>Today</button>
          <button className="btn small icon" onClick={() => step(-1)} aria-label="Previous">‹</button>
          <button className="btn small icon" onClick={() => step(1)} aria-label="Next">›</button>
          <strong className="cal-title">{title}</strong>
        </div>
        <div className="seg">
          {(["month", "week", "list"] as CalMode[]).map((m) => (
            <button key={m} className={"seg-btn" + (mode === m ? " on" : "")} onClick={() => setMode(m)}>{m[0].toUpperCase() + m.slice(1)}</button>
          ))}
        </div>
      </div>
      {loading ? <div className="empty" style={{ padding: 40 }}>Loading…</div>
        : mode === "month" ? <MonthView cursor={cursor} evs={evs} onSelect={setSel} />
        : mode === "week" ? <WeekView cursor={cursor} evs={evs} onSelect={setSel} />
        : <ListView evs={evs} onSelect={setSel} />}
      {sel && <EventPopup ev={sel} onClose={() => setSel(null)} />}
    </section>
  );
}

// EventPopup is a Google-Calendar-style detail card for one event, with a button
// that opens the event in Google Calendar (rather than the item being a link).
function EventPopup({ ev, onClose }: { ev: CalEv; onClose: () => void }) {
  const dayLabel = ev.start.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric", year: "numeric" });
  return (
    <div className="popover-overlay" onClick={onClose}>
      <div className="popover" onClick={(e) => e.stopPropagation()}>
        <div className="popover-head">
          <span className="popover-swatch" />
          <h3>{ev.title}</h3>
          <button className="popover-x" onClick={onClose} aria-label="Close">✕</button>
        </div>
        <div className="popover-row"><span className="popover-ico">🕐</span><div>{dayLabel}<br /><span className="muted">{hhmm(ev.start)} – {hhmm(ev.end)}</span></div></div>
        {ev.location && <div className="popover-row"><span className="popover-ico">📍</span><a href={ev.location} target="_blank" rel="noreferrer" className="mini-link">{ev.location}</a></div>}
        {ev.from.length > 0 && <div className="popover-row"><span className="popover-ico">👤</span><div className="src">{ev.from.map((m) => <span key={m} className="chip">{m}</span>)}</div></div>}
        <div className="popover-actions">
          {ev.link
            ? <a className="btn primary" href={ev.link} target="_blank" rel="noreferrer">Open in Google Calendar ↗</a>
            : <span className="muted">No Google Calendar link available</span>}
        </div>
      </div>
    </div>
  );
}

function MonthView({ cursor, evs, onSelect }: { cursor: Date; evs: CalEv[]; onSelect: (e: CalEv) => void }) {
  const first = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
  const gridStart = startOfWeek(first);
  const weeks: Date[][] = [];
  let day = gridStart;
  // Enough weeks to cover the month (5 or 6).
  const lastOfMonth = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 0);
  const gridEnd = addDays(startOfWeek(lastOfMonth), 6);
  while (day <= gridEnd) {
    const row: Date[] = [];
    for (let i = 0; i < 7; i++) { row.push(day); day = addDays(day, 1); }
    weeks.push(row);
  }
  const today = new Date();
  const byDay = (d: Date) => evs.filter((e) => sameDay(e.start, d));

  return (
    <div className="cal-month">
      <div className="cal-dow">{WEEKDAYS.map((w) => <div key={w} className="cal-dow-cell">{w}</div>)}</div>
      {weeks.map((week, wi) => (
        <div className="cal-week-row" key={wi}>
          {week.map((d) => {
            const dayEvs = byDay(d);
            const shown = dayEvs.slice(0, 3);
            const off = d.getMonth() !== cursor.getMonth();
            return (
              <div className={"cal-day" + (off ? " off" : "") + (sameDay(d, today) ? " today" : "")} key={d.toISOString()}>
                <div className="cal-day-num">{sameDay(d, today) ? <span className="today-dot">{d.getDate()}</span> : d.getDate()}</div>
                <div className="cal-chips">
                  {shown.map((e) => (
                    <button key={e.id} className="cal-chip" onClick={() => onSelect(e)} title={`${e.title}\n${hhmm(e.start)}–${hhmm(e.end)}${e.from.length ? "\n" + e.from.join(", ") : ""}`}>
                      <span className="cal-chip-t">{hhmm(e.start)}</span> {e.title}
                    </button>
                  ))}
                  {dayEvs.length > shown.length && <div className="cal-more">+{dayEvs.length - shown.length} more</div>}
                </div>
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}

interface Placed { e: CalEv; top: number; height: number; left: number; width: number; }
const HOUR_PX = 44;

function layoutDay(dayEvs: CalEv[], startHour: number): Placed[] {
  // Greedy interval-graph column packing within overlapping clusters.
  const sorted = [...dayEvs].sort((a, b) => a.start.getTime() - b.start.getTime() || a.end.getTime() - b.end.getTime());
  const placed: Placed[] = [];
  let cluster: CalEv[] = [];
  let clusterEnd = 0;
  const flush = () => {
    if (!cluster.length) return;
    const cols: CalEv[][] = [];
    for (const e of cluster) {
      let ci = cols.findIndex((col) => col[col.length - 1].end.getTime() <= e.start.getTime());
      if (ci === -1) { cols.push([e]); ci = cols.length - 1; } else cols[ci].push(e);
    }
    const n = cols.length;
    cols.forEach((col, ci) => col.forEach((e) => {
      const sMin = e.start.getHours() * 60 + e.start.getMinutes() - startHour * 60;
      const eMin = e.end.getHours() * 60 + e.end.getMinutes() - startHour * 60;
      placed.push({ e, top: (sMin / 60) * HOUR_PX, height: Math.max(18, ((eMin - sMin) / 60) * HOUR_PX), left: (ci / n) * 100, width: (1 / n) * 100 });
    }));
    cluster = [];
  };
  for (const e of sorted) {
    if (cluster.length && e.start.getTime() >= clusterEnd) flush();
    cluster.push(e);
    clusterEnd = Math.max(clusterEnd, e.end.getTime());
  }
  flush();
  return placed;
}

function WeekView({ cursor, evs, onSelect }: { cursor: Date; evs: CalEv[]; onSelect: (e: CalEv) => void }) {
  const weekStart = startOfWeek(cursor);
  const days = Array.from({ length: 7 }, (_, i) => addDays(weekStart, i));
  const weekEvs = evs.filter((e) => e.start >= weekStart && e.start < addDays(weekStart, 7));
  // Hour range from events, clamped; sensible default when empty.
  let minH = 8, maxH = 18;
  if (weekEvs.length) {
    minH = Math.min(...weekEvs.map((e) => e.start.getHours()));
    maxH = Math.max(...weekEvs.map((e) => e.end.getHours() + (e.end.getMinutes() > 0 ? 1 : 0)));
  }
  minH = Math.max(0, Math.min(minH, 8));
  maxH = Math.min(24, Math.max(maxH, 18));
  const hours = Array.from({ length: maxH - minH }, (_, i) => minH + i);
  const today = new Date();

  return (
    <div className="cal-weekwrap">
      <div className="cal-week-head">
        <div className="cal-gutter" />
        {days.map((d) => (
          <div key={d.toISOString()} className={"cal-wh" + (sameDay(d, today) ? " today" : "")}>
            <div className="cal-wh-dow">{WEEKDAYS[d.getDay()]}</div>
            <div className="cal-wh-num">{sameDay(d, today) ? <span className="today-dot">{d.getDate()}</span> : d.getDate()}</div>
          </div>
        ))}
      </div>
      <div className="cal-week-body">
        <div className="cal-gutter">
          {hours.map((h) => <div key={h} className="cal-hour-label" style={{ height: HOUR_PX }}>{h === 0 ? "12 AM" : h < 12 ? h + " AM" : h === 12 ? "12 PM" : (h - 12) + " PM"}</div>)}
        </div>
        {days.map((d) => {
          const dayEvs = weekEvs.filter((e) => sameDay(e.start, d));
          const placed = layoutDay(dayEvs, minH);
          return (
            <div key={d.toISOString()} className="cal-daycol" style={{ height: hours.length * HOUR_PX }}>
              {hours.map((h) => <div key={h} className="cal-slot" style={{ height: HOUR_PX }} />)}
              {placed.map((p) => (
                <button key={p.e.id} className="cal-ev" onClick={() => onSelect(p.e)}
                  style={{ top: p.top, height: p.height, left: `calc(${p.left}% + 2px)`, width: `calc(${p.width}% - 4px)` }}
                  title={`${p.e.title}\n${hhmm(p.e.start)}–${hhmm(p.e.end)}${p.e.from.length ? "\n" + p.e.from.join(", ") : ""}`}>
                  <div className="cal-ev-t">{p.e.title}</div>
                  <div className="cal-ev-time">{hhmm(p.e.start)}</div>
                  {p.e.from.length > 0 && <div className="cal-ev-from">{p.e.from.join(", ")}</div>}
                </button>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ListView({ evs, onSelect }: { evs: CalEv[]; onSelect: (e: CalEv) => void }) {
  const upcoming = evs.filter((e) => e.end >= new Date()).slice(0, 200);
  const fmt = (d: Date) => d.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
  return (
    <table>
      <thead><tr><th>Event</th><th>From (user)</th><th>Start</th><th>End</th><th></th></tr></thead>
      <tbody>
        {upcoming.map((e) => (
          <tr key={e.id} className="clickable" onClick={() => onSelect(e)}>
            <td><div className="emp"><strong>{e.title}</strong>{e.location && <span className="sub">{e.location}</span>}</div></td>
            <td>{e.from.length > 0 ? <span className="src">{e.from.map((m) => <span key={m} className="chip">{m}</span>)}</span> : <span className="muted">—</span>}</td>
            <td>{fmt(e.start)}</td>
            <td>{fmt(e.end)}</td>
            <td><span className="mini-link">details ›</span></td>
          </tr>
        ))}
        {upcoming.length === 0 && <tr><td colSpan={5} className="empty">No upcoming events in the destination calendar.</td></tr>}
      </tbody>
    </table>
  );
}


// SendCell emails a recording's transcript + summary: to manually entered
// addresses, and/or (re)send to the employees matched from their calendars.
function SendCell({ dir }: { dir: string }) {
  const [emails, setEmails] = useState("");
  const [busy, setBusy] = useState<"" | "manual" | "attendees">("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const list = () => emails.split(/[,;\s]+/).map((s) => s.trim()).filter(Boolean);

  async function doSend(body: { emails?: string[]; attendees?: boolean }, which: "manual" | "attendees") {
    setBusy(which); setErr(""); setMsg("");
    try {
      const r = await api.sendRecording(dir, body);
      const parts: string[] = [];
      if (r.sent) parts.push(`✓ sent to ${r.sent}`);
      if (r.failed?.length) parts.push(`${r.failed.length} failed`);
      setMsg(parts.join(" · ") || "no recipients");
      if (which === "manual" && r.sent) setEmails("");
    } catch (e: any) { setErr(e.message); }
    finally { setBusy(""); }
  }

  return (
    <div className="sendcell">
      <div className="sendrow">
        <input className="sendinput" placeholder="email, email…" value={emails}
          onChange={(e) => setEmails(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && list().length && !busy) doSend({ emails: list() }, "manual"); }} />
        <button className="btn small" disabled={!list().length || !!busy} onClick={() => doSend({ emails: list() }, "manual")}>
          {busy === "manual" ? "Sending…" : "Send"}
        </button>
      </div>
      <button className="btn small linkish" disabled={!!busy} onClick={() => doSend({ attendees: true }, "attendees")}>
        {busy === "attendees" ? "Sending…" : "↻ Send to attendees"}
      </button>
      {msg && <div className="sendmsg ok">{msg}</div>}
      {err && <div className="sendmsg bad">{err}</div>}
    </div>
  );
}

function RecordingsView() {
  const [meetings, setMeetings] = useState<RecMeeting[] | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => { api.recordings().then((r) => setMeetings(r.meetings)).catch((e) => setErr(e.message)); }, []);
  const fmt = (t: string) => t ? new Date(t).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "\u2014";
  const kb = (n: number) => n < 1024 ? n + " B" : (n / 1024).toFixed(0) + " KB";
  const fileURL = (dir: string, name: string, dl?: boolean) =>
    `/api/v1/recordings/${encodeURIComponent(dir)}/files/${encodeURIComponent(name)}` + (dl ? "?dl=1" : "");

  return (
    <main>
      <section className="toolbar"><div><strong>Meeting files</strong> <span className="muted">transcripts &amp; summaries downloaded from Fireflies</span></div></section>
      {err && <div className="banner bad">{err}</div>}
      <section className="card">
        <table>
          <thead><tr><th>Meeting</th><th>When</th><th>Length</th><th>Files</th><th>Send to</th></tr></thead>
          <tbody>
            {meetings?.map((m) => (
              <tr key={m.dir}>
                <td><div className="emp"><strong>{m.title}</strong>{m.transcript_url && <a className="sub mini-link" href={m.transcript_url} target="_blank" rel="noreferrer">open in Fireflies ↗</a>}</div></td>
                <td>{fmt(m.date)}</td>
                <td>{m.duration ? Math.round(m.duration) + " min" : "\u2014"}</td>
                <td><div className="src">{m.files.map((f) => (
                  <span key={f.name} className="chip">
                    <a className="mini-link" href={fileURL(m.dir, f.name)} target="_blank" rel="noreferrer">{f.name}</a>
                    {" "}<a className="mini-link" href={fileURL(m.dir, f.name, true)} title="download">⬇</a>
                    <span className="muted" style={{ marginLeft: 4 }}>{kb(f.size)}</span>
                  </span>
                ))}</div></td>
                <td><SendCell dir={m.dir} /></td>
              </tr>
            ))}
            {meetings && meetings.length === 0 && <tr><td colSpan={5} className="empty">No meeting files yet. They appear here after Fireflies sends a completed transcription webhook.</td></tr>}
            {!meetings && !err && <tr><td colSpan={5} className="empty">Loading…</td></tr>}
          </tbody>
        </table>
      </section>
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
  const [sending, setSending] = useState(false);
  const [editing, setEditing] = useState(false);
  const [en, setEn] = useState("");
  const [ee, setEe] = useState("");
  const [saveMsg, setSaveMsg] = useState("");
  useEffect(() => { api.detail(id).then((x) => { setD(x); setEn(x.employee.name); setEe(x.employee.email); }).catch((e) => setErr(e.message)); }, [id]);

  async function saveProfile() {
    setErr(""); setSaveMsg("");
    try {
      await api.updateEmployee(id, { name: en, email: ee });
      const x = await api.detail(id); setD(x); setEn(x.employee.name); setEe(x.employee.email);
      setEditing(false); setSaveMsg("Saved.");
    } catch (e: any) { setErr(e.message); }
  }

  const fmt = (t: string | null) => t ? new Date(t).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "—";

  return (
    <div className="drawer-bg" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <header className="drawer-head">
          <div>
            {editing ? (
              <div className="edit-profile">
                <label className="field"><span>Name</span><input value={en} onChange={(e) => setEn(e.target.value)} placeholder="Full name" /></label>
                <label className="field"><span>Email</span><input value={ee} onChange={(e) => setEe(e.target.value)} placeholder="name@company.com" /></label>
                <div className="drawer-actions">
                  <button className="btn small primary" onClick={saveProfile}>Save</button>
                  <button className="link" onClick={() => { setEditing(false); if (d) { setEn(d.employee.name); setEe(d.employee.email); } }}>Cancel</button>
                </div>
              </div>
            ) : (
              <>
                <h3>{d?.employee.email || "…"}</h3>
                {d?.employee.name && <div className="muted">{d.employee.name}{d.employee.department ? ` · ${d.employee.department}` : ""}</div>}
                {d && <div className="muted" style={{ marginTop: 2 }}>Last invited: {d.employee.invited_at ? fmt(d.employee.invited_at) : "never"}</div>}
              </>
            )}
          </div>
          <div className="drawer-actions">
            {d && !editing && <button className="btn small" onClick={() => setEditing(true)}>Edit</button>}
            {d && d.employee.onboarding_status !== "authorized" &&
              <button className="btn small primary" disabled={sent || sending} onClick={async () => {
                setErr(""); setSending(true);
                try { await api.invite(d.employee.id); setSent(true); setD(await api.detail(d.employee.id)); } catch (e: any) { setErr(e.message); }
                finally { setSending(false); }
              }}>{sending ? "⏳ Sending…" : sent ? "Invitation sent ✓" : (["invited","opened"].includes(d.employee.onboarding_status) ? "Resend invitation" : "Send invitation")}</button>}
            <button className="link" onClick={onClose}>Close ✕</button>
          </div>
        </header>
        {err && <div className="banner bad">{err}</div>}
        {saveMsg && <div className="banner" style={{ background: "var(--ok-bg)", color: "var(--ok)" }}>{saveMsg}</div>}

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
                  <td>{m.title || "(untitled)"}<br /><a className="mini-link" href={m.meeting_url} target="_blank" rel="noreferrer">{m.meeting_url}</a>
                    {m.shared_with && m.shared_with.length > 0 && <div className="sub muted">also attended by: {m.shared_with.join(", ")}</div>}</td>
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
