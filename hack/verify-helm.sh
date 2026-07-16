#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
chart="${repo_root}/manifests/kcover"
workdir=$(mktemp -d)
trap 'rm -rf "${workdir}"' EXIT

fail() {
  echo "helm verification failed: $*" >&2
  exit 1
}

assert_contains() {
  local file=$1
  local value=$2
  grep -Fq -- "${value}" "${file}" || fail "${file} does not contain ${value}"
}

assert_not_contains() {
  local file=$1
  local value=$2
  if grep -Fq -- "${value}" "${file}"; then
    fail "${file} unexpectedly contains ${value}"
  fi
}

assert_count() {
  local file=$1
  local pattern=$2
  local expected=$3
  local actual
  actual=$(grep -Ec -- "${pattern}" "${file}" || true)
  [[ "${actual}" == "${expected}" ]] || fail "${file} matched ${pattern} ${actual} times, expected ${expected}"
}

helm lint "${chart}"

if helm template kcover "${chart}" --kube-version 1.24.0 >"${workdir}/unsupported-kubernetes.yaml" 2>&1; then
  fail "chart unexpectedly rendered for unsupported Kubernetes 1.24"
fi

helm show crds "${chart}" >"${workdir}/crds.yaml"
assert_contains "${workdir}/crds.yaml" 'kind: CustomResourceDefinition'
assert_contains "${workdir}/crds.yaml" 'name: preflightreports.kcover.io'

helm template kcover "${chart}" --kube-version 1.25.0 >"${workdir}/default.yaml"
assert_count "${workdir}/default.yaml" '^kind: ServiceAccount$' 2
assert_contains "${workdir}/default.yaml" 'serviceAccountName: kcover-agent'
assert_contains "${workdir}/default.yaml" 'serviceAccountName: kcover-controller'
assert_contains "${workdir}/default.yaml" 'name: POD_NAME'
assert_contains "${workdir}/default.yaml" 'name: NODE_NAME'
assert_contains "${workdir}/default.yaml" 'fieldPath: metadata.name'
assert_contains "${workdir}/default.yaml" 'fieldPath: spec.nodeName'
assert_contains "${workdir}/default.yaml" 'privileged: false'
assert_not_contains "${workdir}/default.yaml" 'verbs: ["*"]'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --show-only templates/daemonset.yaml >"${workdir}/default-agent.yaml"
assert_not_contains "${workdir}/default-agent.yaml" 'nodeSelector:'
assert_not_contains "${workdir}/default-agent.yaml" 'affinity:'
assert_not_contains "${workdir}/default-agent.yaml" 'tolerations:'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --show-only templates/deployment.yaml >"${workdir}/default-controller.yaml"
assert_not_contains "${workdir}/default-controller.yaml" 'nodeSelector:'
assert_not_contains "${workdir}/default-controller.yaml" 'affinity:'
assert_not_contains "${workdir}/default-controller.yaml" 'tolerations:'

cat >"${workdir}/workload-values.yaml" <<'EOF'
agent:
  podLabels:
    app: intentionally-wrong-agent
    test.kcover.io/agent-label: agent-value
  nodeSelector:
    test.kcover.io/agent-node: agent-pool
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: test.kcover.io/agent-affinity
                operator: Exists
  tolerations:
    - key: test.kcover.io/agent-toleration
      operator: Exists
      effect: NoSchedule
controller:
  podLabels:
    app: intentionally-wrong-controller
    test.kcover.io/controller-label: controller-value
  nodeSelector:
    test.kcover.io/controller-node: controller-pool
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: test.kcover.io/controller-affinity
                operator: Exists
  tolerations:
    - key: test.kcover.io/controller-toleration
      operator: Exists
      effect: NoSchedule
EOF

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --values "${workdir}/workload-values.yaml" \
  --show-only templates/daemonset.yaml >"${workdir}/custom-agent.yaml"
assert_contains "${workdir}/custom-agent.yaml" 'test.kcover.io/agent-label: agent-value'
assert_not_contains "${workdir}/custom-agent.yaml" 'intentionally-wrong-agent'
assert_count "${workdir}/custom-agent.yaml" '^[[:space:]]+app: kcover-agent$' 2
assert_contains "${workdir}/custom-agent.yaml" 'nodeSelector:'
assert_contains "${workdir}/custom-agent.yaml" 'affinity:'
assert_contains "${workdir}/custom-agent.yaml" 'tolerations:'
assert_contains "${workdir}/custom-agent.yaml" 'test.kcover.io/agent-node: agent-pool'
assert_contains "${workdir}/custom-agent.yaml" 'key: test.kcover.io/agent-affinity'
assert_contains "${workdir}/custom-agent.yaml" 'key: test.kcover.io/agent-toleration'
assert_not_contains "${workdir}/custom-agent.yaml" 'test.kcover.io/controller-'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --values "${workdir}/workload-values.yaml" \
  --show-only templates/deployment.yaml >"${workdir}/custom-controller.yaml"
assert_contains "${workdir}/custom-controller.yaml" 'test.kcover.io/controller-label: controller-value'
assert_not_contains "${workdir}/custom-controller.yaml" 'intentionally-wrong-controller'
assert_count "${workdir}/custom-controller.yaml" '^[[:space:]]+app: kcover-controller$' 2
assert_contains "${workdir}/custom-controller.yaml" 'nodeSelector:'
assert_contains "${workdir}/custom-controller.yaml" 'affinity:'
assert_contains "${workdir}/custom-controller.yaml" 'tolerations:'
assert_contains "${workdir}/custom-controller.yaml" 'test.kcover.io/controller-node: controller-pool'
assert_contains "${workdir}/custom-controller.yaml" 'key: test.kcover.io/controller-affinity'
assert_contains "${workdir}/custom-controller.yaml" 'key: test.kcover.io/controller-toleration'
assert_not_contains "${workdir}/custom-controller.yaml" 'test.kcover.io/agent-'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --set agent.flavor=metax >"${workdir}/metax.yaml"
assert_contains "${workdir}/metax.yaml" 'image: ghcr.io/baizeai/kcover-agent-metax:'
assert_contains "${workdir}/metax.yaml" 'mountPath: /dev/infiniband'
assert_contains "${workdir}/metax.yaml" 'privileged: true'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --set agent.flavor=metax \
  --set agent.flavors.metax.securityContext.privileged=false >"${workdir}/metax-unprivileged.yaml"
assert_contains "${workdir}/metax-unprivileged.yaml" 'privileged: false'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --set agent.serviceAccount.name=custom-agent \
  --set controller.serviceAccount.name=custom-controller >"${workdir}/custom-serviceaccounts.yaml"
assert_count "${workdir}/custom-serviceaccounts.yaml" '^kind: ServiceAccount$' 2
assert_contains "${workdir}/custom-serviceaccounts.yaml" 'serviceAccountName: custom-agent'
assert_contains "${workdir}/custom-serviceaccounts.yaml" 'serviceAccountName: custom-controller'
assert_contains "${workdir}/custom-serviceaccounts.yaml" 'name: custom-agent'
assert_contains "${workdir}/custom-serviceaccounts.yaml" 'name: custom-controller'

helm template kcover "${chart}" \
  --kube-version 1.25.0 \
  --set controller.leaderElection.enabled=false >"${workdir}/leader-election-disabled.yaml"
assert_contains "${workdir}/leader-election-disabled.yaml" '--leader-elect=false'

echo "Helm chart verification passed"
