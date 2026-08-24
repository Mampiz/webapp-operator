# Recording the media

The GIF and the screenshot in these docs are **generated from a running
cluster**, not captured by hand. Regenerating them is a make target, so they can
be refreshed when the CLI output changes instead of slowly going stale.

## Terminal demo → GIF

```bash
make record-demo
```

`hack/record-demo.sh` records the walkthrough and renders
`docs/assets/demo.gif`.

**Tooling.** [asciinema](https://asciinema.org/) captures the session and
[agg](https://github.com/asciinema/agg) renders the cast to a GIF. Both are
downloaded as static musl binaries into `./bin/`, the same convention the
Makefile already uses for `controller-gen` and `kustomize` — nothing is
installed system-wide and no root is required.

**Why a terminal recorder rather than a screen recorder.** The product is driven
by `kubectl`, so the artifact is text. A cast file records characters and timing,
which makes the output crisp at any size, keeps the GIF around 300 KB, and lets
the same recording be re-rendered with different speed or font size without
re-running anything.

**Setup is deliberately excluded.** Creating the cluster, installing
cert-manager and building the image are prerequisites, not the story. The script
runs them *before* the recorder starts, then records only the usage phase
(`DEMO_SKIP_SETUP=1`), so the GIF is about forty seconds of the operator being
used rather than ten minutes of Docker pulls.

**Rendering options** worth knowing, in `hack/record-demo.sh`:

| Option | Purpose |
|---|---|
| `--idle-time-limit 1.5` | Caps dead air while waiting for pods, without cutting output |
| `--speed 1.6` | Trims the remaining waits |
| `--font-size 15` | Keeps `kubectl` tables readable once the GIF is scaled down in a README |

To re-render an existing recording without touching the cluster:

```bash
./bin/agg --font-size 15 --speed 1.6 docs/assets/demo.cast docs/assets/demo.gif
```

## Grafana dashboard → screenshot

```bash
make screenshots
```

`hack/screenshot-grafana.sh` starts Prometheus and Grafana as containers, points
Prometheus at a locally running operator, provisions the dashboard from
[`config/grafana/`](../config/grafana/), generates some reconcile traffic so the
panels are not empty, and captures `docs/assets/grafana.png` with headless
Chromium.

Grafana is a web UI, so here a browser automation tool is the right instrument —
[Playwright](https://playwright.dev/) runs from its official container image
with `--network host`, which avoids installing Node, a browser or its system
libraries on the host.

The script tears down the containers it started when it finishes.

## Why generated rather than hand-captured

A screenshot pasted into a README is correct exactly once. When a column is
added to `kubectl get webapp` or a panel changes, a hand-captured image silently
starts lying, and nobody notices because regenerating it means remembering the
whole manual sequence. Making it a target turns that into one command.
