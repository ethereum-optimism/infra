# Network Acceptance Tester (op-acceptor)

op-acceptor is a tool to run checks against a network to determine if it is ready for production. It helps ensure your network meets all necessary requirements before deployment.
The checks it runs are standard Go tests.

## Usage Modes

### Gateless Mode

For quick testing without configuration, op-acceptor can auto-discover and run all tests in a directory:

```bash
# Run all tests in a directory
op-acceptor --testdir ./my-acceptance-tests

# Run all tests recursively (supports Go's ... notation)
op-acceptor --testdir ./my-acceptance-tests/...

# With additional options
op-acceptor --testdir ./my-tests \
    --orchestrator sysgo \
    --run-interval 0 \
    --log.level info
```

In gateless mode:
- **No validators config required**: Tests are auto-discovered
- **No gate specification needed**: Creates a synthetic "gateless" gate
- **Supports Go's ... notation**: Recursively finds all test packages
- **Simple workflow**: Perfect for CI/CD or quick test runs

#### Timeout Configuration

Control test timeouts in gateless mode:

```bash
# Set a specific timeout for all tests (recommended)
op-acceptor --testdir ./tests --timeout 10m

# Use the default timeout fallback (5 minutes)
op-acceptor --testdir ./tests --default-timeout 2m

# Disable timeouts entirely
op-acceptor --testdir ./tests --timeout 0
```

The `--timeout` flag applies to all auto-discovered tests in gateless mode. If not specified, it falls back to `--default-timeout` (default: 5m).

### Gate-based Mode

For structured testing with gates, suites, and inheritance, use the traditional approach:

```bash
# Run specific gate with validators config
DEVNET_ENV_URL=kt://isthmus-devnet op-acceptor \
    --testdir ../../optimism/ \
    --gate isthmus \
    --validators ../../optimism/op-acceptance-tests/acceptance-tests.yaml \
    --log.level INFO
```

## Concepts

### Test Discovery
op-acceptor discovers tests by scanning for `*_test.go` files. In gateless mode, it automatically finds all directories containing test files. In gate-based mode, you specify tests in a validators configuration file.

### Test Grouping
1. **Test** - An individual check that validates a specific aspect of your network
2. **Suite** - A logical grouping of related tests (gate-based mode only)
3. **Gate** - A high-level collection of suites and/or tests that represent a complete validation scenario

### Addons
See [ADDONS.md](./ADDONS.md)

## Migration from `go test`

If you're currently running `go test some-dir/...`, you can easily switch to op-acceptor:

```bash
# Instead of:
go test ./acceptance-tests/...

# Use:
op-acceptor --testdir ./acceptance-tests/...
```

Benefits of using op-acceptor over plain `go test`:
- Rich HTML reporting and structured output
- Metrics and monitoring integration
- Test result aggregation and history
- Standardized test execution environment
- Integration with devstack orchestrators

## Contributing

Please note that this project is under active development and the API may evolve. We welcome all contributions and appreciate your interest in improving op-acceptor!

### Adding a new test/suite/gate
All tests, suites, and gates are defined in a `validators.yaml` file. The filename is not important.
For an example `validators.yaml`, see [example-validators.yaml](./example-validators.yaml).

**Gate**
A gate is a collection of suites and/or tests that represent a complete validation scenario. A gate can inherit from one or more other gates.

```yaml
# Example gate definition
- id: alphanet
  description: "Alphanet validation gate"
  inherits: ["localnet"]
  suites:
    - id: interop
      description: "Interop suite"
      tests:
        - id: TestInteropSystem
```

**Suite**
A suite is a collection of tests that validate a specific aspect of your network. It's a convenience mechanism for grouping tests together.

```yaml
# Example suite definition
- id: interop
  description: "Interop suite"
  tests:
    - id: TestInteropSystem
```

**Test**
A test is a single check that validates a specific aspect of your network.
A description is optional.
A package is optional. If not provided, the test will be searched for. This lengthens the runtime, so we recommend providing the package.
If a package is provided and no name is provided, op-acceptor runs all tests in the package.

```yaml
# Run test 'TestInteropSystem' in package 'github.com/ethereum-optimism/optimism/kurtosis-devnet/tests/interop'
- name: TestInteropSystem
  description: "Test the interop system"
  package: github.com/ethereum-optimism/optimism/kurtosis-devnet/tests/interop
```

```yaml
# Run all tests in package 'github.com/ethereum-optimism/optimism/kurtosis-devnet/tests/interop'
- package: github.com/ethereum-optimism/optimism/kurtosis-devnet/tests/interop
```

## Development

