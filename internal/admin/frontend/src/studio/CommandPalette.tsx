import { useEffect, useMemo, useRef, useState } from "react";
import { Input, Kbd, Modal, ModalBody, ModalHeader, ModalTitle, Stack, Text } from "@bzync/rui";
import { rankCommandMatches } from "./resultTools";

// One Studio action offered by the command palette. `run` is the existing
// handler; `disabled` hides the command (nothing to invoke). `keywords` adds
// non-visible search terms (e.g. "sql" for Run).
export type StudioCommand = {
  id: string;
  label: string;
  hint?: string;
  keywords?: string;
  disabled?: boolean;
  run: () => void;
};

// A keyboard-driven launcher over Studio's own actions, opened with
// Ctrl/Cmd+K. Same Modal + ranked-list + arrow/Enter/Esc shape as the object
// finder (ObjectSearch); it invokes an action rather than opening a table.
export function CommandPalette({ commands, onClose }: { commands: StudioCommand[]; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const results = useMemo(() => rankCommandMatches(query, commands), [query, commands]);

  useEffect(() => {
    setActiveIndex(0);
  }, [query]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const activate = (command: StudioCommand | undefined) => {
    if (!command || command.disabled) return;
    onClose();
    command.run();
  };

  return (
    <Modal open onClose={onClose} size="md" ariaLabel="Studio command palette" scrollable>
      <ModalHeader>
        <ModalTitle>Command palette</ModalTitle>
      </ModalHeader>
      <ModalBody scrollable>
        <Stack gap="sm">
          <Input
            ref={inputRef}
            id="studio-command-palette"
            label="Run a Studio command"
            size="sm"
            value={query}
            placeholder="Type a command"
            role="combobox"
            aria-expanded
            aria-controls="studio-command-palette-results"
            aria-activedescendant={results[activeIndex] ? `studio-command-option-${activeIndex}` : undefined}
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
            <Kbd keys={["Ctrl", "K"]} size="sm" /> opens this anywhere in Studio.
          </Text>
          {results.length === 0 ? (
            <Text size="sm" variant="muted">No matching command.</Text>
          ) : (
            <ul id="studio-command-palette-results" className="nss-object-search-results" role="listbox" aria-label="Studio commands">
              {results.map((command, index) => (
                <li key={command.id} role="presentation">
                  <button
                    type="button"
                    id={`studio-command-option-${index}`}
                    role="option"
                    aria-selected={index === activeIndex}
                    className={`nss-object-search-option${index === activeIndex ? " nss-object-search-option-active" : ""}`}
                    onMouseEnter={() => setActiveIndex(index)}
                    onClick={() => activate(command)}
                  >
                    <span className="nss-object-search-name">{command.label}</span>
                    {command.hint ? <Text size="xs" variant="muted">{command.hint}</Text> : null}
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
