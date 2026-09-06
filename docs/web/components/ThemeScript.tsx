"use client";

const STORAGE_KEY = "nextsql-theme";

const themeScript = `(function(){try{var q=new URLSearchParams(location.search).get("theme");var t=q||localStorage.getItem(${JSON.stringify(STORAGE_KEY)});var d=t!=="light";var root=document.documentElement;root.classList.toggle("dark",d);root.classList.add("rui-theme","rtui-theme");root.dataset.theme=d?"dark":"light";var m=document.querySelector('meta[name="theme-color"]');if(m)m.setAttribute("content",d?"#040912":"#f3f4f6");}catch(e){document.documentElement.classList.add("dark");}})();`;

// SSR: executable so the saved theme applies before first paint.
// Client: text/plain so React 19 does not warn on a client-created <script>.
export function ThemeScript() {
  return (
    <script
      type={typeof window === "undefined" ? "text/javascript" : "text/plain"}
      suppressHydrationWarning
      dangerouslySetInnerHTML={{ __html: themeScript }}
    />
  );
}
