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

## More runners: jobs in parallel

One runner runs one job at a time. Several runners on the same machine run a
pull request's jobs in parallel again (the jobs pick free ports, so they do
not collide). Each runner is a folder of its own, for the same `gh-runner`
user; in **Settings → Actions → Runners → New self-hosted runner**, copy the
download URL and the token, then for runner 2:

```sh
sudo -u gh-runner -H bash -c '
  mkdir ~/actions-runner-2 && cd ~/actions-runner-2 &&
  curl -fsSL -o runner.tar.gz <download URL> && tar xzf runner.tar.gz && rm runner.tar.gz &&
  ./config.sh --unattended --url https://github.com/PhotonRing/Marineris-Server \
    --token <token> --name marineris-2 --labels self-hosted'
cd /home/gh-runner/actions-runner-2 && sudo ./svc.sh install gh-runner && sudo ./svc.sh start
```

About as many runners as the machine has cores divided by four works well:
the Go builds and tests already use every core of the machine (Go compiles
in parallel by itself), and the browser tests run one after the other by
design (each one continues the previous one's state).

## Good to know

- A runner keeps the `PATH` of the user who configured it in its `.path`
  file. After moving a runner to another user, reset it, then restart the
  runner:
  `echo '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' | sudo -u gh-runner tee ~gh-runner/actions-runner/.path`
- The release job builds both architectures with QEMU: it registers QEMU's
  binary formats on the host (`docker/setup-qemu-action`).
- Images and build caches accumulate; prune them from time to time:
  `docker image prune -f && docker buildx prune -f --filter until=168h`.
- In **Settings → Actions → General**, keep *Require approval for all
  outside collaborators* (fork pull requests run on GitHub's runners anyway).

**Verify:** a pull request from a branch of this repository shows its jobs
running on your runner (job log: *Runner name*), except *Integration*.
