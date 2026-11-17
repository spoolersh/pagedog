---
name: pagedog
description: Deploy and configure pagedog (github.com/spoolersh/pagedog), the
on-call investigation companion that takes pages from Alertmanager, incident.io
or PagerDuty, investigates each with an LLM worker (Claude, OpenAI or Gemini)
reading Prometheus, Loki or any MCP server, and reports back to a log, a
webhook, Slack, incident.io or PagerDuty, over a Spooler queue. Use when a task
mentions pagedog, spoolersh/pagedog, or asks to run LLM investigations of
alerts or incidents, on Kubernetes, systemd, Quadlet, Compose or anything else.
Covers the binary, the image, the one configuration file, the secrets, the
Spooler instance to run against (hosted, or the free in-memory image), the
queues, the worker runtimes and what a platform has to provide. Its Deployment
section is a wizard: ask the user the platform, the Spooler, the page source,
the report destination, the model, the tools and the runtime, one question at a
time, then write the configuration and the platform's specs from the answers.
metadata:
  author: spoolersh
  docs: https://github.com/spoolersh/pagedog
---

# Pagedog

Pagedog is one binary and one YAML file. `pagedog server` serves a webhook
pages come to, puts each on a Spooler queue, keeps workers scaled to the queue,
and delivers the workers' reports. `pagedog worker` is what it spins per
incident, from the same binary: by default a goroutine of the server's own
process, or a child process, or a container of the same image, each reading the
same file. The queue is Spooler, https://docs.spooler.sh: leased messages,
redelivery when a worker dies, dedup on the incident's key. Read those docs for
anything about Spooler this file does not state; never guess server behaviour.

Read first, from the repository or its raw URLs under
https://raw.githubusercontent.com/spoolersh/pagedog/main/:

- `pagedog.yaml`: the reference configuration, every key with its meaning, its
  default as a commented line, every alternative as a commented block. Read it
  whole and choose among what it offers; the file you write is a subset of it,
  and a key it does not have does not exist.
- `README.md`: the overview and the demo.
- `demo/compose.yaml` and `demo/pagedog.yaml`: a whole stack with the docker
  runtime, if that is what is wanted.

## The binary and the image

- Image `spoolersh/pagedog`, entrypoint `pagedog`, command `worker --config
  /etc/pagedog/pagedog.yaml`: a container of it with no command is a worker. A
  server is run as `server --config /etc/pagedog/pagedog.yaml`. From a
  checkout, `go install ./cmd/pagedog`.
- `pagedog server` takes three flags: `--config` (default `pagedog.yaml`),
  `--log-level` and `--no-color` (on when `NO_COLOR` is set). Everything else
  is in the file.
- `pagedog worker` takes `--config` and `--log-level`, and reads the same file:
  the server runs it so, and so can a hand.
- Logs are JSON lines on stderr. There is no health endpoint: a TCP check on
  the webhook port is what there is. It exits non-zero with the error on stderr
  when the file is wrong, a secret is missing or a queue does not exist.
- The image sets no user. Set one if the platform wants it, with the reports
  directory writable by it.

The reference run, which every platform's pieces map onto:

```sh
export SPOOLER_API_KEY=...
export INCIDENTIO_SIGNING_SECRET=...
export INCIDENTIO_API_KEY=...
export ANTHROPIC_API_KEY=...
docker run -d --restart unless-stopped --name pagedog \
  -e SPOOLER_API_KEY -e INCIDENTIO_SIGNING_SECRET \
  -e INCIDENTIO_API_KEY -e ANTHROPIC_API_KEY \
  -v /etc/pagedog/pagedog.yaml:/etc/pagedog/pagedog.yaml:ro \
  -v pagedog-reports:/var/lib/pagedog/reports \
  -p 7070:7070 \
  spoolersh/pagedog server --config /etc/pagedog/pagedog.yaml
```

