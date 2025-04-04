# Release Process

This document the process for releasing a new version of one or more services in the `ethereum-optimism/infra` repository.
The release process is driven by CircleCI to build and publish components

## Release Process Overview

1. **Tagging and Releasing**

   The release process is initiated by creating a new tag on the main branch
   The tag should be prefixed with the component and suffixed with release semver
   Ex. op-acceptor/v0.1.6 / <component>/v<major>.<minor>.<patch>

   > **Note**: Generally, the branch used for releases is `main` but if needed, you can use a different branch, say, if you'd like to create test releases from your branch.

2. **Triggering the CircleCI Workflow**

   Once a release tag is created, the CircleCI `release` workflow is triggered. The configuration for this workflow is located in `.circle/config.yml` within the tagged release.

   The workflow compares differences between the newly released tag and the `main` branch (the base revision). Based on these differences, it selectively toggles parameters to `true` only for services that have been updated. This prevents unnecessary builds or deployments when minor changes are made to unrelated services. [Refer to config here](https://github.com/ethereum-optimism/infra/blob/main/.circleci/config.yml#L18-L25).

3. **Approval Process for Certain Tags**

   For specific tags ([listed here](https://github.com/ethereum-optimism/infra/blob/main/.circleci/continue_config.yml#L590)), the workflow pauses with a hold step that requires approval from a member of the `@release-managers` team on `ethereum-optimism` GitHub Organisation. Once approved, the workflow continues.

4. **Selective Component Release**

   Depending on the type of released tag i.e. the component/service updated in the release, the following steps are selectively triggered to handle only that updated services:

   - `docker-build`: Builds Docker images for that updated service.
   - `docker-publish`: Publishes that image.
   - `docker-tag-op-stack-release`: Attaches from more tags to that image compliant with OP-Stack releases.

   Only the components that were updated in the circle ci release are built and delivered as Docker images.

## Requesting a Release as External Contributor

Typically a reviewer will publish a new release after reviewing the contribution.
Feel free to request a new release as part of the creating the PR in the PR description.

If no release has been created feel free to create an issue or reach out in the OP Stack Protocol R&D Discord

