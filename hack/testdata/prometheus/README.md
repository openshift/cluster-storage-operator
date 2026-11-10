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

The target runs `hack/test-prometheus-rules.sh`:

- Set `PROMTOOL=/path/to/promtool` for native execution. This takes precedence
  over `PROMETHEUS_IMAGE`.
- Otherwise, set `PROMETHEUS_IMAGE` to an image matching the target OpenShift
  release for container execution; no image is selected automatically.
- If neither is set, the script uses `promtool` from `PATH`.

CI already inside a Prometheus image can invoke the script directly with
`PROMTOOL=/bin/promtool`, without Make, Go, or a container runtime.
See the root README for image and CI setup.

For detailed evaluation output:

```sh
PROMTOOL_DEBUG=1 PROMTOOL=/path/to/promtool make verify-prometheus-rules
```

## Fixture coverage

The fixtures are organized by subject:

- `volume-pv-context.yaml`: PV-to-PVC context selection and CSI metadata.
- `volume-device-context.yaml`: device topology recordings, annotations, and alert thresholds.
- `lifecycle.yaml`: path failure and recovery lifecycle behavior.
- `multipath.yaml`: multipath timers, node-scoped identities, and missing health.
- `nvme-subsystem.yaml`: NVMe-oF path states, timers, and missing live metrics.
- `exporter-availability.yaml`: absence of all exporter `up` series versus failed
  scrapes (`up = 0`) for an individual target, including timers and recovery.
- `volume-path-health.yaml`: integrated annotation fixtures and transient combined path health.

The runner extracts and validates the production rules once, then tests each
fixture against them. Output lists scenarios and assertion counts; a fixture's
PASS line confirms its assertions succeeded. Fixtures use raw input metrics to
exercise the ordered recording chain rather than maintaining copies of the rules.

## Adding fixtures

Add a focused `*.yaml` fixture using the existing layout:

- Start with `rule_files:` followed immediately by exactly one list entry;
  the runner replaces that entry with the extracted production rules.
- Keep the `- name:`, `  - expr:`, and `  - eval_time:` indentation so the runner
  can report scenario names and assertion counts.
- Use `stale` to remove a series immediately; omitted values are subject to
  Prometheus lookback. `AxN` produces `N+1` samples, starting at `t0`.
- Check both pending and firing times. At a one-minute evaluation interval,
  a condition starting at `t0` with `for: 5m` is pending at `4m` and fires at `5m`.
- For tied selections, assert stable properties such as cardinality rather than
  a particular winner.
