# Release Process

This documents the process of releasing a new version of one or more services in the `ethereum-optimism/infra` repository.
The release process is driven by tags. GitHub Actions builds and publishes the Go binaries for the modules listed in step 2.

## Release Process Overview

1. **Tagging and Releasing**

   The release process is initiated by creating a new tag on the main branch
   The tag should be prefixed with the component and suffixed with release semver
   Ex. op-signer/v0.1.6 / <component>/v<major>.<minor>.<patch>

   > **Note**: Generally, the branch used for releases is `main` but if needed, you can use a different branch, say, if you'd like to create test releases from your branch.

2. **Publishing Go Binaries**

   Pushing a `<module>/vX.Y.Z` tag for one of `op-signer`, `op-txproxy`, `op-ufm`, `proxyd`, `op-conductor-mon`, `peer-mgmt-service`, or `cci-stats` runs the `release-go-binaries` GitHub Actions workflow (`.github/workflows/release-go-binaries.yaml`). The workflow builds archives for linux/amd64, darwin/amd64, darwin/arm64, and windows/amd64, writes a `<module>_<version>_checksums.txt` file, and publishes a GitHub release for the tag with the changelog generated from the commits that touched the module.

   Versions with a `-` suffix are published as prereleases. `op-txproxy` releases are never marked as the latest release.

   The workflow only runs for a tag whose commit contains it. For any other tag, or to run the workflow again for an existing tag, start it manually with `workflow_dispatch` and the `tag` input. A manual run can only update a tag that has no published release.

3. **Selective Component Release**

   Depending on the type of released tag i.e. the component/service updated in the release, the following steps are selectively triggered to handle only that updated services:

   - `docker-build`: Builds Docker images for that updated service.
   - `docker-publish`: Publishes that image.
   - `docker-tag-op-stack-release`: Attaches from more tags to that image compliant with OP-Stack releases.

   Only the components that were updated in the circle ci release are built and delivered as Docker images.

## Requesting a Release as External Contributor

Typically a reviewer will publish a new release after reviewing the contribution.
Feel free to request a new release as part of the creating the PR in the PR description.

If no release has been created feel free to create an issue or reach out in the OP Stack Protocol R&D Discord
