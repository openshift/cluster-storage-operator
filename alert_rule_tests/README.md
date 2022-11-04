# How to test and run prometheus rules

## Download and install yq tool

    https://kislyuk.github.io/yq/

## Extract rules from k8s asset:
    cat assets/vsphere_problem_detector/12_prometheusrules.yaml|yq -Y '.spec' > alert_rule_tests/12_prometheusrules.yaml

## Run the unit test:


   promtool test rules ./alert_rule_tests/vsphere_older_version_test.yaml
