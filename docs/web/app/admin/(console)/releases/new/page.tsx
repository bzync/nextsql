import { ReleaseEditor } from "@/components/admin/ReleaseEditor";

export const metadata = { title: "New release" };

export default function NewReleasePage() {
  return (
    <div className="max-w-3xl">
      <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-blue-500 dark:text-blue-400">
        New release
      </p>
      <h1 className="mt-2 text-2xl font-bold tracking-[-0.03em]">Create a version</h1>
      <p className="mt-2 text-sm text-muted">
        Save the notes first, then upload binaries on the next screen.
      </p>
      <div className="mt-8">
        <ReleaseEditor mode="create" />
      </div>
    </div>
  );
}
