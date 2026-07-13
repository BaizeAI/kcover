# kcover - Kubernetes Coverage for Fault Awareness and Recovery

Welcome to `kcover`, a Kubernetes solution designed to enhance the reliability and resilience of large-scale AI workloads by providing fault awareness and robust instant recovery mechanisms.

## Features

- **Fault Awareness**: Detect and respond to hardware, network, and software failures dynamically.
- **Instant Recovery**: Quickly restore operations without manual intervention, minimizing downtime and ensuring continuous training and service availability.
- **Scalability**: Designed for large-scale environments, handling complexities of distributed AI workloads.

## Getting Started

### Prerequisites

Ensure you have Kubernetes and Helm installed on your cluster. `kcover` is compatible with Kubernetes versions 1.19 and above.

For local builds and tests, the repository now uses `go 1.25` with
`toolchain go1.26.5`. The toolchain bump is part of the current CVE
remediation for the agent dependency stack.

### Installation

Install `kcover` using Helm:

```shell
helm repo add baizeai https://baizeai.github.io/charts
helm install kcover baizeai/kcover --version 0.11.0 --namespace kcover-system --create-namespace
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
mounted configuration file. Business settings such as `interval` are always
read from the config file. MetaX-specific settings are parsed only by the
`kcover-agent-metax` image.

The chart always renders the same inline config structure under
`agent.config.data`. The generic image ignores the optional `metaX` block,
while the MetaX image consumes it.

The chart uses `agent.flavor` to choose the agent image flavor. The default is
`base`, which selects the generic image. Setting `agent.flavor=metax` selects
the MetaX image and enables MetaX-only host integrations such as
`/dev/infiniband` and `/etc/localtime`. `agent.image.repository` remains
available as an advanced override when you need a custom image repository.

Default chart-managed config:

```yaml
agent:
  config:
    data:
      interval: 5
```

`kcover-agent` is the default generic image and should also be treated as the
replacement for the old Nvidia-only path. It is published as a multi-arch
image and keeps common code paths such as preflight report collection, while
MetaX-specific checks fall back to no-op.

`kcover-agent-metax` adds the MetaX-specific day2 tooling and checks. The day2
clock check is currently disabled and is therefore not exposed in the chart
values.

Install or update the default generic release:

```shell
helm install kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --create-namespace

helm upgrade kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --reuse-values
```

Install or update the MetaX release:

```shell
helm install kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --create-namespace \
  --set agent.flavor=metax

helm upgrade kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --reuse-values \
  --set agent.flavor=metax
```

If your MetaX nodes require HCA checks, set the HCA IDs as chart values too:

```shell
helm upgrade kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --reuse-values \
  --set agent.flavor=metax \
  --set-json 'agent.config.data.metaX.hcaIDs=["mlx5_0","mlx5_1"]'
```

Example MetaX-specific config:

```yaml
agent:
  flavor: metax
  config:
    data:
      interval: 5
      metaX:
        hcaIDs:
          - mlx5_0
          - mlx5_1
        day2CheckTime: "10:00"
        gpuNum: 8
        temperature: 85
        eccMaxCount: 64
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
- Agents store each compacted payload as a namespaced `PreflightReport`
  (`kcover.io/v1alpha1`) owned by the source Pod. Kubernetes Events contain
  only a short human-readable notification and are not used to transport the
  report. Reports are grouped by workload UID, so reusing a workload name does
  not combine different workload runs. Agents retry transient report creation
  failures with capped exponential backoff. The compacted payload keeps only manager-required fields:
  report identity plus per-batch `batch_idx`, `pair`, `self_ip`, `status`, and
  performance fields needed for bus-bandwidth threshold evaluation.
- Incomplete report collections no longer wait forever. The controller expires
  stale job aggregations after the controller flag
  `--preflight-report-collection-timeout` and logs a warning describing
  how many reports were received.

Inspect current reports with:

```shell
kubectl get preflightreports -A
```

Supported compacted report threshold field:

```yaml
node_check_busbw_threshold_gbps: "0"
```

The default is `0`, which records bus bandwidth without marking a batch slow
based on bandwidth. Set a positive value to enable threshold evaluation.

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
MetaX agent image no longer needs to reference the full `maca-pytorch` runtime
directly.

- Extracted image: `ghcr.io/baizeai/mx-smi:v0.2`
- Generic agent image: `ghcr.io/baizeai/kcover-agent`
- MetaX agent image: `ghcr.io/baizeai/kcover-agent-metax`
- Generic agent platforms: `linux/amd64,linux/arm64`
- MetaX agent platforms: `linux/amd64`
- MetaX build arg: `MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2`

Build and push the extracted `mx-smi` image:

```shell
make image-mx-smi
```

Build and push the default generic agent image:

```shell
make image-agent
```

Build and push the MetaX agent image:

```shell
make image-agent-metax
```

If you need to build manually, use:

```shell
docker build -f docker/mx-smi.Dockerfile -t ghcr.io/baizeai/mx-smi:v0.2 .
docker buildx build -f docker/agent.Dockerfile --platform linux/amd64,linux/arm64 -t ghcr.io/baizeai/kcover-agent:v0.11.0 .
docker build -f docker/agent-metax.Dockerfile --build-arg MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2 -t ghcr.io/baizeai/kcover-agent-metax:v0.11.0 .
```