A bare `-e NAME` copies the variable from the shell, so no secret is on the
command line. The volume is where the least configuration below keeps the
reports. Against the in-memory Spooler on the same machine, the
configuration names it `host.docker.internal:8080` with `no_tls: true`, as
a container's localhost is its own. From a checkout it is `pagedog server
--config /etc/pagedog/pagedog.yaml` with the same variables set.

## The configuration

One file, five sections, and `pagedog.yaml` at the repository root is the
whole of it. Each choice is on by its key; this is everything there is:

- `spooler`: `host`, `api_key` (a secret), `spool`, `no_tls` for the
  in-memory server only, and the names of the two queues, `incidents` and
  `reports`.
- `webhook`: `listen`, a host:port, and `sources`, a list, each served at
  `/webhook/<source>`: `alertmanager` with `token`, a bearer token, one
  incident per firing alert; `incidentio` with `signing_secret`, one per
  incident_created event; `pagerduty` with `secret`, one per
  incident.triggered event.
- `workers`: `min` (default 0), `max` (default 3), and `runtime`: if
  empty, goroutines of the server; `exec` with `path`, `args`, `env` and
  `stop_timeout`; `docker` with `image`, `command` (if empty, the image's
  own, which for `spoolersh/pagedog` is
  `worker --config /etc/pagedog/pagedog.yaml`), `files` (container path to
  a file of the server's, copied in before the container starts), `env`,
  `network`, `binds` and `stop_timeout`. The runtime runs what is written,
  nothing implied: a worker that is pagedog is the binary as
  `[worker, --config, FILE]`, and the file reaches a container by a bind, or
  by `files` when the server is a container itself.
- `worker.spooler`: the Spooler as the workers reach it, the keys of
  `spooler` again; if empty, the file's `spooler`. Written when the workers
  see the server from elsewhere, as containers do a host's localhost, or
  hold a key of their own.
- `worker.model`: exactly one of `claude`, `openai`, `gemini`, each with
  `name`, `max_tokens` and `api_key`, a secret. If `api_key` is empty, the
  SDK reads its own variable of the worker's environment, `ANTHROPIC_API_KEY`,
  `OPENAI_API_KEY` or `GEMINI_API_KEY`, which only goroutine workers share
  with the server.
- `worker.max_turns` (default 10), and `worker.task`, a Go text/template
  over the incident, written as itself or as `{file: PATH}` the worker
  reads; if empty, the built-in `cmd/pagedog/task.md`.
- `worker.tools`: `prometheus` with `url`; `loki` with `url`; `mcp`, a list
  of servers, each a `name` and either `url` with `auth`, a `bearer` token
  written as where it comes from, and `headers`, written as themselves or,
  for a secret, as where it comes from, or `command` with `args` and `env`,
  a program the worker runs and talks to over its standard streams, given
  only the variables named; and `tools`, which keeps the model's menu short. Any may be left out; a tool the model has not got it
  cannot call.
- `worker.report`: one of `fs` with `dir`, files in a directory, or
  `github` with `owner`, `repo` and `token`, a secret, each report an issue
  of the repository labelled pagedog, the same incident updating its issue.
  The notifiers read the report back from either.
- `notify`: a list, several of a kind fine: `incidentio` with `api_key` and
  `url`, an update on the incident; `pagerduty` with `api_key`, `from` and
  `url`, a note on the incident; `log`; `webhook` with `url` and `token`,
  JSON with the incident and the report; `slack` with `url`, an incoming
  webhook whose URL is a secret.

The least file that runs for real: incident.io at both ends, workers as
goroutines of the server, the reports kept as files.

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

`demo/local.yaml` is the file for one machine: the in-memory Spooler on
localhost, Alertmanager as the source, workers as child processes reading
the same file. The reports are files the workers write and the server reads back, so a second
pagedog instance needs the same directory mounted.

Secrets are never written as values. Each is where it comes from:
`{env: NAME}`, `{file: PATH}` or `{exec: {cmd: COMMAND, args: [...]}}`. A
file that holds a secret as a value is refused. The variable names are the
user's; the examples use `SPOOLER_API_KEY`, the Spooler CLI's name,
`INCIDENTIO_SIGNING_SECRET`, `INCIDENTIO_API_KEY` and the model's. Map them
to the platform: a Secret and `secretKeyRef` on Kubernetes, `-e NAME` from
the shell with `docker run`, `LoadCredential=` or `LoadCredentialEncrypted=`
under systemd with `{file: /run/credentials/pagedog.service/NAME}`, a
`Secret=` line in a Quadlet unit.

An MCP server's `tools` list keeps the model's menu short; if empty, the
model gets every tool the server has, which for a server that can write is
the way to keep the model reading:

```yaml
worker:
  tools:
    mcp:
      - name: example
        url: https://mcp.example.com/mcp
        auth:
          bearer: {env: EXAMPLE_TOKEN}
        headers:
          X-Tenant: acme
        tools:
          - search_logs
          - query_metrics
