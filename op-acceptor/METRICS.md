# OP-Acceptor Prometheus Metrics

This document describes the Prometheus metrics exposed by OP-Acceptor for monitoring test execution and performance.

## Metrics Overview

All metrics use the namespace `nat` (Network Acceptance Tester).

## Test Run Metrics

### Basic Counters

- **`nat_tests_total{network_name, test_name, gate, suite}`** - Total number of tests run (aggregate counter)
- **`nat_tests_passed_total{network_name, test_name, gate, suite}`** - Total number of passed tests
- **`nat_tests_failed_total{network_name, test_name, gate, suite}`** - Total number of failed tests
- **`nat_tests_skipped_total{network_name, test_name, gate, suite}`** - Total number of skipped tests

### Per-Run Metrics (with run_id)

- **`nat_test_run_status{network_name, run_id, result}`** - Status of test runs (value is always 1)
- **`nat_test_run_tests_total{network_name, run_id}`** - Total tests in a specific run
- **`nat_test_run_tests_passed{network_name, run_id}`** - Passed tests in a specific run
- **`nat_test_run_tests_failed{network_name, run_id}`** - Failed tests in a specific run
- **`nat_test_run_tests_skipped{network_name, run_id}`** - Skipped tests in a specific run
- **`nat_test_run_duration_seconds{network_name, run_id}`** - Duration of the entire test run

## Individual Test Metrics

- **`nat_test_status{network_name, run_id, test_name, gate, suite}`** - Individual test status (1=pass, 0=fail, -1=skip)
- **`nat_test_latest_status{network_name, test_name, gate, suite}`** - Most recent status of each test without run_id (1=pass, 0=fail, -1=skip)
- **`nat_test_duration_seconds{network_name, run_id, test_name, gate, suite}`** - Duration of individual tests
- **`nat_test_duration_histogram_seconds{network_name, test_name, gate, suite}`** - Histogram of test durations for distribution analysis

### Timeout Tracking

- **`nat_test_timeouts_total{network_name, run_id, test_name, gate, suite}`** - Number of tests that timed out

## Gate-Level Metrics

Gates are logical groupings of tests (e.g., "pre-merge", "post-merge").

- **`nat_gate_tests_total{network_name, gate}`** - Total tests per gate
- **`nat_gate_tests_passed_total{network_name, gate}`** - Passed tests per gate
- **`nat_gate_tests_failed_total{network_name, gate}`** - Failed tests per gate
- **`nat_gate_duration_seconds{network_name, run_id, gate}`** - Total duration of gate execution

## Suite-Level Metrics

Suites are collections of related tests within a gate.

- **`nat_suite_tests_total{network_name, gate, suite}`** - Total tests per suite
- **`nat_suite_tests_passed_total{network_name, gate, suite}`** - Passed tests per suite
- **`nat_suite_tests_failed_total{network_name, gate, suite}`** - Failed tests per suite

## Error Tracking

- **`nat_errors_total{error}`** - Total count of errors by type
- **`nat_validations_total{network_name, run_id, name, type, result}`** - Validator execution results

## Example Prometheus Queries

### Test Success Rate

```promql
# Overall success rate
sum(rate(nat_tests_passed_total[5m])) / sum(rate(nat_tests_total[5m])) * 100

# Success rate by network
sum by (network_name) (rate(nat_tests_passed_total[5m])) / sum by (network_name) (rate(nat_tests_total[5m])) * 100

# Success rate by individual test
sum by (test_name) (rate(nat_tests_passed_total[5m])) / sum by (test_name) (rate(nat_tests_total[5m])) * 100

# Current status of all tests (most recent run)
nat_test_latest_status

# Currently failing tests
nat_test_latest_status == 0

# Currently passing tests
nat_test_latest_status == 1
```

### Test Failure Rate

```promql
# Failure rate over time
rate(nat_tests_failed_total[5m])

# Top 5 failing tests
topk(5, sum by (test_name) (rate(nat_tests_failed_total[1h])))

# Top 5 failing gates
topk(5, sum by (gate) (rate(nat_gate_tests_failed_total[1h])))

# Most unreliable tests (highest failure rate)
topk(10, sum by (test_name) (rate(nat_tests_failed_total[1h])) / sum by (test_name) (rate(nat_tests_total[1h])))

# Count of currently failing tests
count(nat_test_latest_status == 0)

# List of currently failing test names
nat_test_latest_status{test_name=~".+"} == 0
```

### Test Duration Analysis

```promql
# 95th percentile test duration by gate
histogram_quantile(0.95, sum by (gate, le) (rate(nat_test_duration_histogram_seconds_bucket[5m])))

# Slowest tests (average duration over last hour)
topk(10, avg_over_time(nat_test_duration_seconds[1h]))

# Median test duration
histogram_quantile(0.5, sum by (le) (rate(nat_test_duration_histogram_seconds_bucket[5m])))
```

### Timeout Detection

```promql
# Timeout rate
rate(nat_test_timeouts_total[5m])

# Tests with timeouts in last hour
count by (gate, suite) (increase(nat_test_timeouts_total[1h]) > 0)

# Total timeouts by gate
sum by (gate) (rate(nat_test_timeouts_total[5m]))
```

### Gate Performance

```promql
# Gate execution time
nat_gate_duration_seconds

# Gate failure rate
sum by (gate) (rate(nat_gate_tests_failed_total[5m])) / sum by (gate) (rate(nat_gate_tests_total[5m]))

# Gate success rate
sum by (gate) (rate(nat_gate_tests_passed_total[5m])) / sum by (gate) (rate(nat_gate_tests_total[5m])) * 100
```

### Suite Performance

```promql
# Suite success rate
sum by (suite) (rate(nat_suite_tests_passed_total[5m])) / sum by (suite) (rate(nat_suite_tests_total[5m]))

# Slowest suites
topk(5, histogram_quantile(0.95, sum by (suite, le) (rate(nat_test_duration_histogram_seconds_bucket[5m]))))

# Suite failure rate
sum by (gate, suite) (rate(nat_suite_tests_failed_total[5m])) / sum by (gate, suite) (rate(nat_suite_tests_total[5m]))
```

## Alerting Rules Examples

