# Cloud Deploy Git Deployer

Git Deployer is a sample implementation of a Cloud Deploy [Custom Target](https://cloud.google.com/deploy/docs/custom-targets) for deploying to a Git repository. It allows users to manage Cloud Deploy delivery pipelines while using a Kubernetes git synchronization tool (such as Config Sync) to actually apply changes to the cluster. This enables normal operation and use of Cloud Deploy features (such as progressions, verification, automation, etc…) with the difference being the deploy job writes the manifest to a git repository instead of applying to the cluster directly. From there, it is expected that a git syncing tool is running on the clusters which will synchronize its state.

The repo also contains a [cloudbuild.yaml](./cloudbuild.yaml) to build the images and deploy to Artifact Registry. An optional [GitHub Actions workflow](.github/workflows/submit-build.yaml) is provided to trigger the Cloud Build job.

## Usage
1. Clone this repository

1. Setup build pipeline, as documented under [Installation](#installation).

1. Push a commit to the repo and confirm that Cloud Build job executes and a containerized image is pushed to Artifact Registry.

## Installation

### 1. Deploy Rollout Manager Module

1. Deploy the [Rollout Manager terraform module TODO: Add link to repo](). This process creates the Cloud Deploy pipeline and sets up the necessary GCP infrastructure, including an Artifact Registry repository where the Git Deployer image will be stored.

### 2. Prepare GCP Project for Cloud Build

1. Create service account for Cloud Build with the following roles:
   ```
   roles/artifactregistry.writer
   roles/logging.logWriter
   roles/storage.objectViewer
   ```

1. Add roles to IAM member submitting the Cloud Build job, e.g. the GitHub Actions workflow
   ```
   roles/cloudbuild.builds.editor
   roles/storage.admin
   roles/iam.serviceAccountUser
   ```

### Update Build Paramters

1. Determine whether the Cloud Build job to build the Git Deployer container image will be triggered via a GitHub Actions workflow or by Cloud Build's [GitHub Trigger](https://cloud.google.com/build/docs/automating-builds/github/build-repos-from-github?generation=2nd-gen).

    * If Cloud Build [GitHub Trigger](https://cloud.google.com/build/docs/automating-builds/github/build-repos-from-github?generation=2nd-gen) is used, delete the [.github](.github/) folder to remove the included Github Actions workflow.


1. Update build parameters

   * If a Cloud Build [GitHub Trigger](https://cloud.google.com/build/docs/automating-builds/github/build-repos-from-github?generation=2nd-gen) is used to trigger builds, update the substitution values found in [cloudbuild.yaml](./cloudbuild.yaml) to match your environment.

   * If GitHub Actions is used instead, modify the environment variable values defined in [GitHub Actions workflow](.github/workflows/submit-build-job.yaml) to match your setup.

      * **Note:** Use of [Workload Identity Federation (WIF)](https://cloud.google.com/iam/docs/workload-identity-federation) is recommended to provide the GitHub Actions workflow access to GCP APIs. If WIF cannot be used, modify the workflow to use a [service account key](https://github.com/google-github-actions/auth).


## Deploy
Git Deployer will perform the following steps when invoked by the Cloud Deploy pipeline:

1. Access the configured Secret Manager SecretVersion.

1. Clone the Git Repository and check out the source branch.

1. Open a feature branch.

   * Update the `source_of_truth.csv` file with values provided in the Cloud Deploy release.
   
   * Run the hydration script (included as part of the image).

   * Commit changes to branch.

1. If a destination branch is provided via `customTarget/gitDestinationBranch`:

    a. Open a pull request with the changes from the source branch to the destination branch. The pull request is merged if `customTarget/gitEnablePullRequestMerge` is `true`.

### Deploy Parameters

| Parameter | Required | Description |
| --- | --- | --- |
| platform-revision | No | Revision (Git tag, commit, or hash) of platform Root Sync - at least one of `platform-revision` and `workload-revision` must be set |
| workload-revision | No | Revision (Git tag, commit, or hash) of workload Root Sync - at least one of `platform-revision` and `workload-revision` must be set |
| match-clusters-having-any-listed-tags | No | Match clusters that have any tag in this comma-separated list |
| match-clusters-having-all-listed-tags | No | Match clusters that match all tags in this comma-separated list |
| customTarget/gitSourceRepo | Yes | The URI of the Git repository, e.g. "github.com/{owner}/{repository}" |
| customTarget/gitSourceBranch | Yes | The branch used for committing changes |
| customTarget/gitOutputRepo | Yes | The URI of the Git repository, e.g. "github.com/{owner}/{repository}" |
| customTarget/gitOutputBranch | Yes | The branch used for committing changes |
| customTarget/gitSecret | Yes | The name of the Secret Manager SecretVersion resource used for cloning the Git repository and optionally opening pull requests, e.g. "projects/{project-number}/secrets/{secret-name}/versions/{version-number}" |
| customTarget/gitPath | No | Relative path from the repository root where the manifest will be written. If not provided then defaults to the root of the repository with the file name "manifest.yaml" |
| customTarget/gitUsername | No | The committer username, if not provided then defaults to "Cloud Deploy" |
| customTarget/gitEmail | No | The committer email, if not provided then the email is left empty |
| customTarget/gitCommitMessage | No | The commit message to use, if not provided then defaults to: "Delivery Pipeline: {pipeline-id} Release: {release-id} Rollout: {rollout-id}" |
| customTarget/gitDestinationBranch | No | The branch a pull request will be opened against, if not provided then no pull request is opened and the deploy completes upon the commit and push to the source branch |
| customTarget/gitPullRequestTitle | No | The title of the pull request, if not provided then defaults to "Cloud Deploy: Release {release-id}, Rollout {rollout-id}" |
| customTarget/gitPullRequestBody | No | The body of the pull request, if not provided then defaults to "Project: {project-num} Location: {location} Delivery Pipeline: {pipeline-id} Target: {target-id} Release: {release-id} Rollout: {rollout-id}" |
| customTarget/gitEnablePullRequestMerge | No | Whether to merge the pull request opened against the `gitDestinationBRanch` |
| customTarget/hydrationClusterGroup | No | placeholder |
| customTarget/hydrationBatchSize | No | placeholder |
| customTarget/hydrationWaitTimeBetweenBatches | No | placeholder |
| customTarget/hydrationSourceOfTruth | No | placeholder |
| customTarget/hydrationBaseDir | No | placeholder |
| customTarget/hydrationOverlayDir | No | placeholder |
| customTarget/hydrationOutputDir | No | placeholder |
| customTarget/hydrationOutputDir | No | placeholder |

## Development

### Building Docker Container
**Note:** The provided `Dockerfile` uses the latest hydration tool's image as the base image. Use build arguments to optionally customize both the version and the repo location

```shell
docker build -t git-deploy

# optionally, customize hydration tool base image
docker build -t git-deploy \
  --build-arg HYDRATOR_IMAGE=<IMAGE_LOCATION> \
  --build-arg HYDRATOR_VERSION=<IMAGE_VERSION>
```

### Run Unit Tests
Currently, there is no provided way to run the `git-deploy` docker image  outside of a Cloud Deploy context due to dependencies. As such, it is recommended to maintain and leverage unit tests as much as possible.

```shell
go test
```