```

The task the model gets is a Go text template over the incident,
`cmd/pagedog/task.md` by default. `worker.task` holds another: written in the
file itself, which needs no mount anywhere, or as `{file: PATH}` the worker
reads, so it has to be where the workers run.

## The Spooler instance: ask which

Pagedog runs against one of two, and the choice shapes the deployment, so
ask the user which before writing anything:

- **Hosted Spooler**: `spooler.host: api.spooler.sh`, the API key from
  https://console.spooler.sh. Nothing to run.
- **The in-memory image, `spoolersh/memspoolerd`, on the user's own
  machines**: the same API, no account, any bearer token authenticates,
  nothing persists across a restart. Per https://docs.spooler.sh/local it
  is free to run for your own purposes, in development, tests and
  production alike, and may not be provided to others or hosted as a
  service. Run it next to pagedog, bind its port to pagedog only, and set
  `spooler.host` to it with `no_tls: true` and any key, `local` by custom.
  Declare the two queues to it in files mounted at `/etc/spooler/queues.d`,
  as the demo's `demo/queues.d` does, so they exist on every start; that
  page documents the files.

Either way the two queues named by `spooler.incidents` and
`spooler.reports` exist before pagedog starts: pagedog creates none, and
refuses to start while one is missing. In-memory, that is the image's
`docker run` with the queues declared, and the Spooler quickstart's two
variables pointing at it:

```sh
docker run -d --rm -p 127.0.0.1:8080:8080 \
  -v ./demo/queues.d:/etc/spooler/queues.d:ro spoolersh/memspoolerd
export SPOOLER_API=http://127.0.0.1:8080/v1
export SPOOLER_KEY=local
```

Hosted, `SPOOLER_API` is `https://api.spooler.sh/v1` and `SPOOLER_KEY` the
API key, and the queues are made once, over the API or with the Spooler
CLI, https://docs.spooler.sh/cli, the easier way to make queues and look at
what is on them:

```sh
curl -X PUT "$SPOOLER_API/spools/default/queues/incidents" \
  -H "Authorization: Bearer $SPOOLER_KEY" \
  --json '{"leaseTimeoutSeconds": 60, "maxRetries": 3, "dedupWindowSeconds": 300}'
curl -X PUT "$SPOOLER_API/spools/default/queues/reports" \
  -H "Authorization: Bearer $SPOOLER_KEY" \
  --json '{"leaseTimeoutSeconds": 60, "maxRetries": 3, "dedupWindowSeconds": 300}'
```

Of the settings, the dedup window makes a repeated notification one
incident, the lease timeout is how soon a dead worker's incident comes
back, and max retries bounds what one that keeps failing costs in model
calls; https://docs.spooler.sh/operations/createQueue has the rest.

## What a platform provides

- **A directory that survives a restart** for the `fs` report store: a
  report's text waits there while its message waits on the queue, and a
  restart in between must find it. A PersistentVolumeClaim, a named volume,
  `StateDirectory=` under systemd. With workers as goroutines, the one
  process writes and reads it; with exec or docker workers, the workers
  write it and the server reads it, so both mount the same directory. The
  `github` store needs nothing of the kind.
- **An HTTPS route to the webhook port** for the source to reach, at
  `https://<host>/webhook/<source>`. The route is the platform's: an
  Ingress, a reverse proxy, a tunnel; pagedog itself speaks plain HTTP on
  `webhook.listen`.
- **The model's key and the other secrets** in the environment or files of
  the process that reads each: the server's for its sections, a worker's
  for `worker`, which with goroutine workers is the same process.
- **Reachability** of Prometheus, Loki and any MCP server from where the
  workers run.

## The worker runtime

If `workers.runtime` is empty, workers are goroutines of the server's process:
one process, one container, no privileges, the workers sharing the server's
environment, so the model's `api_key` may be left to the SDK's variable there.
It is the right choice on Kubernetes, under systemd, and anywhere a single
process is wanted; `workers.max` bounds the workers whatever the runtime, three
with no `workers` section at all.

