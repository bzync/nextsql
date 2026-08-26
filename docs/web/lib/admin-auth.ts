import { createHmac, timingSafeEqual } from "node:crypto";
import { cookies } from "next/headers";

const COOKIE = "nextsql-admin";
const WEEK = 7 * 24 * 60 * 60;

function secret(): string {
  return process.env.NEXTSQL_ADMIN_SECRET || process.env.NEXTSQL_ADMIN_PASSWORD || "nextsql-dev-secret";
}

export function configuredPassword(): string | null {
  const password = process.env.NEXTSQL_ADMIN_PASSWORD;
  if (password && password.length >= 8) return password;
  if (process.env.NODE_ENV !== "production") return "nextsql-admin";
  return null;
}

export function signSession(): string {
  const exp = Date.now() + WEEK * 1000;
  const payload = `v1.${exp}`;
  const sig = createHmac("sha256", secret()).update(payload).digest("hex");
  return `${payload}.${sig}`;
}

export function verifySession(token: string | undefined): boolean {
  if (!token) return false;
  const parts = token.split(".");
  if (parts.length !== 3) return false;
  const [ver, expRaw, sig] = parts;
  const payload = `${ver}.${expRaw}`;
  const expected = createHmac("sha256", secret()).update(payload).digest("hex");
  const left = Buffer.from(sig);
  const right = Buffer.from(expected);
  if (left.length !== right.length || !timingSafeEqual(left, right)) return false;
  const exp = Number(expRaw);
  return Number.isFinite(exp) && Date.now() < exp;
}

export function passwordsMatch(input: string, expected: string): boolean {
  const left = Buffer.from(input);
  const right = Buffer.from(expected);
  if (left.length !== right.length) {
    timingSafeEqual(left, left);
    return false;
  }
  return timingSafeEqual(left, right);
}

export async function isAdmin(): Promise<boolean> {
  const jar = await cookies();
  return verifySession(jar.get(COOKIE)?.value);
}

export async function setAdminCookie(): Promise<void> {
  const jar = await cookies();
  jar.set(COOKIE, signSession(), {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: WEEK,
  });
}

export async function clearAdminCookie(): Promise<void> {
  const jar = await cookies();
  jar.delete(COOKIE);
}

export async function requireAdmin(): Promise<void> {
  if (!(await isAdmin())) {
    throw new Error("Unauthorized");
  }
}
