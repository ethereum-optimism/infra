# Network Acceptance Tester (NAT)
NAT is a tool to run checks against a network to determine if it is ready for production. It helps ensure your network meets all necessary requirements before deployment.

## Concepts

### Validators
NAT uses "validators" as its core building blocks. These come in three types:
1. **Test** - An individual check that validates a specific aspect of your network
2. **Suite** - A logical grouping of related tests
3. **Gate** - A high-level collection of suites and/or tests that represent a complete validation scenario

All validators are located in the `validators` directory and are written in Go.

## Contributing

Please note that this project is under active development and the API may evolve. We welcome all contributions and appreciate your interest in improving NAT!

### Adding a new validator
Adding a new validator is straightforward! Create a new file in the `validators` directory using one of these Go structs: `nat.Test`, `nat.Suite`, or `nat.Gate`.

Here's a template to get you started:

```go
type MyValidatorParams struct {
    // Your validator parameters here
}

var MyValidator = nat.Test{
    ID: "my-validator",
    DefaultParams: MyValidatorParams{
        // Default parameter values
    },
    Fn: func(ctx context.Context, log log.Logger, config nat.Config, _ interface{}) (bool, error) {
        // Your validation logic here
        return true, nil
    },
}
```

The parameters are optional, but if you provide them, you must provide a `DefaultParams` struct.

#### Key Components:
* `ID`: A unique identifier for your validator
* `DefaultParams`: Optional default parameters for your validator
* `Fn`: The main validation function that returns a pass/fail result and any errors

#### Naming Conventions
To maintain consistency:
* Use lowercase with hyphens for validator IDs
* If your validator belongs to a suite, prefix its ID with the suite name (e.g., `network-get-chainid`)
* Avoid using "test/suite/gate" in names as it's already implied by the package

## Development

### Prerequisites
* [Go](https://go.dev/dl/) 1.22+
* [Just](https://just.systems/)

### Getting Started
Launch NAT with these simple steps:
1. `just build`
2. `./bin/op-nat --kurtosis.devnet.manifest=../kurtosis-devnet/tests/interop-devnet.json`

Want to monitor your validation runs? Start our local monitoring stack:
```bash
just start-monitoring  # Launches Prometheus and Grafana alongside NAT
```

### Future Development
We track our public roadmap and issues on [Github](https://github.com/ethereum-optimism/infra/issues). Feel free to:
* Submit bug reports
* Propose new features
* Contribute improvements
* Join discussions

Your feedback and contributions help make NAT better for everyone!
