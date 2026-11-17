# Pagedog on Kubernetes

The least deployment: one pagedog pod, the workers as goroutines of it, the
Spooler server in memory next to it, Alertmanager paging from inside the
cluster, Claude investigating with the cluster's Prometheus and Loki, the
reports on a volume. All of it is `deploy.yaml`, pagedog's configuration as a
ConfigMap in it; `pagedog.yaml` at the repository root documents every setting,
and [SKILL.md](../../SKILL.md) what any platform has to provide.

The Secret the configuration's `{env: NAME}` entries read is made by hand,
then the rest applied:

```sh
kubectl create secret generic pagedog \
  --from-literal=ALERTMANAGER_TOKEN=... \
  --from-literal=ANTHROPIC_API_KEY=...

kubectl apply -f examples/k8s/deploy.yaml
```

Then give Alertmanager a receiver for it, with the same token:

```yaml
receivers:
  - name: pagedog
    webhook_configs:
      - url: http://pagedog.<namespace>.svc:7070/webhook/alertmanager
        http_config:
          authorization:
            credentials: <ALERTMANAGER_TOKEN>
```

The Spooler server is `spoolersh/memspoolerd`: in memory, any token
authenticates, nothing persists across its restart, and free to run for your
own purposes per https://docs.spooler.sh/local. The hosted one is a change of
`spooler.host` and a real key in the Secret. A changed configuration is applied
the same way, then `kubectl rollout restart deployment/pagedog`, as a changed
ConfigMap does not restart a pod. The reports are files on the claim, which the
notifiers read back; the `github` report store needs no volume.
