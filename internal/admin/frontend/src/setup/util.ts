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

// explainSetupError rewrites the raw error text `nextsql setup` (or the
// Params validator) emits into something a first-run operator can act on,
// without ever hiding the original: `detail` is always the verbatim message
// and `detailOpen` asks the UI to show it expanded when the rewrite is only
// the generic fallback. The engine/CLI stays the one source of truth — this
// never suppresses an error or changes the outcome, it only frames it.
export type SetupErrorExplanation = {
  title: string;
  action: string;
  detail: string;
  known: boolean;
  detailOpen: boolean;
};

type setupErrorRule = { match: (m: string) => boolean; title: string; action: string };

const SETUP_ERROR_RULES: setupErrorRule[] = [
  {
    match: (m) => m.includes("non-loopback listen address requires") || (m.includes("tls-cert") && m.includes("tls-key")) || m.includes("tlscert and tlskey must be given together"),
    title: "This network address needs TLS",
    action:
      "The listen address you chose isn't local-only (127.0.0.1), and NextSQL will not accept remote connections without encryption. Either change the address back to 127.0.0.1 in the network options, or provide both a TLS certificate and its private key.",
  },
  {
    match: (m) => m.includes("config file") && m.includes("already exists"),
    title: "A NextSQL configuration already exists here",
    action:
      "A configuration file is already present at this path. Choose a different config location, or remove the existing file yourself if you meant to replace it, then run setup again.",
  },
  {
    match: (m) => m.includes("recovery key file") && m.includes("already exists"),
    title: "A recovery key file already exists",
    action:
      "A file already exists at the chosen recovery key export path. Setup will not overwrite key material. Choose a fresh filename, or manage the existing key with `nextsql key verify-recovery`.",
  },
  {
    match: (m) => m.includes("recovery-key-out cannot be used with --skip-init") || m.includes("recoverykeyout cannot be used with skipinit"),
    title: "Recovery key cannot be generated when skipping database creation",
    action:
      "A recovery key seals an initialized database. Either initialize the database now, or export a recovery key later with `nextsql key add-recovery`.",
  },
  {
    match: (m) => m.includes("instance-recovery-key-out requires --recovery-key-out") || m.includes("instancerecoverykeyout requires recoverykeyout"),
    title: "Both keystores must have recovery keys configured together",
    action:
      "A NextSQL deployment uses two keystores (database and registry). Recovery keys must be configured for both keystores together or neither.",
  },
  {
    match: (m) => m.includes("already contains an initialized database") || (m.includes("data directory already") && m.includes("database")) || m.includes("database already exists"),
    title: "This data directory already has a database",
    action:
      "NextSQL is already initialized in this folder. Pick an empty data directory for a fresh install, or use `nextsql registry` to upgrade or repair the existing one.",
  },
  {
    match: (m) => m.includes("production profile requires --user") || m.includes("production profile requires an administrator") || m.includes("production profile requires --user and --password-file"),
    title: "The production profile needs an administrator account",
    action:
      "Go back to the Administrator step and set a username and password. A production install cannot be created without a bootstrap administrator.",
  },
  {
    match: (m) => m.includes("key file to be kept off the data volume") || m.includes("unlock key file is inside the data directory"),
    title: "Move the unlock key off the data directory",
    action:
      "In the production profile the encryption unlock key must live outside the data directory, so a backup or disk snapshot of the data never carries the key with it. Choose a key-file path on a different volume.",
  },
  {
    match: (m) => m.includes("production profile requires --key-file") || m.includes("production profile requires --key-file (or --instance-key-file)"),
    title: "The production profile needs an unlock key",
    action:
      "Set a key-file path in the Location step. NextSQL will generate a new key there, or import and reuse one that already exists at that path.",
  },
  {
    match: (m) => m.includes("production profile requires") && (m.includes("_ms") || m.includes("watermark") || m.includes("timeout")),
    title: "The production profile is missing a required safety setting",
    action:
      "This install is missing one of the resource-governance settings a production server needs (see the exact setting below). Re-run setup with the production profile selected so the defaults are written for you.",
  },
  {
    match: (m) => m.includes("production profile cannot skip initialization") || m.includes("skipinit"),
    title: "The production profile can't defer database creation",
    action:
      "The \"write config only\" (advanced) option isn't available with the production profile. Either switch to the developer profile, or let setup create the database now.",
  },
  {
    match: (m) => m.includes("no space left") || m.includes("not enough") && m.includes("space"),
    title: "Not enough disk space",
    action: "The target volume is full. Free up space, or choose a data directory on a volume with more room, and try again.",
  },
  {
    match: (m) => m.includes("permission denied") || m.includes("operation not permitted") || ((m.includes("mkdir") || m.includes("write config") || m.includes("rename config") || m.includes("create") ) && m.includes("permission")),
    title: "NextSQL couldn't write to that location",
    action:
      "The account running Setup doesn't have permission to create files at the path shown below. Choose a directory you can write to, or start Setup again with the necessary permissions.",
  },
  {
    match: (m) => m.includes("post-install health check failed") || m.includes("health check"),
    title: "The database was created but failed its self-check",
    action:
      "NextSQL wrote the files but could not reopen them cleanly — this usually points to a storage or filesystem problem at the data directory. Keep the technical details below if you need to report this.",
  },
  {
    match: (m) => m.includes("produced no valid json output") || m.includes("executable file not found") || (m.includes("nextsql") && m.includes("no such file")),
    title: "Setup couldn't run the NextSQL program",
    action:
      "The `nextsql` program couldn't be started. Make sure it's installed alongside NextSQL Admin, then try again.",
  },
];

export function explainSetupError(raw: string): SetupErrorExplanation {
  const detail = (raw || "").trim();
  const normalized = detail.toLowerCase();
  for (const rule of SETUP_ERROR_RULES) {
    if (rule.match(normalized)) {
      return { title: rule.title, action: rule.action, detail, known: true, detailOpen: false };
    }
  }
  return {
    title: "Setup couldn't finish",
    action: "NextSQL stopped before completing this step. The message below has the specific reason.",
    detail: detail || "No further detail was reported.",
    known: false,
    detailOpen: true,
  };
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
