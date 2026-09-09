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
npm run test:search # verifies the rendered function page and search payload
npm start       # serves out/ locally for a production preview
```

Deployed to GitHub Pages by `.github/workflows/docs-pages.yml` on relevant
`master` pushes and whenever a release is published.

## Publishing a release

GitHub Releases is the binary store and the authoritative source for published
dates, channels, filenames, sizes, SHA-256 digests, and download URLs.

1. Before tagging, add the release's editorial content (title, summary,
   highlights, and structured changes) to `data/releases.json`.
2. Push a `vX.Y.Z` tag. `.github/workflows/release-installers.yml` verifies that
   the tag matches `internal/version.String`, builds the supported packages into
   runner-temporary storage, verifies `SHA256SUMS`, and creates the immutable
   GitHub Release. It refuses to overwrite an existing release.
3. The release workflow calls the Pages workflow. `npm run releases:sync`
   retrieves the published release and checksum manifest, validates them against
   GitHub's SHA-256 digests, and generates the build-only
   `data/releases.github.json` catalog used by the static export.

`installers/` and `data/releases.github.json` are disposable local/CI outputs
and are gitignored. Do not commit installer binaries. For a local catalog check:

```bash
npm run releases:sync
```

Markdown pages are in `content/docs/`; navigation is `lib/nav.ts`.
