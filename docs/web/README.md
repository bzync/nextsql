# NextSQL website

Landing page, documentation, and versioned downloads for NextSQL. Pure static
site — no server, no admin console.

```bash
cd docs/web
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000).

| Path | What |
|---|---|
| `/docs` | Documentation |
| `/download` | Release history, checksums, and version comparison (read-only) |

```bash
npm run build   # emits static HTML into out/
npm start       # serves out/ locally for a production preview
```

Deployed to GitHub Pages by `.github/workflows/docs-pages.yml` on every push to
`master` that touches `docs/web/**` or `content/docs/**`.

## Publishing a release

There's no admin UI — releases are data, edited directly:

1. Upload the installer artifacts to a GitHub Release (`gh release create vX.Y.Z
   …`). Download buttons on `/download` point at
   `https://github.com/bzync/nextsql/releases/download/v<version>/<filename>`.
2. Add or edit the version's entry in `data/releases.json` (highlights, changelog,
   artifact filename/size/sha256) and commit it. `public/downloads/` is gitignored
   and unused; do not commit binaries.

Markdown pages are in `content/docs/`; navigation is `lib/nav.ts`.
