import { useEffect, useMemo, useRef, useState } from "react";
import {
  Badge,
  Inline,
  Input,
  Modal,
  ModalBody,
  ModalHeader,
  ModalTitle,
  Spinner,
  Stack,
  Text,
} from "@bzync/rui";
import { rankObjectMatches, type StudioObjectMatch } from "./resultTools";

// Global object search (Database explorer scope): a keyboard-driven finder
// over the two object namespaces Studio can enumerate completely without a
// fetch-per-object — table names (from the bootstrap read) and workflow
// names (one authorized system.workflows read, loaded when the finder first
// opens). Selecting a table opens it in the inspector; selecting a workflow
// opens the read-only Workflows explorer. Columns and indexes are not
// searched here — that would need a detail fetch for every table.
export function ObjectSearch({
  onClose,
  tables,
  workflows,
  workflowsLoading,
  onOpenTable,
  onOpenWorkflow,
}: {
  onClose: () => void;
  tables: string[];
  workflows: string[];
  workflowsLoading: boolean;
  onOpenTable: (name: string) => void;
  onOpenWorkflow: () => void;
}) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const objects = useMemo<StudioObjectMatch[]>(
    () => [
      ...tables.map((name) => ({ name, kind: "table" as const })),
      ...workflows.map((name) => ({ name, kind: "workflow" as const })),
    ],
    [tables, workflows],
  );

  const results = useMemo(() => rankObjectMatches(query, objects), [query, objects]);

  useEffect(() => {
    setActiveIndex(0);
  }, [query]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const activate = (match: StudioObjectMatch | undefined) => {
    if (!match) return;
    if (match.kind === "table") onOpenTable(match.name);
    else onOpenWorkflow();
    onClose();
  };

  return (
    <Modal open onClose={onClose} size="md" ariaLabel="Search database objects" scrollable>
      <ModalHeader>
        <ModalTitle>Search objects</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="sm">
          <Input
            ref={inputRef}
            id="studio-object-search"
            label="Search tables and workflows"
            size="sm"
            value={query}
            placeholder="Object name"
            role="combobox"
            aria-expanded
            aria-controls="studio-object-search-results"
            aria-activedescendant={results[activeIndex] ? `studio-object-option-${activeIndex}` : undefined}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActiveIndex((index) => Math.min(index + 1, Math.max(results.length - 1, 0)));
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActiveIndex((index) => Math.max(index - 1, 0));
              } else if (event.key === "Enter") {
                event.preventDefault();
                activate(results[activeIndex]);
              }
            }}
          />
          <Text size="xs" variant="muted">
            Table and workflow names only. Columns and indexes are shown in a table's own inspector.
          </Text>
          {workflowsLoading ? (
            <Inline gap="sm" align="center" role="status">
              <Spinner size="sm" />
              <Text size="xs" variant="muted">Loading workflow names…</Text>
            </Inline>
          ) : null}
          {results.length === 0 ? (
            <Text size="sm" variant="muted">No matching object.</Text>
          ) : (
            <ul id="studio-object-search-results" className="nss-object-search-results" role="listbox" aria-label="Matching objects">
              {results.map((match, index) => (
                <li key={`${match.kind}:${match.name}`} role="presentation">
                  <button
                    type="button"
                    id={`studio-object-option-${index}`}
                    role="option"
                    aria-selected={index === activeIndex}
                    className={`nss-object-search-option${index === activeIndex ? " nss-object-search-option-active" : ""}`}
                    onMouseEnter={() => setActiveIndex(index)}
                    onClick={() => activate(match)}
                  >
                    <span className="nss-object-search-name">{match.name}</span>
                    <Badge size="sm" variant="muted">{match.kind}</Badge>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Stack>
      </ModalBody>
    </Modal>
  );
}
