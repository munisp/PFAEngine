# Cilium + eBPF: network security and observability layer

This document is the design rationale and operations runbook for the Cilium
layer in `deploy/k8s/cilium/`. It covers: (a) why Cilium/eBPF for a
PenCom-regulated pension platform, (b) installation on EKS, (c) the
zero-downtime migration path from the current `base.yaml` deployment,
(d) day-2 operations, and (e) an honest list of what eBPF does **not** solve.

---

## (a) Why Cilium/eBPF for PFAEngine

### The problem with the status quo

`deploy/k8s/base.yaml` ships workloads with strong pod-level hygiene
(non-root, read-only rootfs, HPA) but **no network layer at all**: stock EKS
(AWS VPC CNI + kube-proxy/iptables) gives pod-to-pod any-to-any connectivity
with no policy, no flow visibility, and no runtime detection. For a platform
holding RSA balances and member PII for 16 services, that means:

- one compromised pod (e.g. the Python `kyc-service` sidecar with its large
  dependency surface) can reach `postgres:5432` directly;
- vendor-API credentials can be exfiltrated to any IP on the internet;
- there is no evidence trail of "who talked to whom" for auditors;
- iptables-based kube-proxy adds per-packet cost that grows with service and
  endpoint count — the wrong direction for the 1M-RSA scale target (NFR-03).

### Regulatory and architecture drivers, mapped to controls

| Driver | Requirement | Cilium/eBPF control |
|---|---|---|
| **PenCom security expectations** (licensed PFA custody of retirement savings; network segmentation of systems processing member funds/PII) | Demonstrable least-privilege between services; data tier not reachable from internet-adjacent or AI sidecar workloads | Default-deny `CiliumNetworkPolicy` on the whole `pfaengine` namespace; `postgres:5432` and `kafka:9092` accept only the named Go services; `kyc-service` may reach `ollama` and nothing else |
| **NDPA 2023** (data-protection audit trails for personal data processing) | Evidence of where member data flows, incl. cross-border/vendor transfers | Hubble flow logs: every connection (accepted **and** denied) with source/destination identity, exported JSONL per node (`hubble.export.static`) and shippable to the SIEM (Wazuh, AR-09); FQDN-pinned egress proves vendor traffic goes only to `api.termii.com`, `graph.facebook.com`, `www.remita.net`, Smile ID, Dojah |
| **Zero-trust east-west between 16 services** | No implicit trust inside the cluster; per-dependency L3/L4 policy; L7 where it reduces real risk | 19 additive CNPs (see `network-policies.yaml`); L7 HTTP rules restrict member-svc → ml-serve to `POST /score` + `GET /healthz` (model registry and `/reload` are unreachable) and member-svc → kyc-service to the five OCR/face/liveness endpoints |
| **No-sidecar performance at 1M-RSA scale** | Service-mesh features without per-pod proxy CPU/RSS tax or extra hops | eBPF in the kernel: kube-proxy replacement (no iptables), eBPF host routing (`bpf.hostLegacyRouting: false`), Maglev consistent-hash LB, WireGuard transparent encryption in-kernel, bandwidth manager (EDT pacing) — all with zero changes to the Go binaries |
| **Audit evidence / incident forensics** | Flow records and runtime events that stand up in a PenCom examination | Hubble Relay API + `hubble observe`, Prometheus flow/drop/DNS/HTTP metrics with bounded label cardinality, Tetragon JSON events for process/file/network/syscall anomalies |
| **Runtime detection (beyond networking)** | Detect and stop container compromise, not just network misuse | Tetragon: exec allowlist (`/service` + healthcheck wget only, `Sigkill` on violation), sensitive-file write watch, outbound-connect detection on serve-only data/ML pods, ptrace/setns/unshare (container-escape precursor) alerts |

### Why Cilium specifically

- **eBPF, not sidecars**: policy enforcement, load balancing, encryption and
  observability run in the kernel on the node. No per-pod Envoy, no injected
  containers, no latency hop — material at 1M RSAs and 20× HPA scale-out.
- **Identity-based policy**: selectors match `app: <service>` labels (the same
  labels `base.yaml` already uses), not IPs — policies survive pod churn,
  autoscaling and rescheduling.
- **One project, three layers**: CNI + NetworkPolicy + Hubble + Tetragon come
  from the same CRD family (`cilium.io`), one agent, one operational model.
