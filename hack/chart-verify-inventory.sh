#!/usr/bin/env bash
# Compare the set of Kubernetes resource kinds produced by the kustomize
# overlay (config/default) against the Helm chart.  Flags any kind present
# in kustomize but missing from the Helm chart output — the exact class of
# bug where a new resource is added to config/ but forgotten in the chart.
#
# Known differences are listed in KNOWN_HELM_MISSING below and silently
# accepted. Update that list when a difference is intentional.
#
# Usage: hack/chart-verify-inventory.sh [CHART_DIR]
#   CHART_DIR defaults to charts/operator
#
# Requires: yq, helm, kustomize (or make kustomize)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHART_DIR="${1:-${REPO_ROOT}/charts/operator}"

HELM="${HELM:-helm}"
KUSTOMIZE="${KUSTOMIZE:-${REPO_ROOT}/bin/kustomize}"

for tool in yq "${HELM}"; do
  if ! command -v "${tool}" &>/dev/null; then
    echo "ERROR: ${tool} is required but not found in PATH" >&2
    exit 1
  fi
done

if [[ ! -x "${KUSTOMIZE}" ]]; then
  echo "ERROR: kustomize not found at ${KUSTOMIZE} — run 'make kustomize' first" >&2
  exit 1
fi

# Resource kinds that kustomize produces but the Helm chart intentionally
# omits (or gates behind a non-default value).  Update this list when a
# difference is by design.
KNOWN_HELM_MISSING=(
  # Helm chart defaults to createOperatorNamespace: false (chart compliance
  # for opendatahub-operator allows Deployment/RBAC/CRD only, not Namespace).
  "Namespace"
)

TMPDIR_INV="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_INV}"' EXIT

# Ensure the CRD file exists for helm template.
CRD_SRC="${REPO_ROOT}/config/crd/bases/components.platform.opendatahub.io_workbenches.yaml"
CRD_DST="${CHART_DIR}/crd/workbenches.crd.yaml"
if [[ -f "${CRD_SRC}" && ! -f "${CRD_DST}" ]]; then
  mkdir -p "$(dirname "${CRD_DST}")"
  cp "${CRD_SRC}" "${CRD_DST}"
fi

# Render kustomize and extract sorted unique kinds.
"${KUSTOMIZE}" build "${REPO_ROOT}/config/default" | \
  yq -N '.kind' | sort -u > "${TMPDIR_INV}/kustomize-kinds.txt"

# Render Helm with leader election enabled (matches kustomize default) and
# extract sorted unique kinds.
"${HELM}" template inventory-check "${CHART_DIR}" \
  --namespace workbenches-operator-system \
  --set applicationsNamespace=opendatahub \
  --set leaderElection.enabled=true \
  2>"${TMPDIR_INV}/helm-stderr.log" | \
  yq -N '.kind' | sort -u > "${TMPDIR_INV}/helm-kinds.txt"

if [[ ! -s "${TMPDIR_INV}/helm-kinds.txt" ]]; then
  echo "FAIL: helm template produced no output"
  [[ -s "${TMPDIR_INV}/helm-stderr.log" ]] && cat "${TMPDIR_INV}/helm-stderr.log" >&2
  exit 1
fi

ERRORS=0

while IFS= read -r kind; do
  # Skip known exceptions.
  skip=false
  for known in "${KNOWN_HELM_MISSING[@]}"; do
    if [[ "${kind}" == "${known}" ]]; then
      skip=true
      break
    fi
  done
  if [[ "${skip}" == "true" ]]; then
    continue
  fi

  if ! grep -qx "${kind}" "${TMPDIR_INV}/helm-kinds.txt"; then
    echo "FAIL: Resource kind '${kind}' found in kustomize but missing from Helm chart"
    ERRORS=$((ERRORS + 1))
  fi
done < "${TMPDIR_INV}/kustomize-kinds.txt"

# Also warn about kinds in Helm but not in kustomize (informational only).
while IFS= read -r kind; do
  if ! grep -qx "${kind}" "${TMPDIR_INV}/kustomize-kinds.txt"; then
    echo "INFO: Resource kind '${kind}' found in Helm chart but not in kustomize (may be intentional)"
  fi
done < "${TMPDIR_INV}/helm-kinds.txt"

if [[ ${ERRORS} -gt 0 ]]; then
  echo ""
  echo "FAILED: ${ERRORS} resource kind(s) missing from Helm chart — see above"
  exit 1
else
  echo "OK:   Resource kind inventory matches (kustomize vs Helm chart)"
fi
