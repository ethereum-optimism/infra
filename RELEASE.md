# Release Process

This document outlines the steps and processes for releasing a new version of one or more services in the `ethereum-optimism/infra` repository. The release process is driven by a GitHub Action (`Tag Service`) and subsequently triggers workflows in CircleCI to build and publish updated [these services/components](https://github.com/ethereum-optimism/infra/blob/main/ops/tag-service/tag-tool.py#L6-L13) selectively.

## Release Process Overview

1. **Tagging and Releasing with GitHub Action**

   The release process is initiated by the "Tag Service" GitHub Action [here](https://github.com/ethereum-optimism/infra/actions/workflows/tag-service.yml). To trigger this action:
   
   - Select the branch containing the action—usually `main`.
   - Choose the appropriate parameters to specify which services should be tagged and released.

   > **Note**: Generally, the branch used for releases is `main` but if needed, you can use a different branch, say, if you'd like to create test releases from your branch.

   The GitHub Action relies on `ops/tag-service`, which manages key release functions:
   
   - Identifying supported services.
   - Determining version increments.
   - Specifying precise tags for release.
   - Configuring the release title and release notes.

2. **Triggering the CircleCI Workflow**

   Once a release tag is created by the above Github Action, the CircleCI `release` workflow is triggered. The configuration for this workflow is located in `.circle/config.yml` within the tagged release.

   The workflow compares differences between the newly released tag and the `main` branch (the base revision). Based on these differences, it selectively toggles parameters to `true` only for services that have been updated. This prevents unnecessary builds or deployments when minor changes are made to unrelated services. [Refer to config here](https://github.com/ethereum-optimism/infra/blob/main/.circleci/config.yml#L18-L25).

3. **Approval Process for Certain Tags**

   For specific tags ([listed here](https://github.com/ethereum-optimism/infra/blob/main/.circleci/continue_config.yml#L533)), the workflow pauses with a hold step that requires approval from a member of the `@release-managers` team on `ethereum-optimism` GitHub Organisation. Once approved, the workflow continues.

4. **Selective Component Release**

   Depending on the type of released tag i.e. the component/service updated in the release, the following steps are selectively triggered to handle only that updated services:

   - `docker-build`: Builds Docker images for that updated service.
   - `docker-publish`: Publishes that image.
   - `docker-tag-op-stack-release`: Attaches from more tags to that image compliant with OP-Stack releases.

   Only the components that were updated in the GitHub release are built and delivered as Docker images.