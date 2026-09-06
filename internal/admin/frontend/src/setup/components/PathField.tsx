// A filesystem-path input backed by the installer's own /api/v1/browse
// endpoint. A browser file input can never hand JS a real absolute path —
// see internal/installgui/browse.go's doc comment — so this instead lists
// directories on the *server* (this same local machine) as the operator
// types or clicks through them, the way a native "Browse…" dialog would.
// The field stays fully free-typeable throughout; browsing only ever
// offers suggestions, it never forces a choice.
//
// RUI's Autocomplete filters by each option's label. Options therefore use
// the full absolute path as their label (rather than only the basename), so
// its built-in filtering, focus management, listbox semantics, and portal
// positioning remain correct while the field is edited as a full path.
import { useEffect, useRef, useState } from "react";
import { Autocomplete, type AutocompleteOption } from "@bzync/rui";
import { api, ApiError, type BrowseEntry } from "../api";
import { joinPath, splitPath } from "../util";

const DEBOUNCE_MS = 150;
const MAX_VISIBLE = 8;

// Filled folders / outlined files is the conventional split (Finder,
// Explorer, VS Code) — it reads faster at a glance than a same-weight
// outline for both, especially at the small size a dropdown row allows.
function FolderIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M1.5 3.5A1 1 0 0 1 2.5 2.5h3.19a1 1 0 0 1 .8.4l.87 1.16a1 1 0 0 0 .8.4H13.5a1 1 0 0 1 1 1v7a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1v-9Z"
        fill="currentColor"
        className="text-accent-500 dark:text-accent-400"
      />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M4 1.5h5.17a1 1 0 0 1 .71.29l2.83 2.83a1 1 0 0 1 .29.71V14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V2.5a1 1 0 0 1 1-1Z"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinejoin="round"
      />
      <path d="M9 1.6V4.5a1 1 0 0 0 1 1h2.9" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
    </svg>
  );
}

export function PathField({
  id, label, hint, value, onChange, mode, placeholder, autoComplete = "off",
}: {
  id: string;
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  mode: "directory" | "file";
  placeholder?: string;
  autoComplete?: string;
}) {
  const [entries, setEntries] = useState<BrowseEntry[]>([]);
  const [listedDir, setListedDir] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [browseError, setBrowseError] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const requestId = useRef(0);
  const firstFetch = useRef(true);

  function fetchDir(dir: string) {
    const reqId = ++requestId.current;
    setLoading(true);
    api.browse(dir || undefined)
      .then((res) => {
        if (reqId !== requestId.current) return; // a newer request already superseded this one
        setEntries(res.entries);
        setListedDir(res.dir);
        setBrowseError(null);
      })
      .catch((err) => {
        if (reqId !== requestId.current) return;
        setBrowseError(err instanceof ApiError ? err.message : String(err));
      })
      .finally(() => {
        if (reqId === requestId.current) setLoading(false);
      });
  }

  const { dir } = splitPath(value);

  useEffect(() => {
    if (dir === listedDir) return;
    if (timer.current) clearTimeout(timer.current);
    if (firstFetch.current) {
      // The very first listing for whatever directory this field starts
      // with loads immediately — no debounce, and not gated behind focus
      // or any other button. Suggestions are already there the instant
      // the operator looks at the field; nothing else "activates" it.
      firstFetch.current = false;
      fetchDir(dir);
      return;
    }
    timer.current = setTimeout(() => fetchDir(dir), DEBOUNCE_MS);
    return () => { if (timer.current) clearTimeout(timer.current); };
    // Re-run only when the *directory portion* of the typed path changes —
    // typing within one directory's filename segment shouldn't re-browse.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dir]);

  const options: AutocompleteOption<BrowseEntry>[] = entries
    .filter((entry) => mode === "file" || entry.isDir)
    .map((entry) => ({
      value: entry,
      label: entry.path,
      description: entry.isDir ? "Folder" : "File",
      icon: entry.isDir ? <FolderIcon /> : <FileIcon />,
    }));

  function select(entry: BrowseEntry) {
    if (entry.isDir) {
      // Navigate in: appending the separator re-lists this folder's own
      // children next, so drilling down and "this is the folder I want"
      // are the same gesture — the operator just stops clicking.
      onChange(joinPath(entry.path, ""));
      fetchDir(entry.path);
    } else {
      onChange(entry.path); // a file is always a final pick
    }
  }

  return (
    <Autocomplete<BrowseEntry>
      id={id}
      label={label}
      hint={hint}
      error={browseError ?? undefined}
      options={options}
      inputValue={value}
      onInputChange={onChange}
      onSelect={(option) => select(option.value)}
      placeholder={placeholder}
      autoComplete={autoComplete}
      spellCheck={false}
      loading={loading}
      emptyMessage={mode === "directory" ? "No subfolders here" : "No matches here"}
      maxVisible={MAX_VISIBLE}
      clearable={false}
      inputClassName="nsi-path-input"
    />
  );
}
