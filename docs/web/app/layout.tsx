import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "@bzync/rui/styles.css";
import "./globals.css";
import { CommandProvider } from "@bzync/rui";
import { site } from "@/lib/site";
import { SiteThemeProvider } from "@/components/SiteThemeProvider";
import { ThemeScript } from "@/components/ThemeScript";
import { DocsCommandPalette } from "@/components/DocsCommandPalette";
import { searchIndex } from "@/lib/content";

const geist = Geist({
  variable: "--font-geist",
  subsets: ["latin"],
  display: "swap",
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
  display: "swap",
});

export const viewport: Viewport = {
  themeColor: "#040912",
  viewportFit: "cover",
};

export const metadata: Metadata = {
  metadataBase: new URL("https://nextsql.bzync.com"),
  applicationName: site.name,
  title: {
    default: `${site.name} — ${site.tagline}`,
    template: `%s · ${site.name}`,
  },
  description: site.description,
  appleWebApp: {
    capable: true,
    title: site.name,
    statusBarStyle: "black-translucent",
  },
  openGraph: {
    title: `${site.name} — ${site.tagline}`,
    description: site.description,
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: `${site.name} — ${site.tagline}`,
    description: site.description,
  },
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  const docsSearchEntries = searchIndex();

  return (
    <html
      lang="en"
      suppressHydrationWarning
      data-scroll-behavior="smooth"
      className={`${geist.variable} ${geistMono.variable} h-full antialiased`}
    >
      <head>
        <ThemeScript />
      </head>
      <body
        className="min-h-full bg-bg font-sans text-foreground"
        suppressHydrationWarning
      >
        <a href="#content" className="skip-link">
          Skip to content
        </a>
        <SiteThemeProvider>
          <CommandProvider shortcut="k">
            {children}
            <DocsCommandPalette entries={docsSearchEntries} />
          </CommandProvider>
        </SiteThemeProvider>
      </body>
    </html>
  );
}
