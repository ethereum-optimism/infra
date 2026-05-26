# Docker Node install scripts policy

Dependency installs in `Dockerfile` use `--ignore-scripts` by default.

## Allowlist

| Package | Reason | Dockerfile step |
|---------|--------|-----------------|
| _(none)_ | TypeScript app; no native install scripts required for image build | |
