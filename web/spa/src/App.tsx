import { useEffect, useMemo, useState } from "react";
import { api, type Employee, type Me, type Stats, type ImportResult } from "./api";

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

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">📅 Calendar Bridge <span className="tag">Admin</span></div>
        <div className="who">{me?.email} · <span className="role">{me?.role}</span>
          <form method="POST" action="/logout" style={{ display: "inline" }}>
            <button className="link" type="submit">Sign out</button>
          </form>
        </div>
      </header>

      {err && <div className="banner bad">{err} <button className="link" onClick={() => setErr("")}>dismiss</button></div>}

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
                  <tr key={e.ID}>
                    <td><div className="emp"><strong>{e.Email}</strong>{e.Name && <span className="sub">{e.Name}</span>}</div></td>
                    <td>{e.Department || "—"}</td>
                    <td><span className={"badge " + s.cls}>{s.label}</span></td>
                    <td className="num">{e.CalendarCount}</td>
                    <td className="num">{e.MeetingsSynced}</td>
                    <td className="actions">
                      {e.OnboardingStatus === "authorized" &&
                        <button className="btn small danger" onClick={() => revoke(e)}>Revoke</button>}
                    </td>
                  </tr>
                );
              })}
              {shown.length === 0 && <tr><td colSpan={6} className="empty">No employees match.</td></tr>}
            </tbody>
          </table>
        </section>
      </main>

      {importing && <ImportPanel onClose={() => setImporting(false)} onDone={() => { setImporting(false); load(); }} />}
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
