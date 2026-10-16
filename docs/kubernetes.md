# OpenKitty on Linux and Kubernetes

OpenKitty's AgentOS API can run as a database-backed task service. The HTTP
gateway and task workers coordinate through the existing MySQL/SQLite database;
Redis, RabbitMQ, and a load-aware Ingress are not required.

## Architecture

```text
client
  |
Ingress -> openkitty-gateway Pods -> shared MySQL task table
                                      |
                                openkitty-worker Pods
                                      |
                             shared workspace / object storage
```

Ingress only balances HTTP requests across ready gateway Pods. It does not need
to know which worker is busy. A worker with an available concurrency slot
atomically claims the next database task and renews its lease while it runs.

## Runtime modes

Set `gateway.agent_os_mode` in YAML or `OPENKITTY_AGENT_OS_MODE` in the Pod:

| Mode | Accept API submissions | Execute queued tasks | Typical use |
|------|------------------------|----------------------|-------------|
| `all` | Yes | Yes | One Deployment or a single Linux service |
| `gateway` | Yes | No | Stateless HTTP/API Deployment |
| `worker` | No | Yes | Independently scalable worker Deployment |

Queue settings:

```yaml
gateway:
  agent_os_mode: all
  max_tasks: 4
  max_queued_tasks: 100
  task_lease_seconds: 60
  task_poll_interval_ms: 500
```

- `max_tasks` is the real per-instance AgentOS execution limit.
- `max_queued_tasks` is the shared pending-task admission limit. New submissions
  receive HTTP `429` with `Retry-After` when it is reached.
- `task_lease_seconds` controls hard-crash recovery time. A live worker renews
  the lease every few seconds.
- `task_poll_interval_ms` controls how quickly an idle worker notices new work.

The same values can be overridden without rewriting the config file:

```text
OPENKITTY_LISTEN_ADDR
OPENKITTY_WORKSPACE
OPENKITTY_FIXED_PORT
OPENKITTY_HEADLESS
OPENKITTY_DISABLE_HTTPS
OPENKITTY_AGENT_OS_MODE
OPENKITTY_MAX_TASKS
OPENKITTY_MAX_QUEUED_TASKS
OPENKITTY_TASK_LEASE_SECONDS
OPENKITTY_TASK_POLL_INTERVAL_MS
OPENKITTY_INSTANCE_ID
```

## Database choice

### MySQL: multi-Pod production

Use MySQL whenever more than one OpenKitty Pod participates in the queue. The
database and schema must already exist; OpenKitty runs GORM migrations on
startup, so the configured user needs schema migration permissions during an
upgrade.

YAML remains supported:

```yaml
mysql:
  host: mysql.default.svc
  port: "3306"
  user: openkitty
  passwd: change-me
  dbname: openkitty
```

For Kubernetes, prefer Secret-backed environment variables:

```text
OPENKITTY_MYSQL_HOST
OPENKITTY_MYSQL_PORT
OPENKITTY_MYSQL_USER
OPENKITTY_MYSQL_PASSWORD
OPENKITTY_MYSQL_DATABASE
```

### SQLite: single Pod or local Linux service

SQLite is retained for a single OpenKitty process and local development. Put it
on a persistent local/RWO volume with:

```bash
export OPENKITTY_SQLITE_PATH=/var/lib/openkitty/openkitty.db
```

Do not put one SQLite file on an RWX/network filesystem and let multiple Pods
write it concurrently. SQLite file locking and many network storage systems do
not provide the multi-node database guarantees required by the lease queue.

## Kubernetes deployment

1. Build and push the image:

   ```bash
   docker build -t your-registry/openkitty:latest .
   docker push your-registry/openkitty:latest
   ```

2. Create the config Secret. The example manifest sets
   `OPENKITTY_WORKSPACE=/workspace`, so the checked-in workspace value is safely
   overridden inside the Pods:

   ```bash
   kubectl create secret generic openkitty-config \
     --from-file=openkitty-config.yaml=./openkitty-config.yaml
   ```

3. Create the MySQL Secret:

   ```bash
   kubectl create secret generic openkitty-mysql \
     --from-literal=host=mysql.default.svc \
     --from-literal=port=3306 \
     --from-literal=user=openkitty \
     --from-literal=password='replace-me' \
     --from-literal=database=openkitty
   ```

4. Edit the image, hostname, PVC storage class, and resource limits in
   [`deploy/kubernetes/openkitty.yaml`](../deploy/kubernetes/openkitty.yaml),
   then apply it:

   ```bash
   kubectl apply -f deploy/kubernetes/openkitty.yaml
   ```

The example uses two gateway Pods and two worker Pods. Both mount the same RWX
workspace so a task sees the same normalized path regardless of which worker
claims it. S3-compatible artifact storage can replace RWX storage in a future
deployment, but local per-Pod workspaces must not be mixed for one session.

## Probes and load balancing

| Endpoint | Meaning | Kubernetes use |
|----------|---------|----------------|
| `/livez` | The process and HTTP server are alive | `livenessProbe` |
| `/readyz` | Database is reachable and the Pod is not draining | `readinessProbe` |
| `/api/health` | Detailed compatibility health payload and queue statistics | Operations/debugging |
| `/metrics` | Prometheus queue, capacity, admission, and DB gauges | Monitoring/autoscaling |

A busy worker remains ready: busy is not the same as broken. It simply stops
claiming tasks after reaching `max_tasks`. Queue saturation closes task
admission with `429`, while status, cancel, and SSE endpoints remain reachable.
This avoids removing every gateway endpoint when a shared queue becomes full.

NGINX Ingress does not need `least_conn` for task scheduling. AgentOS submission
requests return quickly, so HTTP connection count does not represent background
model/tool load. The example disables proxy buffering and extends read timeout
for SSE; the database queue performs the actual task balancing.

## Failure and shutdown behavior

- Claiming is an atomic conditional database update; two workers cannot win the
  same lease.
- Every claim gets a random lease token. A late result from an old worker is
  fenced and cannot overwrite the current owner.
- A hard-crashed worker's task becomes claimable after its lease expires.
- Cross-Pod cancellation clears the lease in the database. The owning worker
  observes the lost lease, cancels its context, and cannot publish a late result.
- During SIGTERM, readiness closes first. OpenKitty waits for active work until
  the shutdown deadline, then requeues unfinished owned tasks before exiting.
- Successful tool receipts are reused. An uncertain mutating tool call is not
  automatically replayed after a crash; read-only calls may be retried.

This is at-least-once task execution with fenced result publication and durable
tool receipts. It does not claim absolute exactly-once behavior across arbitrary
external systems.

## Scaling

Gateway Pods can scale on CPU or request rate. Worker capacity is approximately:

```text
worker replicas × OPENKITTY_MAX_TASKS
```

The most useful worker autoscaling signal is
`openkitty_agentos_queued_global`. It can be exposed through a Prometheus adapter
or KEDA. Keep a nonzero minimum worker count unless delayed startup is acceptable.

When channels or cron schedules are enabled, run them in a dedicated singleton
deployment or add leader election. The sample sets
`OPENKITTY_DISABLE_CHANNELS=true` and `OPENKITTY_DISABLE_SCHEDULERS=true` on
replicated AgentOS Pods to prevent duplicate consumers and schedules.
