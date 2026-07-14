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
assert_contains "${workdir}/default.yaml" 'privileged: false'
assert_not_contains "${workdir}/default.yaml" 'verbs: ["*"]'

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
