// Typed client for the Fathom admin JSON API. Cookies (session) are sent with
// every request; state-changing requests carry the CSRF token.

export interface Me { email: string; role: string; org_id: number; csrf: string }
export interface Stats { Total: number; Connected: number; Pending: number; Revoked: number; Failed: number }
export interface Employee {
  ID: number; Email: string; Name: string; Department: string;
  OnboardingStatus: string; CalendarCount: number; MeetingsSynced: number;
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
  importCSV(file: File): Promise<ImportResult> {
    const fd = new FormData();
    fd.append("file", file);
    return req<ImportResult>("/api/v1/employees/import", { method: "POST", body: fd });
  },
};
