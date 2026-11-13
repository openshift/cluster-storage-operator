#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "${repo_dir}"

promtool=${PROMTOOL:-promtool}
test_args=()
case "${PROMTOOL_DEBUG:-0}" in
  0) ;;
  1) test_args+=(--debug) ;;
  *)
    printf 'PROMTOOL_DEBUG must be 0 or 1\n' >&2
    exit 1
    ;;
esac
rule_asset=${PROMETHEUS_RULE_ASSET:-assets/volumedeviceexporter/08_prometheusrule.yaml}
fixtures_dir=${PROMETHEUS_FIXTURES_DIR:-hack/testdata/prometheus}
if ! command -v "${promtool}" >/dev/null 2>&1; then
  printf 'promtool not found: %s\n\n' "${promtool}" >&2
  printf '%s\n' \
    'Run from the repository root using one of these options:' \
    '' \
    '  # Install the pinned promtool with Go and run the tests:' \
    '  make verify-prometheus-rules' \
    '' \
    '  # Use an existing promtool binary (replace the path):' \
    '  PROMTOOL=/absolute/path/to/promtool ./hack/test-prometheus-rules.sh' >&2
  exit 1
fi
"${promtool}" --version
if [[ ! -r "${rule_asset}" || ! -d "${fixtures_dir}" ]]; then
  printf 'Rule asset must be readable and fixture directory must exist: %s, %s\n' "${rule_asset}" "${fixtures_dir}" >&2
  exit 1
fi
rules_file=$(mktemp)
temp_files=("${rules_file}")

cleanup() {
  rm -f "${temp_files[@]}"
}
trap cleanup EXIT

# PrometheusRule stores native Prometheus rule groups below spec. Extract that
# section so promtool tests the exact expressions defined in the selected asset.
awk '
  found { sub(/^  /, ""); print }
  /^spec:$/ { found = 1 }
  END { if (!found) exit 1 }
' "${rule_asset}" > "${rules_file}"

printf 'Validating rule definitions: %s\n' "${rule_asset}"
"${promtool}" check rules "${rules_file}"

shopt -s nullglob
test_files=("${fixtures_dir}"/*.yaml)
if ((${#test_files[@]} == 0)); then
  printf 'No Prometheus rule fixtures found in %s\n' "${fixtures_dir}" >&2
  exit 1
fi

fixtures_passed=0
for test_file in "${test_files[@]}"; do
  tests_file=$(mktemp)
  temp_files+=("${tests_file}")

  # Fixtures must begin with a rule_files list containing exactly one entry.
  # Replace it with the extracted rule asset before invoking promtool.
  awk -v rules_file="${rules_file}" '
    /^rule_files:/ {
      print "rule_files:"
      print "- " rules_file
      getline
      next
    }
    { print }
  ' "${test_file}" > "${tests_file}"

  printf '\nTesting fixture: %s\n' "${test_file}"
  # Describe the scenarios and assertions before execution. These counts are
  # diagnostics only: promtool remains responsible for parsing and testing YAML.
  awk '
    function describe() {
      if (name != "") {
        printf "  Scenario: %s (expression checks: %d, alert checks: %d)\n", name, expressions, alerts
      }
    }
    /^- name:/ {
      describe()
      name = substr($0, index($0, ":") + 2)
      expressions = alerts = 0
    }
    /^  - expr:/ { expressions++ }
    /^  - eval_time:/ { alerts++ }
    END { describe() }
  ' "${test_file}"

  if "${promtool}" test rules "${test_args[@]}" "${tests_file}"; then
    printf 'PASS: %s (all scenarios and assertions)\n' "${test_file}"
    fixtures_passed=$((fixtures_passed + 1))
  else
    status=$?
    printf 'FAIL: %s (promtool exit status %d)\n' "${test_file}" "${status}" >&2
    exit "${status}"
  fi
done

printf '\nPASS: %d/%d fixtures; all scenarios and assertions passed.\n' "${fixtures_passed}" "${#test_files[@]}"
