import type { Metadata, Viewport } from "next";
import { Geist_Mono, Manrope, Syne } from "next/font/google";
import "@bzync/rui/styles.css";
import "./globals.css";
import { site } from "@/lib/site";
import { SiteThemeProvider } from "@/components/SiteThemeProvider";

const manrope = Manrope({
  variable: "--font-geist",
  subsets: ["latin"],
  display: "swap",
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
  display: "swap",
});

const syne = Syne({
  variable: "--font-brand-face",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700", "800"],
  display: "swap",
});

const themeScript = `(function(){try{var q=new URLSearchParams(location.search).get("theme");var t=q||localStorage.getItem("nextsql-theme");var d=t!=="light";var root=document.documentElement;root.classList.toggle("dark",d);root.classList.add("rui-theme","rtui-theme");root.dataset.theme=d?"dark":"light";var m=document.querySelector('meta[name="theme-color"]');if(m)m.setAttribute("content",d?"#040912":"#f8fafc");}catch(e){document.documentElement.classList.add("dark");}})();`;

export const viewport: Viewport = {
  themeColor: "#040912",
  viewportFit: "cover",
};

export const metadata: Metadata = {
  metadataBase: new URL("https://nextsql.dev"),
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
  return (
    <html
      lang="en"
      suppressHydrationWarning
      data-scroll-behavior="smooth"
      className={`${manrope.variable} ${geistMono.variable} ${syne.variable} dark h-full antialiased`}
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeScript }} />
      </head>
      <body
        className="min-h-full bg-bg font-sans text-foreground"
        suppressHydrationWarning
      >
        <a href="#content" className="skip-link">
          Skip to content
        </a>
        <SiteThemeProvider>{children}</SiteThemeProvider>
      </body>
    </html>
  );
}
