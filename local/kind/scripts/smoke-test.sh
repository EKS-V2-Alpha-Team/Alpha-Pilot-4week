#!/usr/bin/env bash
# Smoke test: every Deployment in the namespace rolls out and every pod is Ready.
# Usage: smoke-test.sh <namespace> [timeout]
set -euo pipefail

NAMESPACE="${1:?usage: smoke-test.sh <namespace> [timeout]}"
TIMEOUT="${2:-180s}"

echo "==> Smoke testing namespace '${NAMESPACE}' (timeout ${TIMEOUT})"

deployments=$(kubectl get deployments -n "${NAMESPACE}" -o name)
if [[ -z "${deployments}" ]]; then
  echo "FAIL: no Deployments found in '${NAMESPACE}'. Did you run make kind-deploy?"
  exit 1
fi

failed=0
for d in ${deployments}; do
  if kubectl rollout status "${d}" -n "${NAMESPACE}" --timeout="${TIMEOUT}"; then
    echo "  OK   ${d}"
  else
    echo "  FAIL ${d}"
    failed=1
  fi
done

if [[ "${failed}" -ne 0 ]]; then
  echo
  echo "==> Some workloads failed. Useful next commands:"
  echo "  kubectl get pods -n ${NAMESPACE}"
  echo "  kubectl describe pod <pod> -n ${NAMESPACE}"
  echo "  kubectl logs <pod> -n ${NAMESPACE} --previous"
  exit 1
fi

echo "==> All workloads in '${NAMESPACE}' are healthy"
