"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import {
  type ArtifactKind,
  type Change,
  type Platform,
  type Release,
  type ReleaseChannel,
  type ReleaseStatus,
  addArtifact,
  deleteRelease,
  isVersion,
  removeArtifact,
  setLatest,
  upsertRelease,
} from "@/lib/releases";
import { ARTIFACT_KINDS, CHANGE_AREAS, CHANGE_KINDS, PLATFORMS } from "@/lib/releases";
import {
  clearAdminCookie,
  configuredPassword,
  passwordsMatch,
  requireAdmin,
  setAdminCookie,
} from "@/lib/admin-auth";

export type ActionState = { ok: true } | { ok: false; error: string };

function revalidateDownloads(version?: string) {
  revalidatePath("/download");
  revalidatePath("/admin");
  if (version) {
    revalidatePath(`/download/${version}`);
    revalidatePath(`/admin/releases/${version}`);
  }
}

export async function loginAction(_prev: ActionState | undefined, formData: FormData): Promise<ActionState> {
  const expected = configuredPassword();
  if (!expected) {
    return { ok: false, error: "Set NEXTSQL_ADMIN_PASSWORD (8+ characters) before using the admin panel." };
  }
  const password = String(formData.get("password") ?? "");
  if (!passwordsMatch(password, expected)) {
    return { ok: false, error: "Wrong password." };
  }
  await setAdminCookie();
  redirect("/admin");
}

export async function logoutAction(): Promise<void> {
  await clearAdminCookie();
  redirect("/admin/login");
}

function parseReleaseForm(formData: FormData): Omit<Release, "artifacts"> {
  const version = String(formData.get("version") ?? "").trim();
  if (!isVersion(version)) {
    throw new Error("Version must look like 1.2.3 or 1.2.3-beta.1");
  }
  const status = String(formData.get("status") ?? "draft") as ReleaseStatus;
  const channel = String(formData.get("channel") ?? "stable") as ReleaseChannel;
  const highlights = String(formData.get("highlights") ?? "")
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  const changes = parseChanges(String(formData.get("changes") ?? "[]"));
  return {
    version,
    title: String(formData.get("title") ?? "").trim() || version,
    status: status === "published" ? "published" : "draft",
    channel: channel === "preview" ? "preview" : "stable",
    latest: formData.get("latest") === "on",
    releasedAt: toIso(String(formData.get("releasedAt") ?? "")),
    summary: String(formData.get("summary") ?? "").trim(),
    highlights,
    changes,
  };
}

function parseChanges(raw: string): Change[] {
  try {
    const parsed = JSON.parse(raw) as Change[];
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((item) => item && typeof item.text === "string" && item.text.trim())
      .map((item, index) => ({
        id: item.id || `c${index + 1}`,
        kind: (CHANGE_KINDS as readonly string[]).includes(item.kind) ? item.kind : "changed",
        area: (CHANGE_AREAS as readonly string[]).includes(item.area) ? item.area : "Engine",
        text: item.text.trim(),
      }));
  } catch {
    return [];
  }
}

export async function saveReleaseAction(_prev: ActionState | undefined, formData: FormData): Promise<ActionState> {
  try {
    await requireAdmin();
    const data = parseReleaseForm(formData);
    await upsertRelease(data);
    revalidateDownloads(data.version);
    redirect(`/admin/releases/${data.version}`);
  } catch (error) {
    if (isRedirect(error)) throw error;
    return { ok: false, error: error instanceof Error ? error.message : "Could not save release." };
  }
}

export async function deleteReleaseAction(version: string): Promise<ActionState> {
  try {
    await requireAdmin();
    await deleteRelease(version);
    revalidateDownloads(version);
    redirect("/admin");
  } catch (error) {
    if (isRedirect(error)) throw error;
    return { ok: false, error: error instanceof Error ? error.message : "Could not delete release." };
  }
}

export async function setLatestAction(version: string): Promise<ActionState> {
  try {
    await requireAdmin();
    await setLatest(version);
    revalidateDownloads(version);
    return { ok: true };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : "Could not update latest." };
  }
}

export async function uploadArtifactAction(_prev: ActionState | undefined, formData: FormData): Promise<ActionState> {
  try {
    await requireAdmin();
    const version = String(formData.get("version") ?? "");
    const kind = String(formData.get("kind") ?? "") as ArtifactKind;
    const platform = String(formData.get("platform") ?? "") as Platform;
    const file = formData.get("file");
    if (!(file instanceof File) || file.size === 0) {
      return { ok: false, error: "Choose a binary or archive to upload." };
    }
    if (!ARTIFACT_KINDS.includes(kind)) return { ok: false, error: "Pick a binary kind." };
    if (!PLATFORMS.includes(platform)) return { ok: false, error: "Pick a platform." };
    await addArtifact(version, file, kind, platform);
    revalidateDownloads(version);
    return { ok: true };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : "Upload failed." };
  }
}

export async function deleteArtifactAction(version: string, artifactId: string): Promise<ActionState> {
  try {
    await requireAdmin();
    await removeArtifact(version, artifactId);
    revalidateDownloads(version);
    return { ok: true };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : "Could not remove file." };
  }
}

function isRedirect(error: unknown): boolean {
  return typeof error === "object" && error !== null && "digest" in error && String((error as { digest?: string }).digest).startsWith("NEXT_REDIRECT");
}

function toIso(value: string): string {
  if (!value) return new Date().toISOString();
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}