- **CNCF graduated**, the default CNI of GKE Dataplane V2, AKS (Azure CNI
  powered by Cilium) and Alibaba — i.e. the safe, auditable choice a
  regulator's examiner will recognise.

---

## (b) Install runbook (EKS)

> Cilium **replaces** the AWS VPC CNI and kube-proxy. On a **new** cluster
> this is clean; on an **existing** cluster you must drain nodes or rotate
> them — see the migration section. Do not `helm install` onto a live cluster
> running aws-node and expect it to work.

### 1. Create the cluster without the AWS CNI/kube-proxy

```bash
# eksctl: spin up control plane + nodegroup WITHOUT aws-node/kube-proxy
eksctl create cluster -f - <<'EOF'
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata: { name: pfaengine-prod, region: eu-west-1, version: "1.30" }
managedNodeGroups:
  - name: ng-1
    instanceType: m6i.xlarge
    desiredCapacity: 3
    ssh: { allow: false }
    privateNetworking: true
addonsConfig:
  disableDefaultAddons: true          # no aws-node, no kube-proxy, no coredns-yet
EOF

# CoreDNS is still required — install it explicitly once Cilium is up.
```

(Console/SDK equivalent: create the EKS cluster, then
`aws eks delete-addon --addon-name vpc-cni` and `--addon-name kube-proxy`,
and delete the `aws-node` / `kube-proxy` DaemonSets.)

### 2. Install Cilium

```bash
ENDPOINT=$(aws eks describe-cluster --name pfaengine-prod \
  --query 'cluster.endpoint' --output text | sed 's|https://||')

helm repo add cilium https://helm.cilium.io/ && helm repo update
helm install cilium cilium/cilium --version 1.16.5 \
  --namespace kube-system \
  --values deploy/k8s/cilium/values.yaml \
  --set k8sServiceHost="$ENDPOINT" \
  --set k8sServicePort=443

# CoreDNS now that a CNI exists:
aws eks create-addon --cluster-name pfaengine-prod --addon-name coredns
```

### 3. Validate

```bash
cilium status --wait                 # all agents healthy, KVStore OK
cilium connectivity test             # spins client/server pods, tests L3/L4,
                                     # L7, DNS, policy verdicts end-to-end
kubectl -n kube-system exec ds/cilium -- hubble observe --last 5
```

### 4. Apply the platform + policies

```bash
kubectl apply -k deploy/k8s/cilium/          # base workloads + CNPs + Tetragon
cilium connectivity test --namespace pfaengine 2>/dev/null || true
hubble observe --namespace pfaengine --verdict DROPPED --follow
```

### 5. Tetragon

Tetragon ships with the Cilium chart (`tetragon.enabled: true` in recent
charts; or standalone: `helm install tetragon cilium/tetragon -n kube-system`).
The policies in `tetragon.yaml` are `TracingPolicyNamespaced` and activate on
`kubectl apply`. Verify:

```bash
kubectl -n pfaengine get tracingpoliciesnamespaced
kubectl -n kube-system logs ds/tetragon -c export-stdout --tail 20 | jq .
```

---

## (c) Migration path from current base.yaml (no app changes)

Cilium is a CNI — **zero application or manifest changes** are required in
`deploy/k8s/base.yaml`. The policies are additive overlays. Roll out in four
phases so a bad selector can never take down the contribution pipeline:

**Phase 0 — Dataplane only.** Install Cilium with
`policyEnforcementMode: "default"` (the shipped `values.yaml` value). In
`default` mode an endpoint enforces policy **only once a policy selects it**,
and — critically — apply `network-policies.yaml` **without** the
`default-deny-all` document first (comment it out or apply the rest
selectively). All allow policies are additive; nothing is denied yet. Watch
`hubble observe --namespace pfaengine` to build the flow inventory and
compare it against the policy set.

**Phase 1 — Audit posture.** Apply `default-deny-all`. Because
`enableDefaultDeny` denies anything not explicitly allowed, monitor for
accidental drops:

```bash
hubble observe --namespace pfaengine --verdict DROPPED --follow
# expect: nothing but noise; every legit flow already has an allow policy
```

If a legitimate flow is dropped, add the allow rule (or temporarily delete
`default-deny-all` — the remaining policies keep working). Keep a one-week
soak in production-like load before Phase 2.

**Phase 2 — Strict encryption.** Flip `encryption.strictMode.enabled: true`
to refuse plaintext fallback for podCIDRs, after confirming
`cilium encrypt status` shows all nodes WireGuard-established.

