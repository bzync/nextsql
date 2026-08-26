"use client";

import { useActionState } from "react";
import { Button } from "@/components/ui/button";
import { loginAction, type ActionState } from "@/app/admin/actions";

export function LoginForm() {
  const [state, action, pending] = useActionState(loginAction, undefined as ActionState | undefined);

  return (
    <form action={action} className="space-y-4">
      <label className="block">
        <span className="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.16em] text-faint">
          Password
        </span>
        <input
          name="password"
          type="password"
          autoComplete="current-password"
          required
          className="h-11 w-full rounded-lg border border-black/10 bg-white px-3 text-sm text-slate-900 outline-none focus-visible:!shadow-none focus-visible:ring-2 focus-visible:ring-blue-500/70 dark:border-white/10 dark:bg-white/[0.04] dark:text-white"
        />
      </label>
      {state && !state.ok ? (
        <p className="text-sm text-red-600 dark:text-red-400">{state.error}</p>
      ) : null}
      <Button type="submit" className="w-full" disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </Button>
    </form>
  );
}
