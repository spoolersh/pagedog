<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/mascot-wordmark-dark.png">
  <img src="docs/mascot-wordmark-light.png" alt="Pagedog, a blue dog with a headset" width="420">
</picture>

**Your fluffy on-call investigation companion.**

Fetches evidence, sniffs out incidents.

</div>

---

Pagedog is a simple tool that adds LLM investigations to your incidents.

Many teams today have it in one or another shape, so it's not a rocket science --
it's just an attempt to provide an open-source compillation of the approach --
hopefully the simplest one.

## :dog: Overview

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
mechanics -- the queue powering pagedog.


```
  

    Alertmanager   |   webhook  ┌────────────────┐      ┌─────────────┐
    incident.io    |  --------> │ pagedog server │ ───▶ │ alert queue │
    ...            |            └────────────────┘      └─────────────┘
                   |                                           │  
                   |                                           ▼
    MCPs           |            +----------------+      ┌─────────────┐
    Prometheus     |  <-------> |      LLMs      | <--> │   workers   │
    ...            |            +----------------+      └─────────────┘
                   |                                           │  
                   |                                           ▼
    Slack          |            ┌────────────────┐      ┌──────────────┐
    incident.io    |  ◀──────── │ pagedog notify │ ◀─── │ report queue │
    ...            |            └────────────────┘      └──────────────┘


```            

The [demo](#running-locally) is `docker compose up` away.

## :battery: What's included

Every row is turned on by the configuration key in its last column.

#### Alert sources

| source                  | how it pages                                                   | key                               |
| ----------------------- | -------------------------------------------------------------- | --------------------------------- |
| Prometheus Alertmanager | webhook receiver with a bearer token; one incident per firing alert | `webhook.sources[].alertmanager` |
| incident.io             | signed webhook, on incident created; test and retrospective incidents are skipped | `webhook.sources[].incidentio` |
| PagerDuty               | signed v3 webhook, on incident triggered; urgency is the severity        | `webhook.sources[].pagerduty` |

#### Notifications

| destination | what it gets                                                                 | key                   |
| ----------- | ---------------------------------------------------------------------------- | --------------------- |
| log         | a line in pagedog's own log: the incident and where the report is            | `notify[].log`        |
| webhook     | JSON with the incident and the report, its text included when it is a file; optional bearer token | `notify[].webhook` |
| Slack       | an incoming-webhook message: severity, title, the report as a link or its text | `notify[].slack`    |
| incident.io | an update on the incident the report is about, posted once                   | `notify[].incidentio` |
| PagerDuty   | a note on the incident the report is about, from a user of the account       | `notify[].pagerduty`  |

#### Models

| model  | how                                                                    | key                   |
| ------ | ---------------------------------------------------------------------- | --------------------- |
| Claude | the Anthropic Messages API, with a model name and a token limit; `ANTHROPIC_API_KEY` | `worker.model.claude` |
| OpenAI | the Responses API, with a model name and a token limit; `OPENAI_API_KEY` | `worker.model.openai` |
| Gemini | the Interactions API, with a model name and a token limit; `GEMINI_API_KEY` | `worker.model.gemini` |

#### Tools

| tool       | what the model gets                                                      | key                      |
| ---------- | ------------------------------------------------------------------------ | ------------------------ |
| Prometheus | `query_metrics`, a PromQL range query; `list_metrics`, the metric names  | `worker.tools.prometheus` |
| Loki       | `query_logs`, a LogQL range query; `list_log_labels`, the label names     | `worker.tools.loki`      |
| MCP        | every tool of a server, or those named; over HTTP with headers, or run by the worker | `worker.tools.mcp[]` |

#### Worker runtimes

| runtime | how a worker runs                                                      | key                              |
| ------- | ---------------------------------------------------------------------- | -------------------------------- |
| exec    | a child process, with the environment the file gives it and nothing else | `workers.runtime.exec`         |
| docker  | a container on a Docker engine, on a network, with volumes             | `workers.runtime.docker`         |

#### Report stores

| store | where a report goes                                                                    | key                |
| ----- | -------------------------------------------------------------------------------------- | ------------------ |
| fs    | a Markdown file per incident, in a directory (assumes one pagedog instance is running) | `worker.report.fs` |

## :computer: Running locally

The demo app is built to illustrate how pagedog works. It brings up Prometheus,
Alertmanager, Loki with Alloy collecting a simple http server logs, the Spooler
message server, and pagedog, which runs its workers as containers next to them.

```sh
export <YOUR MODEL>_API_KEY=...
docker compose up --build
```

Open [127.0.0.1:8081](http://127.0.0.1:8081). The page has a text field and two
buttons: one answers 200, the other 500. Type something, press 500, and watch:

1. Prometheus sees the 5xx rate rise, Alertmanager pages pagedog, the alert
   appears on the index page.
2. Pagedog launches a worker container, it asks the model and calling its tools.
3. A moments later the report lands on the page, the text you typed is likely included in the report.

`docker compose down -v` stops everything and drops the reports volume.

## :rocket: Running actually

This section sets up the real thing: incident.io paging pagedog and getting the
reports back, with the model reading Prometheus and Loki.

Pagedog is two binaries, `pagedog` and `worker`, and one YAML configuration.
Build the binaries from a checkout:

```sh
go install ./cmd/...
```

`pagedog` serves the webhook, keeps the workers running and delivers the
reports; `worker` is what it spins on demand, from wherever the configuration
points.

Around them you need a Spooler server, an API key for Claude, OpenAI or
Gemini, Prometheus, Loki or any [MCP](https://modelcontextprotocol.io) the
workers can reach, and whatever observability stack you use that can page via
webhook.

Write the configuration (the next section is a complete file), put the secrets
in the environment, and start it:

```sh
export SPOOLER_API_KEY=...
export INCIDENTIO_SIGNING_SECRET=...
export INCIDENTIO_API_KEY=...
export ANTHROPIC_API_KEY=...
pagedog --config /etc/pagedog/pagedog.yaml
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

## configuration

Pagedog reads one YAML file, `pagedog.yaml` by default, and takes three flags:
`--config`, `--log-level` and `--no-color`. Everything else is in the file. The
least of one that runs for real: incident.io at both ends, workers as child
processes, the reports kept as files, and the server's defaults for the queues:

```yaml
spooler:
  host: api.spooler.sh
  api_key: {env: SPOOLER_API_KEY}
  spool: default
  incidents:
    name: incidents
  reports:
    name: reports

webhook:
  listen: :7070
  sources:
    - incidentio:
        signing_secret: {env: INCIDENTIO_SIGNING_SECRET}

workers:
  max: 3
  runtime:
    exec:
      path: /usr/local/bin/worker
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
`cmd/worker/task.md` by default. `worker.task_file` names another, which the
launcher reads and passes the workers on their command line, so neither a
container nor a Pod needs a mount for it.

## faq

**Is it Spooler lock-in?** No. Pagedog is a webhook, a queue and a worker,
and what it asks of the queue is Spooler's delivery semantics: a lease the
worker renews and loses when it dies, redelivery to the next worker, one
atomic ack-and-send, and dedup by key. Those come from Spooler hosted at
`api.spooler.sh` or from its in-memory server, `spoolersh/memspoolerd`, which
[runs locally](https://docs.spooler.sh/local) and is free to run for your own
purposes, in production included.

**Is this vibe coded?** It was built by hand at first, as nothing more than a
showcase of how easy a pipeline on Spooler is to build. Very quickly it started
to look like a good generalisation of one problem I, Sergey, have seen and
heard about many times in the industry.
