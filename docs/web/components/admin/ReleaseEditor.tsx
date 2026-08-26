"use client";

import { useActionState, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  CHANGE_AREAS,
  CHANGE_KINDS,
  type Change,
  type ChangeArea,
  type ChangeKind,
  type Release,
  changeKindLabel,
} from "@/lib/release-model";
import { Button, buttonClassName } from "@/components/ui/button";
import { deleteReleaseAction, saveReleaseAction, type ActionState } from "@/app/admin/actions";

const inputClass =
  "h-10 w-full rounded-lg border border-black/10 bg-white px-3 text-sm text-slate-900 outline-none focus-visible:!shadow-none focus-visible:ring-2 focus-visible:ring-blue-500/70 dark:border-white/10 dark:bg-white/[0.04] dark:text-white";

const areaClass =
  "h-10 rounded-lg border border-black/10 bg-white px-2 text-sm dark:border-white/10 dark:bg-white/[0.04] dark:text-white";

export function ReleaseEditor({
  release,
  mode,
}: {
  release?: Release;
  mode: "create" | "edit";
}) {
  const router = useRouter();
  const [state, action, pending] = useActionState(saveReleaseAction, undefined as ActionState | undefined);
  const [changes, setChanges] = useState<Change[]>(release?.changes ?? []);
  const [deleting, setDeleting] = useState(false);
  const encoded = useMemo(() => JSON.stringify(changes), [changes]);

  const addChange = () => {
    setChanges((current) => [
      ...current,
      { id: crypto.randomUUID(), kind: "added", area: "Engine", text: "" },
    ]);
  };

  return (
    <form action={action} className="space-y-8">
      <input type="hidden" name="changes" value={encoded} />
      <section className="grid gap-4 sm:grid-cols-2">
        <Field label="Version">
          <input
            name="version"
            required
            defaultValue={release?.version}
            readOnly={mode === "edit"}
            placeholder="0.2.0"
            className={inputClass}
          />
        </Field>
        <Field label="Title">
          <input name="title" defaultValue={release?.title} placeholder="Hybrid plans" className={inputClass} />
        </Field>
        <Field label="Released">
          <input
            name="releasedAt"
            type="datetime-local"
            defaultValue={toLocalInput(release?.releasedAt)}
            className={inputClass}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Status">
            <select name="status" defaultValue={release?.status ?? "draft"} className={inputClass}>
              <option value="draft">Draft</option>
              <option value="published">Published</option>
            </select>
          </Field>
          <Field label="Channel">
            <select name="channel" defaultValue={release?.channel ?? "stable"} className={inputClass}>
              <option value="stable">Stable</option>
              <option value="preview">Preview</option>
            </select>
          </Field>
        </div>
      </section>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" name="latest" defaultChecked={release?.latest} className="rounded border-black/20" />
        Mark as the latest download
      </label>

      <Field label="Summary">
        <textarea
          name="summary"
          rows={4}
          defaultValue={release?.summary}
          className="w-full rounded-lg border border-black/10 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus-visible:!shadow-none focus-visible:ring-2 focus-visible:ring-blue-500/70 dark:border-white/10 dark:bg-white/[0.04] dark:text-white"
        />
      </Field>

      <Field label="Highlights (one per line)">
        <textarea
          name="highlights"
          rows={4}
          defaultValue={release?.highlights.join("\n")}
          placeholder="AES-256-GCM on by default"
          className="w-full rounded-lg border border-black/10 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus-visible:!shadow-none focus-visible:ring-2 focus-visible:ring-blue-500/70 dark:border-white/10 dark:bg-white/[0.04] dark:text-white"
        />
      </Field>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold">What’s new</h2>
          <button type="button" onClick={addChange} className={buttonClassName({ variant: "outline", size: "sm" })}>
            Add change
          </button>
        </div>
        <div className="space-y-3">
          {changes.length === 0 ? (
            <p className="text-sm text-muted">Add features, fixes, and breaking changes for the comparison page.</p>
          ) : (
            changes.map((change, index) => (
              <div
                key={change.id}
                className="grid gap-2 rounded-lg border border-black/[0.08] p-3 sm:grid-cols-[7.5rem_8rem_1fr_auto] dark:border-white/[0.08]"
              >
                <select
                  value={change.kind}
                  onChange={(event) =>
                    setChanges((current) =>
                      current.map((item, i) =>
                        i === index ? { ...item, kind: event.target.value as ChangeKind } : item,
                      ),
                    )
                  }
                  className={areaClass}
                >
                  {CHANGE_KINDS.map((kind) => (
                    <option key={kind} value={kind}>
                      {changeKindLabel(kind)}
                    </option>
                  ))}
                </select>
                <select
                  value={change.area}
                  onChange={(event) =>
                    setChanges((current) =>
                      current.map((item, i) =>
                        i === index ? { ...item, area: event.target.value as ChangeArea } : item,
                      ),
                    )
                  }
                  className={areaClass}
                >
                  {CHANGE_AREAS.map((area) => (
                    <option key={area} value={area}>
                      {area}
                    </option>
                  ))}
                </select>
                <input
                  value={change.text}
                  onChange={(event) =>
                    setChanges((current) =>
                      current.map((item, i) => (i === index ? { ...item, text: event.target.value } : item)),
                    )
                  }
                  placeholder="What changed"
                  className={inputClass}
                />
                <button
                  type="button"
                  className="h-10 px-2 text-sm text-slate-500 hover:text-red-600"
                  onClick={() => setChanges((current) => current.filter((_, i) => i !== index))}
                >
                  Remove
                </button>
              </div>
            ))
          )}
        </div>
      </section>

      {state && !state.ok ? <p className="text-sm text-red-600 dark:text-red-400">{state.error}</p> : null}

      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={pending}>
          {pending ? "Saving…" : mode === "create" ? "Create release" : "Save changes"}
        </Button>
        {mode === "edit" && release ? (
          <Button
            type="button"
            variant="destructive"
            disabled={deleting}
            onClick={async () => {
              if (!confirm(`Delete ${release.version} and its binaries?`)) return;
              setDeleting(true);
              const result = await deleteReleaseAction(release.version);
              setDeleting(false);
              if (result && !result.ok) alert(result.error);
              else router.push("/admin");
            }}
          >
            {deleting ? "Deleting…" : "Delete"}
          </Button>
        ) : null}
      </div>
    </form>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">{label}</span>
      {children}
    </label>
  );
}

function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
