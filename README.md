<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/mascot-wordmark-dark.png">
  <img src="assets/mascot-wordmark-light.png" alt="Pagedog, a blue dog with a headset" width="420">
</picture>

**Your on-call investigation companion.**

Fetches evidence, sniffs out incidents.

</div>

---

Pagedog is a simple tool that adds LLM investigations to your incidents.

Many teams today have it in one shape or another, so it's not rocket science –
it's just an attempt to provide an open-source compilation of the approach –
hopefully the simplest one.

<br/>

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

Workers are designed to be disposable: kill a worker at any time and its job is
redelivered to another one; nothing is lost, nothing is done twice.

The LLM you choose uses the configured tools, so it can explore your
observability stack or fetch runbooks during the investigation.

The messages in both queues are leased on receive, and workers can renew them
periodically for long-running investigations. A worker that dies stops
renewing, the lease expires, and the message will be redelivered to another
worker. See [Spooler Delivery](https://docs.spooler.sh/delivery) for the
mechanics – the queue powering pagedog.

The [demo](#running-locally) is one `docker compose up` away.

<br/>

## Running locally

The demo is built to illustrate how pagedog works. It brings up Prometheus,
Alertmanager, Loki with Alloy collecting a simple HTTP server's logs, the
Spooler message server, and pagedog, which runs its workers as containers next
to them.

```sh
export <YOUR MODEL>_API_KEY=...
docker compose -f demo/compose.yaml up --build
```

Open [127.0.0.1:8081](http://127.0.0.1:8081). The page has a text field and two
buttons: one answers 200, the other 500. Type something, press 500, and watch:

1. Prometheus sees the 5xx rate rise, Alertmanager pages pagedog, and the alert
   appears on the index page.
2. Pagedog launches a worker container, which asks the model and runs the tools
   it calls.
3. A moment later the report lands on the page; the text you typed is likely
   in it.

`docker compose -f demo/compose.yaml down -v` stops everything and drops the
reports volume. The stack pulls the `spoolersh/pagedog` image; to run the
checkout instead, `make pagedog-image` builds it under that tag first.

<br/>

## Running actually

Pagedog is one binary and one YAML file:
- `pagedog server` serves the webhook, keeps the workers running and delivers
  the reports
- `pagedog worker` is what it spins on demand, from the same binary: as a
  goroutine of its own process, as a child process, or as a container of the
  same image, every one reading the same file

The binary comes from the
[releases](https://github.com/spoolersh/pagedog/releases), as the
`spoolersh/pagedog` image, or from a checkout with `go install ./cmd/pagedog`;
`pagedog version`, or `pagedog -v`, says which one you have.

Around pagedog you would also need:
- a Spooler message server, the hosted one or the free-to-run [in-memory
  image](https://docs.spooler.sh/local)
- an API key for your LLM of choice
- whatever pages you, as long as it can call a webhook: Alertmanager,
  incident.io or PagerDuty, pointed at pagedog's `/webhook/<source>`

You would also need at least one of these reachable by your workers during an
investigation:
- an [MCP](https://modelcontextprotocol.io) server
- Prometheus and/or Loki instances, whose APIs pagedog reads directly

Rather than one manifest for one of the many combinations, [SKILL.md](SKILL.md)
states what pagedog needs from a platform, in the Agent Skills format: a
runbook for you, and a skill for an assistant to write the deployment you want.


### Prompt to deploy

Paste this into your assistant and answer the questions it asks:

```text
Read https://raw.githubusercontent.com/spoolersh/pagedog/main/SKILL.md and https://raw.githubusercontent.com/spoolersh/pagedog/main/pagedog.yaml, then follow the skill's "Deployment" section: ask me its questions one at a time, and write me the deployment from my answers.
```

<br/>

## What's included

- **Sources**: Prometheus Alertmanager, incident.io, PagerDuty
- **Notifications**: log, webhook, Slack, incident.io, PagerDuty
- **Models**: Claude, OpenAI, Gemini
- **Tools**: Prometheus, Loki, any MCP server
- **Worker runtimes**: goroutines of the server, exec, docker
- **Report stores**: fs, GitHub issues

Each is turned on by its key in the configuration;
[pagedog.yaml](pagedog.yaml) documents every one.

<br/>

## Configuration

`pagedog server` reads one YAML file, `pagedog.yaml` by default, and takes three flags:
`--config`, `--log-level` and `--no-color`. Everything else is in the file.

The smallest configuration example is below: incident.io at both ends, workers
as goroutines of the server, the reports kept as files:

```yaml
spooler:
  host: api.spooler.sh # (or local image)
  api_key: {env: SPOOLER_API_KEY}
  spool: default
  incidents: incidents
  reports: reports

webhook:
  listen: :7070
  sources:
    - incidentio:
        signing_secret: {env: INCIDENTIO_SIGNING_SECRET}

worker:
  model:
    claude:
      name: claude-opus-5-5
  tools:
    prometheus:
      url: http://prometheus.internal:9090
    loki:
      url: http://loki.internal:3100
  report:
    fs:
      dir: /var/lib/pagedog/reports

notify:
  - incidentio:
      api_key: {env: INCIDENTIO_API_KEY}
```

The reports are files the workers write and the server reads back, so a second
pagedog instance needs the same directory mounted; or they are issues of a
GitHub repository, one per incident and the same incident updating its issue,
which needs nothing mounted.

`pagedog.yaml` at the repository root has every setting, documented: a
commented one is a default, an alternative to the one above it, or one that may
be left out. `demo/local.yaml` is the file for one machine, with the in-memory
Spooler server on localhost and Alertmanager as the source.

A secret is never written in the file. It says where the secret comes from:

```yaml
api_key: {env: SPOOLER_API_KEY}
token:   {file: /run/secrets/alertmanager-token}
url:     {exec: {cmd: op, args: [read, "op://vault/item/field"]}}
```

The variable names are yours. The examples use the other side's own where it
has one, as Spooler's CLI reads `SPOOLER_API_KEY`.

Workers are goroutines of the server unless `workers.runtime` names `exec` or
`docker`. Whatever the runtime, a worker reads the same file the server does,
as nginx's workers do: its `worker` section, and `spooler`, unless
`worker.spooler` names the Spooler the workers reach, as containers reaching a
host's localhost must. The runtime runs what the file says and nothing more:
`exec` runs `path` with `args`, which for pagedog is the binary and
`[worker, --config, FILE]`; `docker` runs `image` with `command`, or with the
image's own, which for `spoolersh/pagedog` is
`worker --config /etc/pagedog/pagedog.yaml`. The file reaches a container by a
bind, or by `files`, a map of container paths to files the server copies in
before the container starts, for a server that is a container itself and has
no host path to bind. Nothing else travels from the server to a worker.

Under a named runtime a worker gets nothing of the server's environment: the
runtime's `env` is all of it, each value written as itself or, for a secret, as
where it comes from, and that is where a worker's own `{env: NAME}` secrets are
read from. A runtime can run another program altogether, with `exec.path` or a
docker `command`; that program gets its environment the same way and reads
nothing of the file.

The model's key is `api_key` under the model, a secret like the rest. If empty,
the SDK reads its own variable, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` or
`GEMINI_API_KEY`, of the worker's environment: a default to lean on only with
goroutine workers, whose environment is the server's.

The model's tools are the two built in, Prometheus and Loki, and any MCP
server:

```yaml
worker:
  tools:
    mcp:
      - name: example
        url: https://mcp.example.com/mcp
        headers:
          X-Tenant: acme
          X-API-Key: {env: EXAMPLE_API_KEY}
        tools:
          - search_logs
          - query_metrics
```

The task the model gets is a Go text template over the incident,
`cmd/pagedog/task.md` by default. `worker.task` holds another: written in the
file itself, or as `{file: PATH}` the worker reads, so it has to be where the
workers run.

<br/>

## Contributing

PRs are welcome, and most of all for new sources, notifiers, tools,
report stores and worker runtimes: each is one package under `internal/`,
named after what it talks to. For one to land it needs to:

- follow the code style in [AGENTS.md](AGENTS.md), tests included;
- be wired into the configuration in `cmd/pagedog`, with its key in
  [pagedog.yaml](pagedog.yaml) and the wiring tested;
- be documented here, in "What's included" and "Configuration", and in
  [SKILL.md](SKILL.md), so an assistant can deploy it.

For anything beyond a new one of those or a bug fix, please open an issue
first, so it is discussed before it is written.

<br/>

## FAQ

#### Is it a cheeky Spooler lock-in?

No. The only thing pagedog expects of the queue is compatibility with Spooler's
delivery semantics: leased messages, redelivery, atomic ack-and-send, and
dedup. Those come from either hosted Spooler or from its in-memory server,
`spoolersh/memspoolerd`, which [runs locally](https://docs.spooler.sh/local)
and is free to run for your own purposes, production included.

<br/>

## License

MIT, see [LICENSE](LICENSE).
