---
name: k8s-best-practices
description: Avoid common Kubernetes mistakes — resource limits, probe configuration, selector mismatches, RBAC pitfalls.
---

# Kubernetes Best Practices

## Resource Management
- `requests` = guaranteed minimum — scheduler uses this for placement
- `limits` = maximum allowed — exceeding memory = OOMKilled, CPU = throttled
- No limits = can consume entire node — always set production limits
- `requests` without `limits` = burstable — can use more if available

## Probes
- `readinessProbe` controls traffic — fails = removed from Service endpoints
- `livenessProbe` restarts container — fails = container killed and restarted
- `startupProbe` for slow starts — disables liveness/readiness until success
- Don't use same endpoint for liveness and readiness

## Probe Pitfalls
- Liveness probe checking dependencies — if DB down, all pods restart
- Readiness probe too aggressive — minor latency spike removes all pods
- No startup probe on slow-starting app — gets killed before ready
- Probe timeout too short — Java/Node apps need 10-30s

## Deployment Stability
- `PodDisruptionBudget` prevents all pods going down during node drain
- `podAntiAffinity` spreads replicas across nodes/availability zones
- `topologySpreadConstraints` for even distribution
- `terminationGracePeriodSeconds` long enough for cleanup (default 30s)

## Security
- Never run as root — `securityContext.runAsNonRoot: true`
- `readOnlyRootFilesystem` for stateless workloads
- `NetworkPolicy` to restrict pod-to-pod traffic
- RBAC: least privilege — don't use `cluster-admin` for apps

## When Asked About K8s Issues
1. Check pod status: `kubectl describe pod <name>`
2. Check events: `kubectl get events --sort-by='.lastTimestamp'`
3. Check logs: `kubectl logs <pod> --tail=100`
4. Check resources: `kubectl top pods`
5. Check node health: `kubectl describe node <name>`