### Prerequisites
* [Go](https://go.dev/dl/) 1.22+
* [Just](https://just.systems/)

### Getting Started
Build the binary:
```bash
just build
```

Run tests:
```bash
just test
```

Run op-acceptor:
```bash
DEVNET_ENV_URL=devnets/alpaca-devnet.json # path to the devnet manifest
go run cmd/main.go \
  --gate interop \                  # The gate to run
  --testdir ../../optimism/ \       # Path to the directory containing your tests
  --validators validators.yaml \    # Path to the validator definitions
```

By default, op-acceptor will run tests once and then exit, which is ideal for CI/CD pipelines and one-off testing.

If you want to run tests periodically (for continuous monitoring), specify a run interval:
```bash
go run cmd/main.go \
  --gate interop \
  --testdir ../../optimism/ \
  --validators validators.yaml \
  --run-interval 1h                # Run tests every hour
```

By default, op-acceptor sets the `DEVNET_EXPECT_PRECONDITIONS_MET` environment variable. This instructs [devnet-sdk](https://github.com/ethereum-optimism/optimism/tree/develop/devnet-sdk) to make tests fail if preconditions are not met, rather than skip. If you wish to bypass this and instead allow tests to skip when preconditions are not met, use the `--allow-skips` flag:
```bash
go run cmd/main.go \
  --gate interop \
  --testdir ../../optimism/ \
  --validators validators.yaml \
  --allow-skips                    # Allow tests to skip when preconditions aren't met
```

Want to monitor your validation runs? Start our local monitoring stack:
```bash
just start-monitoring  # Launches Prometheus and Grafana alongside op-acceptor
```


### Log Management

By default, test output from all tests is saved to log files, while only test results summaries are shown in the terminal. This makes the output much more manageable, especially when running many tests.

Test logs are saved under the log directory path, in a directory named with a standardized prefix `testrun-` followed by the run ID. The default log directory is `logs`, but you can specify a custom location:

```bash
go run cmd/main.go \
  --gate interop \
  --testdir ../../optimism/ \
  --validators validators.yaml \
  --logdir /path/to/logs          # Custom log directory
```

#### Log Directory Structure

For each test run, a new directory is created with the following structure:
```
logs/
  testrun-{run-id}/              # Each run gets a unique directory with 'testrun-' prefix
    passed/                      # Successful test logs
      {test-name}.log
    failed/                      # Failed test logs
      {test-name}.log
    results.html                 # HTML summary of the test run
    summary.log                  # Text summary with timeout warnings and counts
    all.log                     # Combined log of all tests
    raw_go_events.log           # Raw Go test events in -json format
```

#### Log File Types

1. **HTML Summary** (`results.html`)
   - Interactive summary of all test results
   - Includes test status, duration, and error details
   - Provides filtering and search capabilities

2. **Individual Test Logs** (`passed/` and `failed/` directories)
   - Each test gets its own log file with a standardized name
   - Contains both plaintext and JSON output
   - Failed tests include:
     - Clear error summaries
     - Timeout indicators when applicable
     - Expected vs actual results for assertions
     - Stack traces for debugging

3. **Summary Log** (`summary.log`)
   - Overall test run summary
   - Test counts by status (passed/failed/skipped)
   - Timeout warnings and counts
   - Total run duration

4. **Combined Log** (`all.log`)
   - Chronological log of all test output
   - Includes clear separators between tests
   - Structured format for easy reading
   - Contains metadata for each test (package, gate, suite, etc.)

5. **Raw Events Log** (`raw_go_events.log`)
   - Complete raw JSON output from all tests
   - Compatible with `go test -json` format
   - Useful for integration with Go test analysis tools
   - Can be processed by tools like `gotestsum`

#### Test Log Naming

Test log files follow a standardized naming pattern that includes:
- Gate name (if specified)
- Suite name (if specified)
- Package name (shortened for readability)
- Test function name

For example:
```
isthmus-acceptance_package_TestWithSubtests.log
```

#### Raw JSON Events Logging

The `raw_go_events.log` file contains the complete raw JSON output from all tests. This file is compatible with tools like `gotestsum` and other Go test analysis tools that expect the standard `go test -json` format.

Log directories are preserved between runs, with each run getting its own unique directory.


### Create a release
Releases are created by pushing tags which triggers a CircleCI pipeline.
One simply needs to create an annotated tag (with a semantic version) and push it.
Note that the CircleCI workflow will require manual approval before it will publish the new release.

```
# Determine the latest version
git tag -l --sort=-v:refname | grep op-acceptor

# Set you target version (increase the latest appropriately)
# and add a useful summary
VERSION=v0.1.6
SUMMARY="Some useful summary of changes"

# Tag your release
git tag -a op-acceptor/$VERSION -m "$SUMMARY"
git push origin op-acceptor/$VERSION
```

Finally, approve the hold on the 'release' workflow in [CircleCI](https://app.circleci.com/pipelines/github/ethereum-optimism/infra)

### Future Development
We track our public roadmap and issues on [Github](https://github.com/ethereum-optimism/infra/issues). Feel free to:
* Submit bug reports
* Propose new features
* Contribute improvements
* Join discussions

Your feedback and contributions help make op-acceptor better for everyone!
