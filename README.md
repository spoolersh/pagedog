<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/mascot-wordmark-dark.png">
  <img src="docs/mascot-wordmark-light.png" alt="Pagedog, a blue dog with a headset" width="420">
</picture>

**Your on-call investigation companion.**

Fetches evidence, sniffs out incidents.

</div>

---

Pagedog is a simple tool that adds LLM investigations to your incidents.

Many teams today have it in one or another shape, so it's not a rocket science –
it's just an attempt to provide an open-source compillation of the approach –
hopefully the simplest one.

<br/>

## :dog: Overview

```
  

    Alertmanager   |   webhook  ┌────────────────┐      ┌───────────────┐
    incident.io    |  --------> │ pagedog server │ ───▶ │  alert queue  │
    ...            |            └────────────────┘      └───────────────┘
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

An alert comes in over a webhook, becomes a job on a queue, a worker picks it
up and hands it over to an LLM model to investigate and write a report, which
is then delivered as a notification.

Workers are designed to be disposable: kill a worker at any time and its job is
redelivered to another one; nothing is lost, nothing is done twice.

The LLM model you choose uses configured tools so it can explore your
observability stack or fetch runbooks during investigation.

The messages in both queues are leased on receive and workers can renew them
periodically for long running investigations. A worker that dies stops
renewing, the lease expires, and the message will be redelivered to another
worker. See [Spooler Delivery](https://docs.spooler.sh/delivery) for the
mechanics – the queue powering pagedog.

The [demo](#running-locally) is one `docker compose up` away.

<br/>

## :computer: Running locally

The demo app is built to illustrate how pagedog works. It brings up Prometheus,
Alertmanager, Loki with Alloy collecting a simple http server logs, the Spooler
message server, and pagedog, which runs its workers as containers next to them.

```sh
export <YOUR MODEL>_API_KEY=...
docker compose -f demo/compose.yaml up --build
```

Open [127.0.0.1:8081](http://127.0.0.1:8081). The page has a text field and two
buttons: one answers 200, the other 500. Type something, press 500, and watch:

1. Prometheus sees the 5xx rate rise, Alertmanager pages pagedog, the alert
   appears on the index page.
2. Pagedog launches a worker container, it asks the model and calling its tools.
3. A moments later the report lands on the page, the text you typed is likely included in the report.

`docker compose -f demo/compose.yaml down -v` stops everything and drops the
reports volume. The stack pulls the `spoolersh/pagedog` image; to run the
checkout instead, `make pagedog-image` builds it under that tag first.

<br/>

## :rocket: Running actually

This section sets up the real thing: incident.io paging pagedog and getting the
reports back, with the model reading Prometheus and Loki.

Pagedog is one binary and one YAML configuration. The binary is the
`spoolersh/pagedog` image, or built from a checkout:

```sh
go install ./cmd/pagedog
```

`pagedog server` serves the webhook, keeps the workers running and delivers
the reports; `pagedog worker` is what it spins on demand, from the same
binary: as a child process, or as a container of the same image.

Around them you need a Spooler server, an API key for Claude, OpenAI or
Gemini, Prometheus, Loki or any [MCP](https://modelcontextprotocol.io) the
workers can reach, and whatever observability stack you use that can page via
webhook.

Make the two queues first: pagedog creates none, and refuses to start while
one is missing. However you manage the rest of your Spooler account works; by
hand it is two calls:

```sh
curl -X PUT https://api.spooler.sh/v1/spools/default/queues/incidents \
  -H "Authorization: Bearer $SPOOLER_API_KEY" \
  --json '{"leaseTimeoutSeconds": 60, "maxRetries": 3, "dedupWindowSeconds": 300}'
curl -X PUT https://api.spooler.sh/v1/spools/default/queues/reports \
  -H "Authorization: Bearer $SPOOLER_API_KEY" \
  --json '{"leaseTimeoutSeconds": 60, "maxRetries": 3, "dedupWindowSeconds": 300}'
```

Three settings matter to pagedog. The dedup window on the incidents queue is
what makes a repeated notification the same incident instead of another
investigation. The lease timeout is how long a dead worker's incident waits to
be redelivered; a live worker renews, so it can be short. Max retries bounds
what one incident that keeps failing costs in model calls. [Create a
queue](https://docs.spooler.sh/operations/createQueue) documents the rest, and
[demo/queues.d](demo/queues.d) is the demo's version of these two, declared to
memspoolerd.

Write the configuration (the next section is a complete file), put the secrets
in the environment, and start it:

```sh
export SPOOLER_API_KEY=...
export INCIDENTIO_SIGNING_SECRET=...
export INCIDENTIO_API_KEY=...
export ANTHROPIC_API_KEY=...
pagedog server --config /etc/pagedog/pagedog.yaml
```

Point an incident.io webhook at `https://<your host>/webhook/incidentio`,
subscribed to the incident created event, with the endpoint's signing secret in
`INCIDENTIO_SIGNING_SECRET`. Every incident declared there is
investigated, and the report comes back as an update on it, posted once however
many times the queue delivers it.