**Phase 3 — Cluster-wide enforcement.** Set `policyEnforcementMode: "always"`
so that even endpoints no policy selects are default-denied (protects against
a future workload landing in `pfaengine` without a policy). Then enable
`hostFirewall.enabled: true` in a later change window for node-level policy.

Rollback at every phase is `kubectl delete -f` of the policy file (Phase 1+)
or a helm value flip (Phase 0/2/3) — the eBPF dataplane itself never needs to
come down.

---

## (d) Day-2 operations

**Flow retention & audit evidence.** `hubble.export.static` writes JSONL flow
records to `/var/run/cilium/hubble/events.log` on every node (100 MB × 10
rotations). Ship them with the node log agent (Fluent Bit/Vector) into the
Wazuh/SIEM tier (AR-09) with the retention your PenCom examination schedule
requires (recommend ≥ 7 years to match pension record-keeping norms; the
node-local files are a buffer, not the archive). Redact at the shipper if the
SIEM holds data beyond its need — Hubble metadata contains identities, not
payloads, which keeps NDPA exposure low.

**Live triage.**

```bash
hubble observe --namespace pfaengine --verdict DROPPED --follow
hubble observe --from pod pfaengine/member-svc-xxx --protocol http
hubble observe --to-identity ingress --namespace pfaengine --last 20
```

**SIEM export.** Prefer the static file export (above) for the durable trail;
use Hubble Relay's gRPC API or `hubble observe --output jsonpb` streaming for
live dashboards. Tetragon events come from the Tetragon agent log
(`kubectl logs ds/tetragon -c export-stdout`) as JSON — ship identically.

**Policy testing / regression.** `cilium connectivity test` exercises
L3/L4/L7/DNS and policy verdicts; run it after any policy change. For a
single policy, `cilium policy get` / `cilium endpoint list` show the realized
per-endpoint policy, and `cilium policy selectors` shows which identities a
selector currently matches — the fast way to answer "why was this dropped?"
without tcpdump.

**Metrics.** Agent (`:9962`), operator, and Hubble metrics are exposed with
ServiceMonitors (enable the commented `allow-prometheus-scrape` CNP once the
monitoring namespace is fixed). Key alerts: `hubble_drop_total`,
`hubble_dns_total`, `cilium_policy_import_errors_total`, WireGuard handshake
age, Tetragon event rate.

---

## (e) Honest limits — what eBPF does NOT replace

- **Host-level IDS and node hardening.** Cilium secures pod/network flows;
  it is not a host IDS, EDR, or file-integrity monitor. Keep Wazuh (AR-09),
  EBS/IMDS controls, and node AMI patching. Tetragon narrows the gap for
  *container* runtime events but does not watch the node root user.
- **WAF / API-gateway authz.** The L7 HTTP rules here are coarse
  (method+path). They do not replace `internal/httpsrv` authn/authz, rate
  limiting, body limits, or a WAF against injection in request bodies —
  Cilium L7 policy never inspects payload semantics.
- **mTLS and application-layer authorisation.** WireGuard encryption is
  **node-to-node transport encryption**, not workload identity: it proves a
  packet came from a node in the cluster, not that `api-gateway` (vs a
  compromised pod on the same node) called `ledger`. Tenant-scoped authz
  decisions (`X-Tenant-ID`, bearer tokens, maker-checker) remain
  application-layer duties. If cryptographic workload identity becomes a
  requirement, evaluate Cilium's mutual-auth (SPIFFE) or a service mesh as a
  separate tranche.
- **Managed nodegroups without CNI replacement.** Cilium cannot simply
  coexist with the AWS VPC CNI in default mode. On EKS managed nodegroups you
  must follow the CNI replacement steps above (remove `aws-node`/`kube-proxy`
  or use CNI chaining configurations with documented caveats). Bottlerocket,
  Fargate, and some managed-nodegroup configurations are unsupported for
  full kube-proxy replacement — verify per nodegroup before enrolling it.
- **FQDN policy cost.** `toFQDNs` rules depend on DNS interception and have
  per-fqdn IP-set churn; keep the vendor list tight (as shipped) and don't
  point it at CDNs with thousands of rotating IPs.
- **Encryption overhead.** WireGuard costs a few percent CPU per node and one
  encapsulation MTU hop; at the 1M-RSA target, load-test contribution-window
  peaks before enabling `strictMode`.
