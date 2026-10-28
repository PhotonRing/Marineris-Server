# Self-hosted CI runner

The workflows run on the project's self-hosted runner (label `self-hosted`),
with two exceptions that stay on GitHub's runners:

- **pull requests from forks**: anyone can open one, and its code would run
  on your server;
- **the integration test**: it starts the stack under the names, ports
  (80/443) and volumes of a real installation and deletes it afterwards, so it
  would replace a Marineris running on the same server.

## What the runner machine needs

Installed once, for the user the runner runs as:

- Docker Engine with the buildx plugin, and the runner user in the `docker`
  group (the image, scan and release jobs use Docker; `docker` access is
  root-equivalent, so give the runner its own user);
- `git`, `curl`, `gh` (GitHub CLI) and `shellcheck`;
- Chromium's system libraries for the browser tests (they need apt, so the
  jobs cannot install them themselves):
  ```sh
  cd test/e2e && npm ci && sudo npx playwright install-deps chromium
  ```

Go, Node, Trivy, cosign and the browser itself are installed by the jobs, in
the runner's tool cache or temporary folder.

## Good to know

- One runner runs one job at a time: GitHub runs a pull request's jobs in
  parallel, the runner one after the other. Registering more runners on the
  same machine (each in its own folder) runs them in parallel again.
- The release job builds both architectures with QEMU: it registers QEMU's
  binary formats on the host (`docker/setup-qemu-action`).
- Images and build caches accumulate; prune them from time to time:
  `docker image prune -f && docker buildx prune -f --filter until=168h`.
- In **Settings → Actions → General**, keep *Require approval for all
  outside collaborators* (fork pull requests run on GitHub's runners anyway).

**Verify:** a pull request from a branch of this repository shows its jobs
running on your runner (job log: *Runner name*), except *Integration*.
