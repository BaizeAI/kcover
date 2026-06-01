# kcover - Kubernetes Coverage for Fault Awareness and Recovery

Welcome to `kcover`, a Kubernetes solution designed to enhance the reliability and resilience of large-scale AI workloads by providing fault awareness and robust instant recovery mechanisms.

## Features

- **Fault Awareness**: Detect and respond to hardware, network, and software failures dynamically.
- **Instant Recovery**: Quickly restore operations without manual intervention, minimizing downtime and ensuring continuous training and service availability.
- **Scalability**: Designed for large-scale environments, handling complexities of distributed AI workloads.

## Getting Started

### Prerequisites

Ensure you have Kubernetes and Helm installed on your cluster. `kcover` is compatible with Kubernetes versions 1.19 and above.

### Installation

Install `kcover` using Helm:

```shell
helm repo add baizeai https://baizeai.github.io/charts
helm install kcover baizeai/kcover --namespace kcover-system --create-namespace
```

### Configuration

Configure `kcover` to monitor specific Kubernetes resources by labeling them:

```shell
kubectl label pytorchjobs <job-name> kcover.io/cascading-recovery=true
kubectl label pytorchjobs <job-name> kcover.io/need-recovery=true
```

`kcover` and `agent` read the current node name from the `NODE_NAME`
environment variable. Helm templates inject this automatically from
`spec.nodeName`. The legacy `FAST_RECOVERY_NODE_NAME` variable is still read in
code for backward compatibility during migration, but new deployments should use
`NODE_NAME` only.

## Agent Config

The agent supports loading its runtime configuration from a YAML file mounted
from a ConfigMap. The Helm chart creates a default ConfigMap automatically, and
you can also point the agent to an existing user-managed ConfigMap.

The only runtime flag kept by the agent is `--config`, which points to the
mounted configuration file. Business settings such as `interval`, `vendor`, and
all `metaX` thresholds are now read from the config file only.

Default chart-managed config:

```yaml
agent:
  config:
    data:
      vendor: 1
      interval: 5
      metaX:
        hcaIDs:
          - mlx5_0
          - mlx5_1
        day2CheckTime: "10:00"
        gpuNum: 8
        temperature: 85
        eccMaxCount: 64
        ntpMaxOffsetMillis: 10
```

The default vendor is Nvidia (`vendor: 1`). To switch the agent to MetaX,
set `agent.config.data.vendor` to `2`. MetaX-specific day2 checks and
preflight report collection are enabled automatically for the MetaX vendor.

Install with MetaX enabled:

```shell
helm install kcover baizeai/kcover \
  --namespace kcover-system \
  --create-namespace \
  --set agent.config.data.vendor=2
```

Switch an existing release to MetaX:

```shell
helm upgrade kcover baizeai/kcover \
  --namespace kcover-system \
  --reuse-values \
  --set agent.config.data.vendor=2
```

If your MetaX nodes require HCA checks, set the HCA IDs as chart values too:

```shell
helm upgrade kcover baizeai/kcover \
  --namespace kcover-system \
  --reuse-values \
  --set agent.config.data.vendor=2 \
  --set-json 'agent.config.data.metaX.hcaIDs=["mlx5_0","mlx5_1"]'
```

If `metaX.hcaIDs` is set, the agent runs `ibv_devinfo` and requires every
listed `hca_id` to have `state: PORT_ACTIVE (...)`.

Use a user-defined ConfigMap:

```yaml
agent:
  config:
    existingConfigMap: my-agent-config
    path: /etc/kcover-agent/config.yaml
```

## Usage

Once installed, `kcover` will automatically monitor the labeled resources for any signs of failures and perform recovery actions as specified in the configuration.

## Preflight Slow Node Detection

- The collector expects one preflight report per node.
- `workload_size` is required in the report so the manager can determine the
  expected report count and batch count.
- Each report must contain exactly `min(workload_size - 1, 5)` logical batch
  slots, although fail-fast nodes may skip pairwise batch parsing entirely.
- For the common 16-node topology, this usually means 16 reports and 15
  possible pairings, but the current manager-side aggregation only consumes up
  to 5 batches per report.
- Nodes that fail `gpu_check` or `storage_check` are marked abnormal directly
  and excluded from pairwise slow-node intersection.
- Pairwise slow-node detection marks a node as slow only when its node IP
  appears in failed observations across every effective batch considered by the
  aggregation logic.
- Agent-side node events carry a compacted preflight payload rather than the
  raw host report. The compacted payload keeps only manager-required fields:
  report identity plus per-batch `batch_idx`, `pair`, `self_ip`, `status`, and
  performance fields needed for bus-bandwidth threshold evaluation.
- Incomplete report collections no longer wait forever. The controller expires
  stale job aggregations after the controller flag
  `--preflight-report-collection-timeout` and emits a warning event describing
  how many reports were received.

Supported compacted report threshold field:

```yaml
node_check_busbw_threshold_gbps: "5"
```

Controller timeout example:

```yaml
controller:
  args:
    - --preflight-report-collection-timeout=30m
```

Controller leader election can also be toggled from chart values. Keep it
enabled for multi-replica or HA deployments. Disable it only when you want a
single controller instance to bypass Lease lock acquisition.

```yaml
controller:
  leaderElection:
    enabled: false
```

## Image Build Notes

The MetaX utility `mx-smi` is extracted into a dedicated image so that the
agent image no longer needs to reference the full `maca-pytorch` runtime
directly.

- Extracted image: `ghcr.io/baizeai/mx-smi:v0.2`
- Agent base runtime: `ubuntu:24.04`
- Agent build arg: `MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2`

Build and push the extracted `mx-smi` image:

```shell
make image-mx-smi
```

Build and push the agent image with the extracted `mx-smi` image injected:

```shell
make image-agent
```

If you need to build manually, use:

```shell
docker build -f docker/mx-smi.Dockerfile -t ghcr.io/baizeai/mx-smi:v0.2 .
docker build -f docker/agent.Dockerfile --build-arg MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2 -t ghcr.io/baizeai/kcover-agent:<tag> .
```
