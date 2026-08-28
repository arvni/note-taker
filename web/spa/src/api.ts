// Typed client for the Fathom admin JSON API. Cookies (session) are sent with
// every request; state-changing requests carry the CSRF token.

export interface Me { email: string; role: string; org_id: number; csrf: string }
export interface Stats { Total: number; Connected: number; Pending: number; Revoked: number; Failed: number }
export interface Employee {
  ID: number; Email: string; Name: string; Department: string;
  OnboardingStatus: string; CalendarCount: number; MeetingsSynced: number;
}
export interface Calendar { UID: string; Name: string; Type: string; Enabled: boolean; IsPersonal: boolean; }
export interface Meeting {
  source_event_id: string; title: string; starts_at: string | null; ends_at: string | null;
  meeting_provider: string; meeting_url: string; synced: boolean; cancelled: boolean;
  recording_url: string; has_transcript: boolean; has_summary: boolean; recorded_at: string | null;
}
export interface EmployeeDetail {
  employee: { id: number; email: string; name: string; department: string; onboarding_status: string };
  calendars: Calendar[]; meetings: Meeting[];
}
export interface DestEvent {
  id: string; summary: string; location: string; description: string;
  htmlLink: string; status: string; start: { dateTime?: string }; end: { dateTime?: string };
}
export interface ImportResult {
  parsed: number; created: number; updated: number; disabled: number;
  reactivated: number; conflicts: string[]; row_errors: string[];
}

let csrf = "";

async function req<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers = new Headers(opts.headers);
  if (opts.method && opts.method !== "GET") headers.set("X-CSRF-Token", csrf);
  const res = await fetch(path, { credentials: "include", ...opts, headers });
  if (res.status === 401) { window.location.href = "/login"; throw new Error("unauthorized"); }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export const api = {
  async me(): Promise<Me> { const m = await req<Me>("/api/v1/me"); csrf = m.csrf; return m; },
  stats(): Promise<Stats> { return req<Stats>("/api/v1/stats"); },
  async employees(): Promise<Employee[]> {
    const r = await req<{ employees: Employee[] }>("/api/v1/employees");
    return r.employees;
  },
  revoke(id: number): Promise<{ ok: boolean }> {
    return req(`/api/v1/employees/${id}/revoke`, { method: "POST" });
  },
  detail(id: number): Promise<EmployeeDetail> { return req<EmployeeDetail>(`/api/v1/employees/${id}`); },
  invite(id: number): Promise<{ ok: boolean }> { return req(`/api/v1/employees/${id}/invite`, { method: "POST" }); },
  destStatus(): Promise<{ connected: boolean; calendar_id: string; connected_email: string; connect_url: string }> { return req("/api/v1/destination/status"); },
  destEvents(): Promise<{ connected: boolean; calendar_id: string; events: DestEvent[] }> { return req("/api/v1/destination/events"); },
  fathomStatus(): Promise<{ api_key_configured: boolean; registered: boolean; destination_url: string }> { return req("/api/v1/fathom/status"); },
  fathomRegister(): Promise<{ webhook_id: string; destination_url: string }> { return req("/api/v1/fathom/register", { method: "POST" }); },
  createDestEvent(body: { summary: string; location: string; description: string; start: string; end: string }): Promise<{ id: string }> {
    return req("/api/v1/destination/events", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  },
  importCSV(file: File): Promise<ImportResult> {
    const fd = new FormData();
    fd.append("file", file);
    return req<ImportResult>("/api/v1/employees/import", { method: "POST", body: fd });
  },
};
