# NextSQL website

Landing page, documentation, versioned downloads, and a release admin for NextSQL.

```bash
cd docs/web
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000).

| Path | What |
|---|---|
| `/docs` | Documentation |
| `/download` | Published binaries, checksums, and version comparison |
| `/admin` | Password-protected release admin |

```bash
npm run build
npm start
```

The site is a Next.js server (`next start`), not a static export, so the admin can accept binary uploads.

## Admin

Set `NEXTSQL_ADMIN_PASSWORD` (8+ characters) in `.env.local`. In development, if it is unset, the password is `nextsql-admin`.

1. Sign in at `/admin/login`
2. Create a version with highlights and a changelog (added / changed / fixed / removed / breaking)
3. Upload `nextsql`, `nextsqld`, `nextsql-bench`, or an archive per platform
4. Publish. The release appears on `/download` and can be compared with earlier versions

Catalog: `data/releases.json`. Binaries: `public/downloads/<version>/`.

Markdown pages are in `content/docs/`; navigation is `lib/nav.ts`.
