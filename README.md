<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/mascot-wordmark-dark.png">
  <img src="assets/mascot-wordmark-light.png" alt="Pagedog, a blue dog with a headset" width="420">
</picture>

**Your on-call investigation companion.**

Fetches evidence, sniffs out incidents.

</div>

---

Pagedog is a simple tool that investigates your incidents using the logs,
metrics and tools you connect to your LLM, then sends findings to your existing
incident workflow.

Many teams today have it in one shape or another, so it's not rocket science –
it's just an attempt to provide an open-source compilation of the approach –
hopefully the simplest one.

## :dog: Overview

```
  

 Alertmanager   |   webhook  ┌────────────────┐      ┌────────────────┐
 incident.io    |  --------> │ pagedog server │ ───▶ │ incident queue │
 ...            |            └────────────────┘      └────────────────┘
                |                                            │  
                |                                            ▼
 MCPs           |            +----------------+      ┌────────────────┐
 Prometheus     |  <-------> |      LLMs      | <--> │ pagedog worker │
 ...            |            +----------------+      └────────────────┘
                |                                            │  
                |                                            ▼
 Slack          |            ┌────────────────┐      ┌────────────────┐
 incident.io    |  ◀──────── │ pagedog server │ ◀─── │  report queue  │
 ...            |            └────────────────┘      └────────────────┘


```            

A page comes in over a webhook, becomes an incident on a queue, a worker picks
it up and hands it over to an LLM to investigate and write a report, which is
then delivered as a notification.

Workers, if run outside the server process, are designed to be disposable: kill
a worker at any time and its job is redelivered to another one; nothing is
lost.

The LLM you choose uses the configured tools, so it can explore your
observability stack, fetch runbooks and look up your source code during the
investigation.

The messages in both queues are leased on receive, and workers can renew them
periodically for long-running investigations. A worker that dies stops
renewing, the lease expires, and the message will be redelivered to another
worker. See [Spooler Delivery](https://docs.spooler.sh/delivery) for the
mechanics – the queue powering pagedog.

The [demo](#running-locally) is one `docker compose up` away.

## Prompt to deploy

If you're in a hurry, paste this to your artificially intelligent agentic
:robot: and answer the questions it asks:

```text
Read https://raw.githubusercontent.com/spoolersh/pagedog/main/SKILL.md and
https://raw.githubusercontent.com/spoolersh/pagedog/main/pagedog.yaml, then
follow the skill's "Deployment" section: ask me its questions one at a time,
and write me the deployment from my answers.
```

## Running locally

The demo is built to illustrate how pagedog works. It brings up Prometheus,
Alertmanager, Loki with Alloy collecting a simple HTTP server's logs, the
Spooler message server, and pagedog, which runs its workers as containers next
to them. The workers ask Claude; `examples/docker/pagedog.yaml` is where to
switch.

```sh
export ANTHROPIC_API_KEY=...
docker compose -f examples/docker/compose.yaml up --build
```

Open [127.0.0.1:8081](http://127.0.0.1:8081). The page has a text field and two
buttons: one answers 200, the other 500. Type something, press 500, and watch:

1. Prometheus sees the 5xx rate rise, Alertmanager pages pagedog, and the alert
   appears on the index page.
2. Pagedog launches a worker container, which asks the model and runs the tools
   it calls.
3. A moment later the report lands on the page; the text you typed is likely
   in it.

`docker compose -f examples/docker/compose.yaml down -v` stops everything and
drops the reports volume. The stack pulls the `spoolersh/pagedog` image; to run the
checkout instead, `make pagedog-image` builds it under that tag first.

## Running actually

Pagedog is one binary and one YAML file:
- `pagedog server` serves the webhook, keeps the workers running and delivers
  the reports
- `pagedog worker` is what gets spun up on demand, from the same binary reading
  the same configuration file

The binary comes from the
[releases](https://github.com/spoolersh/pagedog/releases), as the
`spoolersh/pagedog` image, or from a checkout with `go install ./cmd/pagedog`;
`pagedog version`, or `pagedog -v`, says which one you have.

Around pagedog you would also need:
- a Spooler message server, the hosted one or the free-to-run [in-memory
  image](https://docs.spooler.sh/local)
- an API key for your LLM of choice
- one of the pagers pagedog understands, Alertmanager, incident.io or
  PagerDuty, pointed at pagedog's `/webhook/<source>`

You would also want at least one of these reachable by your workers during an
investigation:
- an [MCP](https://modelcontextprotocol.io) server
- Prometheus and/or Loki instances, whose APIs pagedog reads directly

[examples/k8s](examples/k8s) is the least Kubernetes deployment: one pod, the
workers as its goroutines. For the other combinations, [SKILL.md](SKILL.md)
states what pagedog needs from a platform, in the Agent Skills format: a
runbook for you, and a skill for an assistant to write the deployment you want.

## What's included

- **Sources**: Prometheus Alertmanager, incident.io, PagerDuty
- **Notifications**: log, webhook, Slack, incident.io, PagerDuty
- **Models**: Claude, OpenAI, Gemini
- **Tools**: Prometheus, Loki, any MCP server
- **Worker runtimes**: goroutines of the server, exec, docker
- **Report stores**: fs, GitHub issues

Each is turned on by its key in the configuration; [pagedog.yaml](pagedog.yaml)
documents every one.

## Configuration

`pagedog server` reads one YAML file, `pagedog.yaml` by default, and takes three flags:
`--config`, `--log-level` and `--no-color`. Everything else is in the file.

Please read [pagedog.yaml](pagedog.yaml) as the full configuration registry.

## Contributing

PRs are welcome, and most of all for new sources, notifiers, tools,
report stores and worker runtimes: each is one package under `internal/`,
named after what it talks to. For one to land it needs to:

- follow the code style in [AGENTS.md](AGENTS.md), tests included;
- be wired into the configuration in `cmd/pagedog`, with its key in
  [pagedog.yaml](pagedog.yaml) and the wiring tested;
- be documented here, in "What's included", and in [SKILL.md](SKILL.md), so
  an assistant can deploy it.

For anything beyond a new one of those or a bug fix, please open an issue
first, so it is discussed before it is written.

## FAQ

#### Can I operate this _durably_ without the hosted service?

Not with the queue backends currently supported. Pagedog uses hosted Spooler or
its local server, `spoolersh/memspoolerd`. The local server keeps messages in
memory and loses them when the container exits. It is free to run for your own
purposes, production included, but may not be provided to others or hosted as a
service; see [Running locally](https://docs.spooler.sh/local).

#### What would changing the queue require?

Spooler is the only queue integration today. Another backend would need to
preserve the behavior pagedog relies on: leases and renewal, redelivery,
deduplication, and the atomic handoff from an incident to its report. Spooler's
[delivery docs](https://docs.spooler.sh/delivery) describe those semantics,
including the ack-and-send operation used for that handoff.

## License

MIT, see [LICENSE](LICENSE).
