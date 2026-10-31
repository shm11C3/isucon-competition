#!/usr/bin/env bash
# Fail-closed deploy-then-measure chain for the current checkout.
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

usage() { echo 'usage: scripts/measure-current.sh OPERATOR LABEL' >&2; exit 2; }
[[ $# -eq 2 ]] || usage
operator=$1
label=$2
[[ $operator =~ ^[A-Za-z0-9._-]{1,80}$ ]] || { echo 'invalid operator identifier' >&2; exit 2; }
[[ -n $label && $label != *$'\n'* && $label != *$'\r'* ]] || { echo 'label must be non-empty and single-line' >&2; exit 2; }

check_source_state() {
  local current dirty unmerged
  if ! current=$(git rev-parse HEAD); then echo 'cannot read source HEAD' >&2; return 1; fi
  [[ $current == "$requested_sha" ]] || { echo 'source HEAD changed; refusing benchmark' >&2; return 1; }
  if ! unmerged=$(git ls-files -u); then echo 'cannot inspect unmerged index entries' >&2; return 1; fi
  [[ -z $unmerged ]] || { echo 'unmerged index entries; refusing benchmark' >&2; return 1; }
  if ! dirty=$(git status --porcelain --untracked-files=all -- go sql public); then echo 'cannot inspect source status' >&2; return 1; fi
  [[ -z $dirty ]] || { echo 'go/sql/public changed or untracked; refusing benchmark' >&2; return 1; }
}

requested_sha=$(git rev-parse HEAD)
[[ $requested_sha =~ ^[0-9a-f]{40}$ ]] || { echo 'could not identify full source SHA' >&2; exit 1; }
check_source_state || exit 1
printf 'measurement operator=%s label=%s requested_source_sha=%s runs=1\n' \
  "$operator" "$label" "$requested_sha" >&2

deploy_log=$(mktemp "${TMPDIR:-/tmp}/measure-current-deploy.XXXXXX")
trap 'rm -f "$deploy_log"' EXIT
if ! bash scripts/deploy.sh >"$deploy_log"; then
  cat "$deploy_log" >&2
  echo 'deployment failed; benchmark not started' >&2
  exit 1
fi
cat "$deploy_log" >&2
deployed_sha=$(tail -n 1 "$deploy_log")
[[ $deployed_sha == "$requested_sha" ]] || {
  echo "deployment SHA mismatch: expected $requested_sha, got ${deployed_sha:-<empty>}" >&2
  exit 1
}
check_source_state || exit 1
node tools/portal-bench/portal-bench.mjs run \
  --operator "$operator" --source-sha "$requested_sha" --label "$label"
