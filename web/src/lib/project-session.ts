const PROJECT_KEY = "artex_project_id";

// A tab owns its project. Requests and streams read the same value until a
// full navigation switches the tab, disposing all old page state and streams.
export function currentProjectID(): string {
  if (typeof window === "undefined") return "";
  const stored = sessionStorage.getItem(PROJECT_KEY);
  if (stored) return stored;
  const legacy = localStorage.getItem(PROJECT_KEY);
  if (legacy && /^[1-9]\d*$/.test(legacy)) {
    sessionStorage.setItem(PROJECT_KEY, legacy);
    return legacy;
  }
  return "";
}

export function setCurrentProjectID(id: number | string) {
  sessionStorage.setItem(PROJECT_KEY, String(id));
}

export function clearCurrentProjectID() {
  sessionStorage.removeItem(PROJECT_KEY);
  localStorage.removeItem(PROJECT_KEY);
}
