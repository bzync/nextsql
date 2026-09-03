# Detection Heuristics

The bundled scanner recognizes common indicators including:

## Languages / ecosystems
- JavaScript/TypeScript: `package.json`, workspace manifests, tsconfig.
- Go: `go.mod`, `go.work`.
- Python: `pyproject.toml`, requirements files.
- PHP: `composer.json`, Artisan/Laravel indicators.
- Rust: `Cargo.toml`.
- Java/Kotlin: Maven/Gradle files.
- .NET: `.csproj`, `.sln`.

## Frontend
- Next.js, React, Vue/Nuxt, Svelte/SvelteKit, Vite.
- Tailwind configuration and common component directories.

## Data
- SQL migration directories/files.
- Prisma/Drizzle and common ORM indicators.
- Database names inferred only from manifest/config keys, never secrets.

## Infrastructure
- Docker/Containerfile, Compose.
- Kubernetes YAML, Helm, Kustomize.
- Terraform/OpenTofu, Ansible, Pulumi.
- GitHub/GitLab CI.
- Nginx/HAProxy/Caddy indicators.

Heuristics are signals, not authoritative architecture facts. The generated evidence file records what triggered each detection.
