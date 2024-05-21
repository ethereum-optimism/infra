# Contributing

🎈 Thanks for your help improving the project! We are so happy to have you!

🚨 Before making any non-trivial change, please first open an issue describing the change to solicit feedback and guidance. This will increase the likelihood of the PR getting merged.

In general, the smaller the diff the easier it will be for us to review quickly.

Please note that we have a [Code of Conduct](./CODE_OF_CONDUCT.md), please follow it in all your interactions with the project.

# Pull Request Process

## Steps for the PR author

We recommend using the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format on commit messages.

Before opening a PR:

1. Ensure that tests pass and code is lint free. You can run `yarn test` and `yarn lint` locally to check.
1. Update the README.md if your changes invalidate or extend its current content.
1. Include tests for any new functionality.
1. Ensure each section of the [Pull Request Template](./PULL_REQUEST_TEMPLATE.md) is filled out. Delete any sections that are not relevant.

Unless your PR is ready for immediate review and merging, please mark it as 'draft' (or simply do not open a PR yet).

**Bonus:** Add comments to the diff under the "Files Changed" tab on the PR page to clarify any sections where you think we might have questions about the approach taken.

### Response time:
We aim to provide a meaningful response to all PRs and issues from external contributors within 2 business days.

## Steps for PR Reviewers

### For all PRs

Reviewers should submit their review with either `Approve` or `Request changes` options (not `Comment`).

If the reviewer selects `Request changes`, they should clearly indicate which changes they require in order to approve.

If the reviewer selects `Approves`, they should either:
1. immediately merge it, or
2. indicate what further review they deem necessary (and from whom).

We further distinguish between two classes of PR those which modify production code (ie. smart contracts, go-ethereum), and those which do not (ie. dev tooling, test scripts, comments).

### For PRs which modify production code

The reviewer's job is to check that the PR:

1. Conforms to the specification (WIP, soon to be published here), or has an issue describing the additional functionality which a code owner has approved.
2. Is appropriately tested.
3. Does not introduce security issues.

#### Merge Criteria

1. All CI checks MUST pass.
1. At least 2 code owners must approve.
1. In the case of very simple changes, a single code owner may choose to merge at their discretion.

### For PRs which modify non-production code

For PRs which do not modify production code (ie. test, dev tooling),

The reviewer's job is to check that the PR:

1. Is correct.
1. Is desirable.

#### Merge Criteria

1. All CI checks MUST pass.
1. At least one code owner must approve.
