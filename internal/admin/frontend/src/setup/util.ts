// Small pure helpers shared by the wizard steps. No React, no API calls —
// kept separate so they stay trivially testable/reviewable on their own.

export function humanBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(i === 0 ? 0 : 1) + " " + units[i];
}

export type PasswordStrength = { label: string; ok: boolean; tooShort: boolean };

export function passwordStrength(pw: string): PasswordStrength {
  if (!pw) return { label: "", ok: false, tooShort: false };
  if (pw.length < 8) return { label: "Too short — minimum 8 characters", ok: false, tooShort: true };
  let classes = 0;
  if (/[a-z]/.test(pw)) classes++;
  if (/[A-Z]/.test(pw)) classes++;
  if (/[0-9]/.test(pw)) classes++;
  if (/[^A-Za-z0-9]/.test(pw)) classes++;
  if (pw.length >= 14 && classes >= 3) return { label: "Strong", ok: true, tooShort: false };
  if (pw.length >= 10 && classes >= 2) return { label: "Good", ok: true, tooShort: false };
  return { label: "Weak — consider a longer password with mixed character types", ok: true, tooShort: false };
}

// guessSeparator/splitPath/joinPath are client-side-only heuristics for the
// path-picker fields (PathField): deciding, as the operator types, which
// directory to ask /api/v1/browse to list next. The server is always the
// source of truth for what a path actually resolves to on its own
// filesystem — these never need to be exact, only good enough to pick the
// right directory to list.
export function guessSeparator(path: string): "/" | "\\" {
  return path.includes("\\") && !path.includes("/") ? "\\" : "/";
}

export function splitPath(path: string): { dir: string; base: string } {
  const sep = guessSeparator(path);
  const idx = path.lastIndexOf(sep);
  if (idx === -1) {
    // A bare "~" names the home directory itself (shell/native-picker
    // convention), not a filename to filter by — list it directly rather
    // than treating "~" as a prefix to match against real file/folder
    // names, which would never match anything and made "~" look broken.
    if (path === "~") return { dir: "~", base: "" };
    return { dir: "", base: path };
  }
  return { dir: idx === 0 ? sep : path.slice(0, idx), base: path.slice(idx + 1) };
}

export function joinPath(dir: string, name: string): string {
  const sep = guessSeparator(dir || name);
  if (dir === "" || dir === sep) return dir + name;
  return dir.endsWith(sep) ? dir + name : dir + sep + name;
}

// looksNonLoopback is a client-side *hint only* — nextsql setup --dry-run's
// ErrInsecureRemote check is the one authoritative validator (see
// docs/design-installer-gui.md M3). A wrong guess here costs nothing.
export function looksNonLoopback(addr: string): boolean {
  if (!addr) return false;
  let host = addr;
  const m = addr.match(/^\[?([^\]]*)\]?(?::\d+)?$/);
  if (m) host = m[1];
  host = host.replace(/:\d+$/, "");
  if (host === "" || host.toLowerCase() === "localhost") return false;
  if (host === "127.0.0.1" || host.indexOf("127.") === 0 || host === "::1") return false;
  return true;
}
