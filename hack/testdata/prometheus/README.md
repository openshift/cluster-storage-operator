# Prometheus Rule Fixtures

These `promtool` fixtures use synthetic metrics to test volume mappings, path
health, and alert firing/recovery. For rule behavior and alert conditions, see
the definitions and comments in
[`assets/volumedeviceexporter/08_prometheusrule.yaml`](../../../assets/volumedeviceexporter/08_prometheusrule.yaml).

## Running the fixtures

Run the complete fixture suite with:

```sh
make verify-prometheus-rules
```

The target installs `promtool` using
`go install -mod=mod github.com/prometheus/prometheus/cmd/promtool@v0.316.0` into
`_output/tools/bin/` and runs `hack/test-prometheus-rules.sh`.
It also ensures the existing build-machinery `yq` v2 tool is installed for YAML
extraction, validation of the rule-file list, and placeholder replacement.
Installation requires Go and network access, but no container runtime.
Go caches downloads and compilation results for subsequent runs.
The required CI verify job runs these fixtures through `make verify`; `make check`
also includes them. The install command explicitly overrides vendor mode for
the tool without changing the operator's vendored dependencies.

To use an existing binary without installation, invoke the script directly:

```sh
YQ=/path/to/yq PROMTOOL=/path/to/promtool ./hack/test-prometheus-rules.sh
```

Alternatively, put `promtool` and the repository-compatible `yq` v2 on `PATH`.
Direct script invocation needs neither Make nor Go;
`make verify-prometheus-rules` always installs the pinned promtool.

For detailed evaluation output:

```sh
PROMTOOL_DEBUG=1 make verify-prometheus-rules
```

## Fixture coverage

The fixtures are organized by subject:

- `volume-pv-context.yaml`: PV-to-PVC context selection, shared handles, and CSI metadata.
- `volume-device-context.yaml`: device topology recordings and alert annotations.
- `lifecycle.yaml`: path failure and recovery lifecycle behavior.
- `multipath.yaml`: device- and node-scoped identities and missing health.
- `nvme-subsystem.yaml`: NVMe-oF path states, subsystem isolation, and missing live metrics.
- `exporter-availability.yaml`: absence of all exporter `up` series versus failed
  scrapes (`up = 0`) for an individual target, including timers and recovery.
- `alerts-integration.yaml`: integrated bound/unclaimed PV labels and alert annotations.

The runner extracts and validates the production rules once, then tests each
fixture against them. Output lists scenario names; a fixture's
PASS line confirms its assertions succeeded. Fixtures use raw input metrics to
exercise the ordered recording chain rather than maintaining copies of the rules.

## Adding fixtures

Add a focused `*.yaml` fixture using the existing layout:

- Include this rule-file list, with no additional entries:
  ```yaml
  rule_files:
  - replaced-by-test-script
  ```
  The runner validates the parsed YAML list, replaces the placeholder with the
  extracted production rules, and rejects missing, duplicate, or additional
  entries before testing the fixture. Comments and YAML formatting do not affect
  replacement.
- Use `stale` to remove a series immediately; omitted values are subject to
  Prometheus lookback. `AxN` produces `N+1` samples, starting at `t0`.
- Use `evaluation_interval: 30s` and `interval: 30s` throughout the fixtures.
  Scrape intervals and rule-evaluation intervals are separate settings; this is
  a consistent test cadence, not an assertion about production evaluation timing.
- Check both pending and firing times. A condition starting at `t0` with
  `for: 5m` is pending at `4m30s` and fires at `5m`; `for: 1m` is pending
  at `30s` and fires at `1m`. Keep lifecycle/timer assertions in `lifecycle.yaml`.
- For tied selections, assert stable properties such as cardinality rather than
  a particular winner.
