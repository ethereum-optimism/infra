# Network Acceptance Tester (op-acceptor)
op-acceptor is a tool to run checks against a network to determine if it is ready for production. It helps ensure your network meets all necessary requirements before deployment.
The checks it runs are standard Go tests.

## Concepts

### Test Discovery
op-acceptor discovers tests. You simply need to provide a path to a directory containing your tests. op-acceptor will discover all tests within that directory and also those within any subdirectories.

### Test Grouping
1. **Test** - An individual check that validates a specific aspect of your network
2. **Suite** - A logical grouping of related tests
3. **Gate** - A high-level collection of suites and/or tests that represent a complete validation scenario


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
  --gate betanet \                  # The gate to run
  --testdir ../../optimism/ \       # Path to the directory containing your tests
  --validators validators.yaml \    # Path to the validator definitions
```

By default, op-acceptor will run tests once and then exit, which is ideal for CI/CD pipelines and one-off testing.

If you want to run tests periodically (for continuous monitoring), specify a run interval:
```bash
go run cmd/main.go \
  --gate betanet \
  --testdir ../../optimism/ \
  --validators validators.yaml \
  --run-interval=1h                 # Run tests every hour
```

Want to monitor your validation runs? Start our local monitoring stack:
```bash
just start-monitoring  # Launches Prometheus and Grafana alongside op-acceptor
```

### Create a release
Releases are created by pushing tags which triggers a CircleCI pipeline.
Create an annotated tag (with a semantic version) and push it.

```
git tag -a op-acceptor/v0.1.0-rc.1 -m "Initial release candidate for op-acceptor"
git push origin op-acceptor/v0.1.0-rc.1
```

### Future Development
We track our public roadmap and issues on [Github](https://github.com/ethereum-optimism/infra/issues). Feel free to:
* Submit bug reports
* Propose new features
* Contribute improvements
* Join discussions

Your feedback and contributions help make op-acceptor better for everyone!