Without Prometheus and Loki running, the model's tools fail and it says so in
its report. Those two are built in; anything else the model should see comes
through an MCP server, remote over HTTP or local as a program the worker runs,
as [configuration](#configuration) shows.

<br/>

## :battery: What's included

- **Alert sources**: Prometheus Alertmanager, incident.io, PagerDuty
- **Notifications**: log, webhook, Slack, incident.io, PagerDuty
- **Models**: Claude, OpenAI, Gemini
- **Tools**: Prometheus, Loki, any MCP server
- **Worker runtimes**: exec, docker
- **Report stores**: fs

Each is turned on by its key in the configuration;
[pagedog.yaml](pagedog.yaml) documents every one.

<br/>

## :gear: Configuration

`pagedog server` reads one YAML file, `pagedog.yaml` by default, and takes three flags:
`--config`, `--log-level` and `--no-color`. Everything else is in the file. The
least of one that runs for real: incident.io at both ends, workers as child
processes, the reports kept as files:

```yaml
spooler:
  host: api.spooler.sh
  api_key: {env: SPOOLER_API_KEY}
  spool: default
  incidents: incidents
  reports: reports

webhook:
  listen: :7070
  sources:
    - incidentio:
        signing_secret: {env: INCIDENTIO_SIGNING_SECRET}

workers:
  max: 3
  runtime:
    exec:
      env:
        PAGEDOG_SPOOLER_API_KEY: {env: SPOOLER_API_KEY}
        ANTHROPIC_API_KEY: {env: ANTHROPIC_API_KEY}

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

The reports are files the workers write and the server reads back, so a
second pagedog instance needs the same directory mounted.

`pagedog.yaml` at the repository root has every setting, documented: a
commented one is a default, an alternative to the one above it, or one that
may be left out. `demo/local.yaml` is the file for one machine, with the
in-memory Spooler server on localhost and Alertmanager as the source.

A secret is never written in the file. It says where the secret comes from:

```yaml
api_key: {env: SPOOLER_API_KEY}
token:   {file: /run/secrets/alertmanager-token}
url:     {exec: {cmd: op, args: [read, "op://vault/item/field"]}}
```

The variable names are yours. The examples use the other side's own where it
has one, as Spooler's CLI reads `SPOOLER_API_KEY`.

A worker gets nothing of the launcher's environment: its runtime's `env` is all
of it, each value written as itself or, for a secret, as where it comes from.
The worker reads its Spooler API key as `PAGEDOG_SPOOLER_API_KEY`, the one
variable name pagedog fixes, and the model's key under the SDK's own,
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY` or `GEMINI_API_KEY`; both are secrets.
The worker itself has no file: the launcher passes it the minimum on its
command line.

The model's tools are the two built in, over Prometheus and Loki, and any MCP
server: one reached over HTTP, with the headers it wants on every request, or
a program in the worker's image that the worker runs and talks to over its
standard streams, with the environment variables named for it and nothing
else. A header is written as itself or, for a secret, as `{env: NAME}`: the
variable of the worker's environment that holds it. A server's `tools` list
keeps the model's menu short; left out, the model gets every tool the server
has, which for a server that can write is the way to keep the model reading.

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
`cmd/pagedog/task.md` by default. `worker.task_file` names another, which the
launcher reads and passes the workers on their command line, so neither a
container nor a Pod needs a mount for it.

<br/>

## :thinking: FAQ

<details>
<summary>Is it a cheeky Spooler lock-in?</summary>

No. Pagedog is a webhook, a queue and workers. What it expects from the queue
is to be compatible with Spooler's delivery semantics: leased messages,
redelivery, atomic ack-and-send, and dedup. Those come from either hosted
Spooler or from its in-memory server, `spoolersh/memspoolerd`, which [runs
locally](https://docs.spooler.sh/local) and is free to run for your own
purposes, production included.

</details>

<details>
<summary>Is this vibe coded?</summary>

It was built by hand at first, as nothing more than a showcase of how easy a
pipeline on Spooler is to build. Very quickly it started to look like a good
generalisation of one problem I, Sergey, have seen and heard about many times
in the industry.

</details>
