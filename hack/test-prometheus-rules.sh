#!/usr/bin/env bash

# Test the production PrometheusRule against synthetic metric fixtures. Extract
# its native rules, point temporary fixture copies at them, then run promtool.
# Source manifests and fixtures are never modified.
# Use `make verify-prometheus-rules` to install/select the required tools.

set -o errexit
set -o nounset
set -o pipefail

# Resolve default asset/fixture paths relative to the repo, not the caller's cwd.
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "${repo_dir}"

# Make supplies these paths; direct callers can override them or use PATH.
# The yq commands below use Mike Farah yq v2 syntax, as pinned by build-machinery.
promtool=${PROMTOOL:-promtool}
yq=${YQ:-yq}

# An array omits the optional argument cleanly when debug output is disabled.
test_args=()
case "${PROMTOOL_DEBUG:-0}" in
  0) ;;
  1) test_args+=(--debug) ;;
  *)
    printf 'PROMTOOL_DEBUG must be 0 or 1\n' >&2
    exit 1
    ;;
esac

# Overrides allow focused fixture runs or testing an alternative rule asset.
rule_asset=${PROMETHEUS_RULE_ASSET:-assets/volumedeviceexporter/08_prometheusrule.yaml}
fixtures_dir=${PROMETHEUS_FIXTURES_DIR:-hack/testdata/prometheus}

# Fail with setup instructions before creating temporary files or running tests.
if ! command -v "${promtool}" >/dev/null 2>&1; then
  printf 'promtool not found: %s\n\n' "${promtool}" >&2
  printf '%s\n' \
    'Run from the repository root using one of these options:' \
    '' \
    '  # Install the pinned promtool with Go and run the tests:' \
    '  make verify-prometheus-rules' \
    '' \
    '  # Use existing promtool and yq v2 binaries (replace the paths):' \
    '  YQ=/absolute/path/to/yq PROMTOOL=/absolute/path/to/promtool ./hack/test-prometheus-rules.sh' >&2
  exit 1
fi
if ! command -v "${yq}" >/dev/null 2>&1; then
  printf 'yq not found: %s\nRun make verify-prometheus-rules to install the required tools, or set YQ to the repository-provided yq v2 binary.\n' "${yq}" >&2
  exit 1
fi
"${promtool}" --version
if [[ ! -r "${rule_asset}" || ! -d "${fixtures_dir}" ]]; then
  printf 'Rule asset must be readable and fixture directory must exist: %s, %s\n' "${rule_asset}" "${fixtures_dir}" >&2
  exit 1
fi

# Share one extracted rules file across all fixtures. Track every temporary file
# so EXIT cleanup also runs when YAML processing or a promtool invocation fails.
rules_file=$(mktemp)
temp_files=("${rules_file}")

cleanup() {
  rm -f "${temp_files[@]}"
}
trap cleanup EXIT

# PrometheusRule stores native Prometheus rule groups below spec. Extract that
# section so promtool tests the exact expressions defined in the selected asset.
# promtool expects a top-level groups list, not Kubernetes metadata/spec wrappers.
"${yq}" read "${rule_asset}" spec > "${rules_file}"

# Validate rule syntax once before evaluating any fixture's time-series data.
printf 'Validating rule definitions: %s\n' "${rule_asset}"
"${promtool}" check rules "${rules_file}"

# With nullglob, no matches produce an empty array rather than a literal *.yaml.
# Treat that as an error so an empty/mistyped fixture directory cannot pass CI.
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

  # Fixtures contain a single placeholder instead of a path to copied rules.
  # Compact JSON lets us compare the parsed list independently of YAML layout.
  # Reject additional rule files before promtool can load fixture-supplied paths.
  if [[ $("${yq}" read --tojson "${test_file}" rule_files) != '["replaced-by-test-script"]' ]]; then
    printf 'Invalid rule_files in %s: expected exactly one "replaced-by-test-script" entry\n' "${test_file}" >&2
    exit 1
  fi

  # Use the absolute mktemp path: promtool resolves rule files relative to the
  # temporary fixture, not the repository. Write a copy, leaving the source intact.
  "${yq}" write "${test_file}" 'rule_files[0]' "${rules_file}" > "${tests_file}"

  # List scenario names for CI logs; promtool evaluates expressions, alert labels,
  # annotations, and timing assertions against the synthetic input series.
  printf '\nTesting fixture: %s\n' "${test_file}"
  "${yq}" read "${test_file}" 'tests[*].name'

  # Handle failure explicitly to identify the source fixture and preserve the
  # promtool exit status, rather than letting errexit terminate without context.
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
