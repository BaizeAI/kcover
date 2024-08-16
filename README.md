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
```

## Usage

Once installed, `kcover` will automatically monitor the labeled resources for any signs of failures and perform recovery actions as specified in the configuration.
