#!/usr/bin/env bash
# Verify that the Helm chart is in sync with the generated config/ artifacts.
#
# Checks:
#   RBAC — generated ClusterRole rules from config/rbac/role.yaml all appear
#          in the rendered Helm chart ClusterRole
#
# CRD drift is not checked here because the CRD file in charts/operator/crd/
# is gitignored and copied at build time by `make chart-sync-crd`.  Drift is
# impossible by construction.
#
# Usage: hack/chart-verify-sync.sh [CHART_DIR]
#   CHART_DIR defaults to charts/operator
#
# Requires: yq, helm, diff

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHART_DIR="${1:-${REPO_ROOT}/charts/operator}"

HELM="${HELM:-helm}"
ERRORS=0

for tool in yq "${HELM}" diff; do
  if ! command -v "${tool}" &>/dev/null; then
    echo "ERROR: ${tool} is required but not found in PATH" >&2
    exit 1
  fi
done

TMPDIR_VERIFY="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_VERIFY}"' EXIT

# Ensure the CRD file exists so that `helm template` can render crd.yaml.
# The CRD in charts/operator/crd/ is gitignored (single source of truth is
# config/crd/bases/), so we copy it here if it is missing.
CRD_SRC="${REPO_ROOT}/config/crd/bases/components.platform.opendatahub.io_workbenches.yaml"
CRD_DST="${CHART_DIR}/crd/workbenches.crd.yaml"
if [[ -f "${CRD_SRC}" && ! -f "${CRD_DST}" ]]; then
  mkdir -p "$(dirname "${CRD_DST}")"
  cp "${CRD_SRC}" "${CRD_DST}"
fi

# --- RBAC sync check ---
ROLE_GENERATED="${REPO_ROOT}/config/rbac/role.yaml"

if [[ ! -f "${ROLE_GENERATED}" ]]; then
  echo "WARN: ${ROLE_GENERATED} not found — run 'make manifests' first"
else
  yq -P -I 4 '.rules | sort_by(.apiGroups[0] // "" , .resources[0] // "")' \
    "${ROLE_GENERATED}" > "${TMPDIR_VERIFY}/generated-rules.yaml"

  "${HELM}" template verify-sync "${CHART_DIR}" \
    --namespace workbenches-operator-system \
    --set applicationsNamespace=opendatahub 2>"${TMPDIR_VERIFY}/helm-stderr.log" | \
    yq -P -I 4 'select(.kind == "ClusterRole" and (.metadata.name | test("manager-role$"))) | .rules | sort_by(.apiGroups[0] // "" , .resources[0] // "")' \
    > "${TMPDIR_VERIFY}/helm-rules.yaml"

  if [[ ! -s "${TMPDIR_VERIFY}/helm-rules.yaml" ]]; then
    echo "FAIL: Could not extract ClusterRole rules from Helm template output"
    [[ -s "${TMPDIR_VERIFY}/helm-stderr.log" ]] && cat "${TMPDIR_VERIFY}/helm-stderr.log" >&2
    ERRORS=$((ERRORS + 1))
  else
    rule_count="$(yq 'length' "${TMPDIR_VERIFY}/generated-rules.yaml")"
    missing=0

    for i in $(seq 0 $((rule_count - 1))); do
      yq -P -I 4 ".[${i}]" "${TMPDIR_VERIFY}/generated-rules.yaml" > "${TMPDIR_VERIFY}/want.yaml"

      found=false
      helm_count="$(yq 'length' "${TMPDIR_VERIFY}/helm-rules.yaml")"
      for j in $(seq 0 $((helm_count - 1))); do
        yq -P -I 4 ".[${j}]" "${TMPDIR_VERIFY}/helm-rules.yaml" > "${TMPDIR_VERIFY}/got.yaml"
        if diff -q "${TMPDIR_VERIFY}/want.yaml" "${TMPDIR_VERIFY}/got.yaml" &>/dev/null; then
          found=true
          break
        fi
      done

      if [[ "${found}" != "true" ]]; then
        api="$(yq -r '.apiGroups[0] // "nonResourceURLs"' "${TMPDIR_VERIFY}/want.yaml")"
        res="$(yq -r '.resources[0] // .nonResourceURLs[0] // "?"' "${TMPDIR_VERIFY}/want.yaml")"
        echo "FAIL: RBAC rule missing from Helm chart: apiGroups=[${api}] resources=[${res}]"
        missing=$((missing + 1))
      fi
    done

    if [[ ${missing} -eq 0 ]]; then
      echo "OK:   RBAC rules are in sync (${rule_count} generated rules found in Helm chart)"
    else
      echo "  → Run 'make chart-sync-rbac' to fix"
      ERRORS=$((ERRORS + missing))
    fi
  fi
fi

# --- Summary ---
if [[ ${ERRORS} -gt 0 ]]; then
  echo ""
  echo "FAILED: ${ERRORS} sync issue(s) found — see above"
  exit 1
else
  echo ""
  echo "All chart sync checks passed"
fi
