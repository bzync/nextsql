import { redirect } from "next/navigation";
import { Logo } from "@/components/Logo";
import { ThemeToggle } from "@/components/ThemeToggle";
import { LoginForm } from "@/components/admin/LoginForm";
import { isAdmin } from "@/lib/admin-auth";

export const metadata = {
  title: "Admin sign in",
  robots: { index: false, follow: false },
};

export default async function AdminLoginPage() {
  if (await isAdmin()) redirect("/admin");

  return (
    <div className="relative flex min-h-full items-center justify-center px-4 py-16">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-sm rounded-xl border border-black/[0.08] bg-white p-6 shadow-sm dark:border-white/[0.08] dark:bg-navy-800/80">
        <Logo />
        <h1 className="mt-5 text-xl font-semibold tracking-tight">Release admin</h1>
        <p className="mt-1.5 text-sm text-muted">
          Publish NextSQL versions, upload installers, and write the comparison notes shown on Downloads.
        </p>
        <div className="mt-6">
          <LoginForm />
        </div>
        {process.env.NODE_ENV !== "production" ? (
          <p className="mt-5 font-mono text-[11px] text-faint">Dev default password: nextsql-admin</p>
        ) : null}
      </div>
    </div>
  );
}