`exec` runs each worker as the child process `path` and `args` name, and
`docker` as a container of `image` on the engine the socket points to, running
`command` or the image's own. Either way the worker reads the same file: a
child process by the path in `args`, a container by a bind or by `files`, which
the server copies in before the container starts, as the compose demo must with
no host path to bind. `worker.spooler` says how the workers reach the Spooler
when that differs from the server's way, as `host.docker.internal` does from
`127.0.0.1`. Both set the worker's whole environment under `env`, which is
where the worker's `{env: NAME}` secrets, the Spooler key and the model's key
among them, are read from: name there every variable the file's `worker`
section names. Pick them only when the user wants workers that can be seen and
killed one at a time, as the demo does. The docker runtime needs the engine
socket mounted and the network and volume names the workers join, as
`demo/pagedog.yaml` shows; it is not for Kubernetes, and no Kubernetes Job
runtime exists.

## Deployment

Run this as a wizard: one question at a time, each with its choices and its
default, an answer taken as given. Do not ask what an earlier answer settled,
and do not assume incident.io or Claude; they are only the examples in the
least file above. When the questions are done, write everything in one go.

1. **Platform.** Kubernetes, systemd with the binary, Quadlet (Podman under
   systemd), Docker Compose, plain `docker run`, or another, named. This
   decides how the secrets, the persistent directory, the route and a Spooler
   container are expressed.
2. **Spooler.** The hosted one, with the API key from the console, or the
   in-memory image on the user's own machines, free to run for their own
   purposes per the local docs page: then a second container or unit next to
   pagedog, the queues declared in `queues.d`, `no_tls: true`, any key. The
   Spooler section above has both.
3. **Pages.** Alertmanager, incident.io, PagerDuty; more than one is fine. Each
   wants its secret, `token`, `signing_secret` or `secret`, and gives the user
   something to set on the pager's side: the URL
   `https://<host>/webhook/<source>` and that secret.
4. **Reports.** log; webhook, a URL and a token if the receiver wants one;
   Slack, an incoming webhook URL, which is a secret; incident.io, an API key,
   for incidents from incident.io only; PagerDuty, an API key and the `from`
   user, for incidents from PagerDuty only. Several are fine.
5. **Model.** Claude, OpenAI or Gemini, and the model name when not the default
   `pagedog.yaml` shows; the key comes under the SDK's variable.
6. **Tools.** Prometheus and its URL; Loki and its URL; MCP servers, for each
   the URL, its bearer token and any headers, or the command, its arguments and
   the variables it needs, and the tools to expose. A command server must be in
   the worker's image: with goroutine workers that is pagedog's own image,
   which holds nothing but pagedog, so it means a derived image, or the exec
   runtime on a host that has the program.
7. **Workers.** How many at most, default 3, and the runtime: goroutines,
   unless the user wants workers visible and killable one at a time, then exec
   or docker as the runtime section says. On Kubernetes, goroutines.
8. **Where things live.** The reports: the `fs` store's directory, default
   `/var/lib/pagedog/reports`, on storage that survives a restart, or the
   `github` store, a repository and a token, which needs nothing from the
   platform and suits Kubernetes; the host or URL the pager will call, and what
   routes HTTPS to the webhook port; where the secrets come from, the
   platform's secret store, files, or a secret manager through `{exec}`.

Then write:

- The configuration file, from `pagedog.yaml`'s keys and the answers, every
  secret as a reference, placed where `--config` points.
- The platform's pieces: the secrets, the persistent directory, the route or
  what to put in front, the Spooler container with its `queues.d` or the
  one-time queue creation, and pagedog itself as the reference run in the
  binary section maps onto the platform.
- What the user sets on the pager's side and the notifier's side: the webhook
  URL and its secret, the API key and its scope.
- How to see it work: the start log line `listening incoming webhooks`; a queue
  missing fails the start; a report arriving at the notifier closes the loop.
  The incident.io notifier posts a report once however many times the queue
  delivers it, under the incident's key; PagerDuty notes have no such key, so a
  report delivered twice is two notes. Without Prometheus and Loki reachable,
  the model's tools fail and it says so in its report.
