# The demo, on Docker Compose

A service that fails on request, the observability stack that notices
(Prometheus, Alertmanager, Loki with Alloy), the Spooler server in memory, and
pagedog investigating with workers it runs as containers of its own image.
Self-contained; the model's key is the one thing from outside:

```sh
export ANTHROPIC_API_KEY=...
docker compose -f examples/docker/compose.yaml up --build
```

Then open http://127.0.0.1:8081 and press 500. `down -v` drops it all.
