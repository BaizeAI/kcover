# kcover - Kubernetes Coverage for Fault Awareness and Recovery

Welcome to `kcover`, a Kubernetes solution designed to enhance the reliability and resilience of large-scale AI workloads by providing fault awareness and robust instant recovery mechanisms.

## Features

- **Fault Awareness**: Detect and respond to hardware, network, and software failures dynamically.
- **Instant Recovery**: Quickly restore operations without manual intervention, minimizing downtime and ensuring continuous training and service availability.
- **Scalability**: Designed for large-scale environments, handling complexities of distributed AI workloads.

## Getting Started

### Prerequisites

Ensure you have Kubernetes and Helm installed on your cluster. `kcover` requires
Kubernetes 1.25 or newer because the `PreflightReport` CRD uses CEL validation.

For local builds and tests, the repository uses `go 1.26` with
`toolchain go1.26.5`. The toolchain bump is part of the current CVE
remediation for the agent dependency stack.

### Installation

Install `kcover` using Helm:

```shell
helm repo add baizeai https://baizeai.github.io/charts
helm install kcover baizeai/kcover --version 0.12.0 --namespace kcover-system --create-namespace
```

### Upgrading from 0.10.x

Version 0.11.0 does not render the 0.10.x values structure. Back up the values
that were explicitly set on the existing release before starting the upgrade:

```shell
helm get values kcover \
  --namespace kcover-system \
  -o yaml > kcover-0.10-values.yaml
```

Helm installs files from a chart's `crds/` directory only during a new install,
so apply the new CRD explicitly before upgrading the release:

```shell
helm show crds baizeai/kcover --version 0.11.0 | kubectl apply -f -
```

Version 0.11.0 splits the agent and controller ServiceAccounts and replaces the
old vendor selector with `agent.flavor`. Create a new values file using the
0.11.0 structure and migrate only settings that still apply:

- Replace `agent.config.data.vendor: 1` with `agent.flavor: base`.
- Replace `agent.config.data.vendor: 2` with `agent.flavor: metax`.
- Do not copy the old top-level `serviceAccount`. The new chart creates separate
  agent and controller ServiceAccounts by default.
- Migrate intentional ServiceAccount names and annotations separately under
  `agent.serviceAccount` and `controller.serviceAccount`.
- Copy intentional custom image, resource, scheduling, and agent configuration
  overrides to their corresponding 0.11.0 fields. Do not copy old default
  security contexts or host volumes.

For example, a MetaX installation with explicit ServiceAccount names can use
the following `kcover-0.11-values.yaml`:

```yaml
agent:
  flavor: metax
  serviceAccount:
    name: kcover-agent
controller:
  serviceAccount:
    name: kcover-controller
```

Render the migrated configuration once before applying it:

```shell
helm upgrade kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --reset-values \
  -f kcover-0.11-values.yaml \
  --dry-run
```

Then perform the upgrade with the same values and without `--dry-run`:

```shell
helm upgrade kcover baizeai/kcover \
  --version 0.11.0 \
  --namespace kcover-system \
  --reset-values \
  -f kcover-0.11-values.yaml
```

For a release with no custom settings, omit `-f`; add
`--set agent.flavor=metax` when upgrading a MetaX deployment. Do not use
`--reuse-values` for the 0.10.x to 0.11.0 upgrade.

### Configuration

Configure `kcover` to monitor specific Kubernetes resources by labeling them:

```shell
kubectl label pytorchjobs <job-name> kcover.io/cascading-recovery=true
kubectl label pytorchjobs <job-name> kcover.io/need-recovery=true
```

Helm injects the current node name from `spec.nodeName` into both the agent and
controller as `NODE_NAME`. The agent also supports the legacy
`FAST_RECOVERY_NODE_NAME` variable during migration. The controller separately
uses the Pod name from `POD_NAME` as its unique leader-election identity.

## Agent Config

The agent supports loading its runtime configuration from a YAML file mounted
from a ConfigMap. The Helm chart creates a default ConfigMap automatically, and
you can also point the agent to an existing user-managed ConfigMap.

The only runtime flag kept by the agent is `--config`, which points to the
mounted configuration file. MetaX-specific settings are parsed only by the
`kcover-agent-metax` image. The legacy `interval` field remains accepted for
configuration compatibility, but no current detector uses it for scheduling.

The chart renders common inline settings from `agent.config.data`. When
`agent.flavor=metax`, it also injects the required MetaX defaults from
`agent.flavors.metax.config`; values explicitly set in `agent.config.data`
override those defaults. The base flavor does not render a `metaX` block.

The chart uses `agent.flavor` to choose the agent image flavor. The default is
`base`, which selects the generic image. Setting `agent.flavor=metax` selects
the MetaX image and enables MetaX-only host integrations such as
`/dev/infiniband` and `/etc/localtime`. `agent.image.repository` remains
available as an advanced override when you need a custom image repository.

Default chart-managed config:

```yaml
agent:
  config:
    data: {}
```

`kcover-agent` is the default generic image and should also be treated as the
replacement for the old Nvidia-only path. It is published as a multi-arch
image and keeps common code paths such as preflight report collection, while
MetaX-specific checks fall back to no-op.

`kcover-agent-metax` adds the MetaX-specific day2 tooling and checks. The day2
clock check is currently disabled and is therefore not exposed in the chart
values.

Install the default generic release, or reset an existing release to the new
generic defaults:

```shell
helm install kcover baizeai/kcover \
  --version 0.12.0 \
  --namespace kcover-system \
  --create-namespace

helm upgrade kcover baizeai/kcover \
  --version 0.12.0 \
  --namespace kcover-system \
  --reset-values
```

Install the MetaX release, or reset an existing release to the new MetaX
defaults:

```shell
helm install kcover baizeai/kcover \
  --version 0.12.0 \
  --namespace kcover-system \
  --create-namespace \
  --set agent.flavor=metax

helm upgrade kcover baizeai/kcover \
  --version 0.12.0 \
  --namespace kcover-system \
  --reset-values \
  --set agent.flavor=metax
```

Setting `agent.flavor=metax` also makes the agent container privileged so it can
access the required MetaX devices. The controller remains non-privileged and
uses a separate ServiceAccount from the agent.

If your MetaX nodes require HCA checks, set the HCA IDs as chart values too:

```shell
helm upgrade kcover baizeai/kcover \
  --version 0.12.0 \
  --namespace kcover-system \
  --reuse-values \
  --set agent.flavor=metax \
  --set-json 'agent.flavors.metax.config.metaX.hcaIDs=["mlx5_0","mlx5_1"]'
```

Example MetaX-specific config:

```yaml
agent:
  flavor: metax
  flavors:
    metax:
      config:
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
docker buildx build -f docker/agent.Dockerfile --platform linux/amd64,linux/arm64 -t ghcr.io/baizeai/kcover-agent:v0.12.0 .
docker build -f docker/agent-metax.Dockerfile --build-arg MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2 -t ghcr.io/baizeai/kcover-agent-metax:v0.12.0 .
```
