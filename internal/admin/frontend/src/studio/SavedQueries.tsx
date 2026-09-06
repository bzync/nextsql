import { useMemo, useState } from "react";
import {
  Badge,
  Button,
  Inline,
  Input,
  Popover,
  PopoverContent,
  Select,
  Stack,
  Text,
} from "@bzync/rui";
import {
  filterSavedQueries,
  savedQueryTags,
  type SavedQuery,
} from "./resultTools";

function preview(sql: string): string {
  const flat = sql.replace(/\s+/g, " ").trim();
  return flat.length > 90 ? `${flat.slice(0, 90)}…` : flat;
}

// Saved queries (SQL editor scope): named, tag-grouped SQL snippets the
// operator explicitly keeps. Persisted to localStorage per connection by the
// workspace; this component is the presentational panel plus a small
// save/rename form. A "folder" is a tag — the tag filter is the folder
// picker. Loading a saved query drops its SQL into the active editor tab
// without running it, so a destructive one still faces the confirm-before-run
// check on its own merits.
export function SavedQueries({
  open,
  onOpenChange,
  queries,
  currentSQL,
  onSave,
  onUpdateSQL,
  onRename,
  onDelete,
  onLoad,
  onExport,
  onImport,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  queries: SavedQuery[];
  currentSQL: string;
  onSave: (name: string, tags: string) => void;
  onUpdateSQL: (id: string) => void;
  onRename: (id: string, name: string) => void;
  onDelete: (id: string) => void;
  onLoad: (sql: string) => void;
  onExport: () => void;
  onImport: () => void;
}) {
  const [name, setName] = useState("");
  const [tags, setTags] = useState("");
  const [filterText, setFilterText] = useState("");
  const [filterTag, setFilterTag] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingName, setEditingName] = useState("");

  const tagOptions = useMemo(
    () => [{ value: "", label: "All tags" }, ...savedQueryTags(queries).map((t) => ({ value: t, label: t }))],
    [queries],
  );
  const shown = useMemo(
    () => filterSavedQueries(queries, { text: filterText, tag: filterTag }),
    [queries, filterText, filterTag],
  );

  const canSave = name.trim().length > 0 && currentSQL.trim().length > 0;

  return (
    <Popover
      open={open}
      onOpenChange={onOpenChange}
      ariaLabel="Saved queries"
      side="bottom"
      align="end"
      trigger={
        <Button variant="outline" size="sm">
          Saved{queries.length ? ` (${queries.length})` : ""}
        </Button>
      }
    >
      <PopoverContent className="nss-saved-panel">
        <Stack gap="sm">
          <Text size="sm" weight="medium">Saved queries</Text>
          <Text size="xs" variant="muted">Stored in this browser for this connection only — never sent anywhere.</Text>

          <form
            className="nss-saved-form"
            onSubmit={(event) => {
              event.preventDefault();
              if (!canSave) return;
              onSave(name.trim(), tags);
              setName("");
              setTags("");
            }}
          >
            <Stack gap="xs">
              <Input
                id="studio-saved-name"
                label="Save current query as"
                size="sm"
                value={name}
                placeholder="Name"
                onChange={(event) => setName(event.currentTarget.value)}
              />
              <Input
                id="studio-saved-tags"
                label="Tags (comma-separated, optional)"
                size="sm"
                value={tags}
                placeholder="reporting, ops"
                onChange={(event) => setTags(event.currentTarget.value)}
              />
              <Button type="submit" variant="primary" size="sm" disabled={!canSave}>Save current query</Button>
            </Stack>
          </form>

          {queries.length ? (
            <Inline gap="xs" align="end" wrap>
              <Input
                id="studio-saved-filter"
                label="Filter"
                size="sm"
                value={filterText}
                placeholder="Name or SQL"
                onChange={(event) => setFilterText(event.currentTarget.value)}
              />
              <Select
                id="studio-saved-tag"
                label="Tag"
                options={tagOptions}
                value={filterTag}
                onChange={setFilterTag}
              />
            </Inline>
          ) : null}

          {queries.length === 0 ? (
            <Text size="sm" variant="muted">No saved queries yet.</Text>
          ) : shown.length === 0 ? (
            <Text size="sm" variant="muted">No saved query matches this filter.</Text>
          ) : (
            <ul className="nss-saved-list" aria-label="Saved queries">
              {shown.map((entry) => (
                <li key={entry.id} className="nss-saved-item">
                  {editingId === entry.id ? (
                    <form
                      className="nss-saved-rename"
                      onSubmit={(event) => {
                        event.preventDefault();
                        onRename(entry.id, editingName.trim() || entry.name);
                        setEditingId(null);
                      }}
                    >
                      <Input
                        id={`studio-saved-rename-${entry.id}`}
                        label={`Rename ${entry.name}`}
                        size="sm"
                        value={editingName}
                        onChange={(event) => setEditingName(event.currentTarget.value)}
                      />
                      <Inline gap="xs">
                        <Button type="submit" variant="primary" size="sm">Save name</Button>
                        <Button type="button" variant="ghost" size="sm" onClick={() => setEditingId(null)}>Cancel</Button>
                      </Inline>
                    </form>
                  ) : (
                    <Stack gap="xs">
                      <button
                        type="button"
                        className="nss-saved-load"
                        onClick={() => onLoad(entry.sql)}
                        title={entry.sql}
                      >
                        <span className="nss-saved-name">{entry.name}</span>
                        <span className="nss-saved-sql">{preview(entry.sql)}</span>
                      </button>
                      <Inline gap="xs" align="center" wrap>
                        {entry.tags.map((tag) => (
                          <Badge key={tag} size="sm" variant="muted">{tag}</Badge>
                        ))}
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => onUpdateSQL(entry.id)}
                          disabled={currentSQL.trim().length === 0}
                          title="Overwrite this saved query with the current editor buffer"
                        >
                          Update
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setEditingId(entry.id);
                            setEditingName(entry.name);
                          }}
                        >
                          Rename
                        </Button>
                        <Button type="button" variant="ghost" size="sm" onClick={() => onDelete(entry.id)}>Delete</Button>
                      </Inline>
                    </Stack>
                  )}
                </li>
              ))}
            </ul>
          )}

          <Inline gap="xs" align="center" wrap className="nss-saved-transfer">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onExport}
              disabled={queries.length === 0}
              title="Download this connection's saved queries as a JSON file"
            >
              Export…
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={onImport}>
              Import…
            </Button>
          </Inline>
          <Text size="xs" variant="muted">
            Import merges by entry, keeping the newer copy of anything that already exists.
          </Text>
        </Stack>
      </PopoverContent>
    </Popover>
  );
}
