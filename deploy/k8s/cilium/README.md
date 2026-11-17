# deploy/k8s/cilium — Cilium + eBPF network security & observability

Cilium is the platform's CNI, network-policy engine, flow-observability layer
(Hubble), transparent-encryption layer (WireGuard), and runtime-enforcement
layer (Tetragon). **Why and how: [docs/CILIUM-EBPF.md](../../../docs/CILIUM-EBPF.md).**

| File | Contents |
|---|---|
| `values.yaml` | Cilium Helm values: kube-proxy replacement, eBPF host routing, Hubble (relay+UI+metrics+flow export), WireGuard encryption, bandwidth manager, Prometheus ServiceMonitors, EKS IPAM (kubernetes default, ENI alternative commented). |
| `network-policies.yaml` | Zero-trust CiliumNetworkPolicies for namespace `pfaengine`: default-deny ingress+egress, per-service L3/L4 allows, L7 HTTP rules on the ML/eKYC paths, FQDN-pinned vendor egress. |
| `tetragon.yaml` | Tetragon `TracingPolicyNamespaced` runtime policies: exec allowlist (Sigkill), sensitive-file write watch, data-tier egress detection, container-escape syscall alerts. |
| `kustomization.yaml` | Overlay: `../base.yaml` + the two policy files. |

## Quick start (EKS)

```bash
# 1. Install Cilium as the CNI (replaces aws-node + kube-proxy; runbook in
#    docs/CILIUM-EBPF.md — do this on cluster bootstrap, not on a live cluster).
helm repo add cilium https://helm.cilium.io/
helm install cilium cilium/cilium --version 1.16.5 \
  -n kube-system -f deploy/k8s/cilium/values.yaml \
  --set k8sServiceHost=<cluster-endpoint-host> --set k8sServicePort=443

# 2. Validate the dataplane.
cilium status --wait
cilium connectivity test

# 3. Apply workloads + policies (base.yaml is untouched by this overlay).
kubectl apply -k deploy/k8s/cilium/

# 4. Watch the verdicts.
hubble observe --namespace pfaengine --verdict DROPPED
```

Labels: every selector matches `app: <service>` exactly as in
`deploy/k8s/base.yaml`; ports mirror `deploy/docker-compose.yml`.
