import { SearchHeader } from "@/components/SearchHeader";
import { SiteFooter } from "@/components/SiteFooter";

export default function DownloadLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-full">
      <SearchHeader />
      {children}
      <SiteFooter />
    </div>
  );
}
