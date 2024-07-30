// Copyright 2023 Google LLC

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     https://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"cloud.google.com/go/storage"
	provider "github.com/GoogleCloudPlatform/cloud-deploy-samples/custom-targets/git-ops/git-deployer/providers"
	"github.com/GoogleCloudPlatform/cloud-deploy-samples/custom-targets/util/clouddeploy"
)

// deployer implements the requestHandler interface for deploy requests.
type deployer struct {
	req       *clouddeploy.DeployRequest
	params    *params
	gcsClient *storage.Client
	smClient  *secretmanager.Client
}

const branchPrefix = "deploy-"

// process processes a deploy request and uploads succeeded or failed results to GCS for Cloud Deploy.
func (d *deployer) process(ctx context.Context) error {
	fmt.Println("Processing deploy request")

	res, err := d.deploy(ctx)
	if err != nil {
		fmt.Printf("Deploy failed: %v\n", err)
		dr := &clouddeploy.DeployResult{
			ResultStatus:   clouddeploy.DeployFailed,
			FailureMessage: err.Error(),
			Metadata: map[string]string{
				clouddeploy.CustomTargetSourceMetadataKey:    gitDeployerSampleName,
				clouddeploy.CustomTargetSourceSHAMetadataKey: clouddeploy.GitCommit,
			},
		}
		fmt.Println("Uploading failed deploy results")
		rURI, err := d.req.UploadResult(ctx, d.gcsClient, dr)
		if err != nil {
			return fmt.Errorf("error uploading failed deploy results: %v", err)
		}
		fmt.Printf("Uploaded failed deploy results to %s\n", rURI)
		return err
	}

	fmt.Println("Uploading deploy results")
	rURI, err := d.req.UploadResult(ctx, d.gcsClient, res)
	if err != nil {
		return fmt.Errorf("error uploading deploy results: %v", err)
	}
	fmt.Printf("Uploaded deploy results to %s\n", rURI)
	return nil
}

// deploy performs the following steps:
//  1. Access the configured Secret Manager SecretVersion.
//  2. Clone the Git Repository
//  3. Determine the clusters that needs to be updated from the source of truth file
//  4. Group clusters into batches for processing. For each batch ...
//     a. Pull latest changes on main, and create a new branch
//     b. Update the cluster row(s) in SOT to match deployment parameters
//     c. Run `hydrate.py` to render cluster registry manifest for this specific cluster
//     d. Commit and push the changes.
//     e. Wait for the specified time before moving to the next batch
func (d *deployer) deploy(ctx context.Context) (*clouddeploy.DeployResult, error) {
	fmt.Printf("Accessing SecretVersion %s\n", d.params.gitSecret)
	s, err := d.accessSecretVersion(ctx, d.params.gitSecret)
	if err != nil {
		return nil, fmt.Errorf("unable to access git secret: %v", err)
	}
	fmt.Printf("Accessed SecretVersion %s\n", d.params.gitSecret)
	secret := string(s)

	sourceRepoParts := strings.Split(d.params.gitSourceRepo, "/")
	if len(sourceRepoParts) != 3 {
		return nil, fmt.Errorf("invalid git repository reference: %q", d.params.gitSourceRepo)
	}
	srcHostname, srcOwner, srcRepoName := sourceRepoParts[0], sourceRepoParts[1], sourceRepoParts[2]
	gitSourceRepo := newGitRepository(srcHostname, srcOwner, srcRepoName, d.params.gitEmail, d.params.gitUsername)
	if err := d.setupGitWorkspace(ctx, secret, gitSourceRepo, d.params.gitSourceBranch); err != nil {
		return nil, fmt.Errorf("unable to set up git workspace: %v", err)
	}

	var gitOutputRepo *gitRepository
	var outHostname, outOwner, outRepoName string

	// Check if hydrated manifests need to be output to a separate repo
	if d.params.gitSourceRepo != d.params.gitOutputRepo {
		outputRepoParts := strings.Split(d.params.gitOutputRepo, "/")
		if len(outputRepoParts) != 3 {
			return nil, fmt.Errorf("invalid git repository reference: %q", d.params.gitOutputRepo)
		}

		outHostname, outOwner, outRepoName = outputRepoParts[0], outputRepoParts[1], outputRepoParts[2]
		gitOutputRepo = newGitRepository(outHostname, outOwner, outRepoName, d.params.gitEmail, d.params.gitUsername)
		if err := d.setupGitWorkspace(ctx, secret, gitOutputRepo, d.params.gitOutputBranch); err != nil {
			return nil, fmt.Errorf("unable to set up git workspace: %v", err)
		}
	} else {
		gitOutputRepo = gitSourceRepo
	}

	fmt.Printf(
		"Determining clusters to update given inputs: cluster group is %s, match any tag is %v, and match all tags is %v\n",
		d.params.hydrationClusterGroup,
		d.params.matchClustersHavingAnyListedTag,
		d.params.matchClustersHavingAllListedTags)
	clustersToUpdate, err := determineClustersToUpdate(srcRepoName, d.params.hydrationSourceOfTruth, d.params.hydrationClusterGroup, d.params.matchClustersHavingAnyListedTag, d.params.matchClustersHavingAllListedTags)
	if err != nil {
		return nil, fmt.Errorf("Unable to determine clusters to be updated: %v", err)
	}
	fmt.Printf("Determined clusters to update: %v\n", clustersToUpdate)

	var batchSize int
	if d.params.hydrationBatchSize > 0 {
		batchSize = d.params.hydrationBatchSize
	} else {
		batchSize = len(clustersToUpdate)
	}
	numBatches := int(math.Ceil(float64(len(clustersToUpdate)) / float64(batchSize)))
	batchCounter := 1

	for i := 0; i < len(clustersToUpdate); i += batchSize {
		end := i + d.params.hydrationBatchSize
		if end > len(clustersToUpdate) {
			end = len(clustersToUpdate)
		}
		batch := clustersToUpdate[i:end]
		featureBranchName := fmt.Sprintf("%s__%d/%d", d.req.Rollout, batchCounter, numBatches)
		fmt.Printf("Processing batch %v with branch %s\n", batch, featureBranchName)

		if err := d.resetGitWorkspace(ctx, gitSourceRepo, d.params.gitSourceBranch, featureBranchName); err != nil {
			return nil, fmt.Errorf("unable to reset git workspace: %v", err)
		}

		if d.params.gitSourceRepo != d.params.gitOutputRepo {
			if err := d.resetGitWorkspace(ctx, gitOutputRepo, d.params.gitOutputBranch, featureBranchName); err != nil {
				return nil, fmt.Errorf("unable to reset git workspace: %v", err)
			}
		}

		if err := updatePlatformAndWorkloadRepositoryRevision(gitSourceRepo.repoName, batch, d.params.hydrationSourceOfTruth, d.params.hydrationPlatformRevision, d.params.hydrationWorkloadRevision); err != nil {
			return nil, fmt.Errorf("unable to update platform revision: %v", err)
		}

		if err := runHydrationCLI(gitSourceRepo.repoName, d.params.hydrationBaseDir, d.params.hydrationOverlaysDir, gitOutputRepo.repoName, d.params.hydrationOutputDir, d.params.hydrationSourceOfTruth); err != nil {
			return nil, fmt.Errorf("unable to hydrate: %v", err)
		}

		op, err := gitSourceRepo.detectDiff()
		if err != nil {
			return nil, fmt.Errorf("unable to run git status: %v", err)
		}

		if len(op) == 0 {
			return nil, fmt.Errorf("no diff detected between the rendered manifest and the manifest on branch %s", featureBranchName)
		}

		fmt.Printf("Committing and pushing source of truth changes to branch %s\n", featureBranchName)
		if err := d.commitPushGitWorkspace(ctx, gitSourceRepo, featureBranchName); err != nil {
			return nil, fmt.Errorf("unable to commit and push changes: %v", err)
		}

		if err := d.handleDestinationBranch(ctx, gitSourceRepo, secret, featureBranchName, d.params.gitOutputBranch); err != nil {
			return nil, err
		}

		if gitSourceRepo != gitOutputRepo {
			op, err := gitOutputRepo.detectDiff()
			if err != nil {
				return nil, fmt.Errorf("unable to run git status: %v", err)
			}

			if len(op) == 0 {
				return nil, fmt.Errorf("no diff detected between the rendered manifest and the manifest on branch %s", featureBranchName)
			}

			fmt.Printf("Committing and pushing hydrated files to branch %s\n", featureBranchName)
			if err := d.commitPushGitWorkspace(ctx, gitOutputRepo, featureBranchName); err != nil {
				return nil, fmt.Errorf("unable to commit and push changes: %v", err)
			}

			if err := d.handleDestinationBranch(ctx, gitOutputRepo, secret, featureBranchName, d.params.gitOutputBranch); err != nil {
				return nil, err
			}
		}

		batchCounter += 1
		time.Sleep(d.params.hydrationWaitTimeBetweenBatches)
		fmt.Printf("Completed processing batch %v with branch %s\n", batch, featureBranchName)
	}
	if err != nil {
		return nil, fmt.Errorf("Error processing cluster batches failed: %v", err)
	}
	fmt.Println("Completed processing all batches")

	fmt.Println("Uploading source of truth as a deploy artifact")
	dURI, err := d.req.UploadArtifact(ctx, d.gcsClient, "source_of_truth.csv", &clouddeploy.GCSUploadContent{LocalPath: filepath.Join(srcRepoName, d.params.hydrationSourceOfTruth)})
	if err != nil {
		return nil, fmt.Errorf("error uploading deploy artifact: %v", err)
	}
	fmt.Printf("Uploaded deploy artifact to %s\n", dURI)

	return &clouddeploy.DeployResult{
		ResultStatus:  clouddeploy.DeploySucceeded,
		ArtifactFiles: []string{dURI},
		Metadata: map[string]string{
			clouddeploy.CustomTargetSourceMetadataKey:    gitDeployerSampleName,
			clouddeploy.CustomTargetSourceSHAMetadataKey: clouddeploy.GitCommit,
		},
	}, nil
}

// accessSecretVersion downloads the Secret Manager SecretVersion, verifies the data checksum and
// provides the data payload.
func (d *deployer) accessSecretVersion(ctx context.Context, svName string) ([]byte, error) {
	res, err := d.smClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: svName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to access secret version %s: %v", svName, err)
	}

	crc32c := crc32.MakeTable(crc32.Castagnoli)
	checksum := int64(crc32.Checksum(res.Payload.Data, crc32c))
	if checksum != *res.Payload.DataCrc32C {
		return nil, fmt.Errorf("secret version response failed CRC-32 checksum validation; possible data corruption")
	}
	return res.Payload.Data, nil
}

// setupGitWorkspace clones the Git repository and checks out the configured source branch.
func (d *deployer) setupGitWorkspace(ctx context.Context, secret string, gitRepo *gitRepository, branch string) error {
	fmt.Printf("Cloning Git repository %s\n", gitRepo.repoName)
	if _, err := gitRepo.cloneRepo(secret); err != nil {
		return fmt.Errorf("failed to clone git repository %s: %v", gitRepo.repoName, err)
	}
	if err := gitRepo.config(); err != nil {
		return fmt.Errorf("failed setting up the git config in the git repository: %v", err)
	}
	fmt.Printf("Checking out branch %s\n", branch)
	if _, err := gitRepo.checkoutBranch(branch); err != nil {
		return fmt.Errorf("unable to checkout branch %s: %v", branch, err)
	}
	output, err := gitRepo.checkIfExists(branch)
	if err != nil {
		return fmt.Errorf("unable to check if branch %s exists: %v", branch, err)
	}
	if output != nil {
		if _, err := gitRepo.pull(branch); err != nil {
			return fmt.Errorf("unable to pull branch %s: %v", branch, err)
		}
	}
	return nil
}

// resetGitWorkspace checks out the configured source branch.
func (d *deployer) resetGitWorkspace(ctx context.Context, gitRepo *gitRepository, sourceBranch, featureBranch string) error {

	if err := gitRepo.config(); err != nil {
		return fmt.Errorf("failed setting up the git config in the git repository: %v", err)
	}

	// Checking out source branch and pulling latest
	fmt.Printf("Checking out branch %s\n", sourceBranch)
	if _, err := gitRepo.checkoutBranch(sourceBranch); err != nil {
		return fmt.Errorf("unable to checkout branch %s: %v", sourceBranch, err)
	}
	output, err := gitRepo.checkIfExists(sourceBranch)
	if err != nil {
		return fmt.Errorf("unable to check if branch %s exists: %v", sourceBranch, err)
	}
	if output != nil {
		if _, err := gitRepo.pull(sourceBranch); err != nil {
			return fmt.Errorf("unable to pull branch %s: %v", sourceBranch, err)
		}
	}

	// Now creating a new branch
	fmt.Printf("Checking out branch %s\n", featureBranch)
	if _, err := gitRepo.checkoutBranch(featureBranch); err != nil {
		return fmt.Errorf("unable to checkout branch %s: %v", featureBranch, err)
	}
	output, err = gitRepo.checkIfExists(featureBranch)
	if err != nil {
		return fmt.Errorf("unable to check if branch %s exists: %v", featureBranch, err)
	}
	if output != nil {
		if _, err := gitRepo.pull(featureBranch); err != nil {
			return fmt.Errorf("unable to pull branch %s: %v", featureBranch, err)
		}
	}
	return nil
}

// commitPushGitWorkspace commits and pushes changes in the local Git workspace to the source branch.
func (d *deployer) commitPushGitWorkspace(ctx context.Context, gitRepo *gitRepository, featureBranch string) error {
	if _, err := gitRepo.add(); err != nil {
		return fmt.Errorf("unable to git add changes: %v", err)
	}
	commitMsg := d.params.gitCommitMessage
	if len(commitMsg) == 0 {
		commitMsg = fmt.Sprintf("Delivery Pipeline: %s Release: %s Rollout: %s", d.req.Pipeline, d.req.Release, d.req.Rollout)
	}
	if _, err := gitRepo.commit(commitMsg); err != nil {
		return fmt.Errorf("unable to git commit changes: %v", err)
	}
	if _, err := gitRepo.push(featureBranch); err != nil {
		return fmt.Errorf("unable to git push changes to branch %s: %v", featureBranch, err)
	}
	return nil
}

// handleDestinationBranch opens a pull request on the destination branch if provided and will optionally
// merge the PR if configured.
func (d *deployer) handleDestinationBranch(ctx context.Context, gitRepo *gitRepository, secret string, featureBranchName string, destinationBranch string) error {
	// If no destination branch is provided then there is no need to open a pull request.
	if len(destinationBranch) == 0 {
		return nil
	}

	title := d.params.gitPullRequestTitle
	if len(title) == 0 {
		title = fmt.Sprintf("[Rollout Manager]: %s", featureBranchName)
	}
	body := d.params.gitPullRequestBody
	if len(body) == 0 {
		body = fmt.Sprintf("Project: %s\nLocation: %s\nDelivery Pipeline: %s\nTarget: %s\nRelease: %s\nRollout: %s",
			d.req.Project,
			d.req.Location,
			d.req.Pipeline,
			d.req.Target,
			d.req.Release,
			d.req.Rollout,
		)
	}

	gitProvider, err := provider.CreateProvider(gitRepo.hostname, gitRepo.repoName, gitRepo.owner, secret)
	if err != nil {
		return fmt.Errorf("unable to create git provider: %v", err)
	}
	fmt.Printf("Opening pull request from %s to %s\n", featureBranchName, destinationBranch)
	pr, err := gitProvider.OpenPullRequest(featureBranchName, destinationBranch, title, body)
	if err != nil {
		return fmt.Errorf("unable to open pull request from %s to %s: %v", featureBranchName, destinationBranch, err)
	}

	if !d.params.enablePullRequestMerge {
		return nil
	}
	fmt.Println("Merging the pull request")
	_, err = gitProvider.MergePullRequest(pr.Number)
	if err != nil {
		return fmt.Errorf("unable to merge pull request %d: %v", pr.Number, err)
	}

	return nil
}

type FieldsNotFoundError struct {
	Fields []string
}

func (e *FieldsNotFoundError) Error() string {
	return fmt.Sprintf("fields %q not found in header", e.Fields)
}

func findFieldIndices(header []string, fields ...string) (map[string]int, error) {
	if len(header) == 0 {
		return nil, &FieldsNotFoundError{Fields: fields}
	}

	indices := make(map[string]int)
	missingFields := []string{}
	for _, field := range fields {
		found := false
		for i, h := range header {
			if h == field {
				indices[field] = i
				found = true
				break
			}
		}

		if !found {
			missingFields = append(missingFields, field)
		}
	}

	if len(missingFields) > 0 {
		return nil, &FieldsNotFoundError{Fields: missingFields}
	}
	return indices, nil
}

func matchesAnyTags(tags, matchTags []string) bool {
	if len(matchTags) == 0 {
		return false
	}
	return slices.ContainsFunc(tags, func(tag string) bool {
		return slices.Contains(matchTags, tag)
	})
}

func matchesAllTags(tags, matchTags []string) bool {
	if len(matchTags) == 0 {
		return false
	}
	for _, tag := range matchTags {
		if !slices.Contains(tags, tag) {
			return false
		}
	}
	return true
}

func determineClustersToUpdate(repo, sourceOfTruth, clusterGroup string, matchClustersHavingAnyListedTag, matchClustersHavingAllListedTags []string) ([]string, error) {
	filePath := filepath.Join(repo, sourceOfTruth)
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}

	fieldIndices, err := findFieldIndices(records[0], "cluster_name", "cluster_group", "cluster_tags")
	if err != nil {
		return nil, err
	}

	clusterNameIndex := fieldIndices["cluster_name"]
	clusterGroupIndex := fieldIndices["cluster_group"]
	clusterTagsIndex := fieldIndices["cluster_tags"]

	clustersToUpdate := []string{}
	for _, record := range records[1:] {
		if clusterGroup != record[clusterGroupIndex] {
			continue
		}

		clusterName := record[clusterNameIndex]
		if len(matchClustersHavingAnyListedTag) == 0 && len(matchClustersHavingAllListedTags) == 0 {
			clustersToUpdate = append(clustersToUpdate, clusterName)
			continue
		}

		clusterTags := strings.Split(strings.Trim(record[clusterTagsIndex], "\" "), ",")
		if len(matchClustersHavingAnyListedTag) > 0 {
			if matchesAnyTags(clusterTags, matchClustersHavingAnyListedTag) {
				clustersToUpdate = append(clustersToUpdate, clusterName)
			}
		} else {
			if matchesAllTags(clusterTags, matchClustersHavingAllListedTags) {
				clustersToUpdate = append(clustersToUpdate, clusterName)
			}
		}
	}

	return clustersToUpdate, nil
}

func updatePlatformAndWorkloadRepositoryRevision(repo string, clusterNames []string, sourceOfTruth, platformRevision, workloadRevision string) error {
	filePath := filepath.Join(repo, sourceOfTruth)

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("unable to open input file %s: %w", filePath, err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("error reading CSV records: %w", err)
	}

	fieldIndices, err := findFieldIndices(records[0], "cluster_name", "platform_repository_revision", "workload_repository_revision")
	if err != nil {
		return err
	}

	clusterNameIndex := fieldIndices["cluster_name"]
	platformRevisionIndex := fieldIndices["platform_repository_revision"]
	workloadRevisionIndex := fieldIndices["workload_repository_revision"]

	wFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file for writing: %w", err)
	}
	defer wFile.Close()

	writer := csv.NewWriter(wFile)
	defer writer.Flush()
	for _, record := range records {
		if slices.Contains(clusterNames, record[clusterNameIndex]) && len(platformRevision) > 0 {
			record[platformRevisionIndex] = platformRevision
		}

		if slices.Contains(clusterNames, record[clusterNameIndex]) && len(workloadRevision) > 0 {
			record[workloadRevisionIndex] = workloadRevision
		}

		err := writer.Write(record)
		if err != nil {
			return fmt.Errorf("error writing CSV record: %w", err)
		}
	}

	return nil
}

// delete all files/directories in provided "hydrated" directorydirec except .gitkeep file
func cleanHydratedDirectory(directory string) error {
	files, err := os.ReadDir(directory)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.Name() == ".gitkeep" {
			continue
		}

		path := filepath.Join(directory, file.Name())
		if file.IsDir() {
			err = os.RemoveAll(path)
			if err != nil {
				return err
			}
			continue
		}

		err := os.Remove(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func runHydrationCLI(sourceRepo, baseLibraryPath, overlayPath, outputRepo, outputPath, sourceOfTruth string) error {
	const hydrateCliBin = "hydrate"

	if outputRepo == "" {
		outputRepo = sourceRepo
	}
	cleanHydratedDirectory(filepath.Join(outputRepo, outputPath))

	args := []string{"-b", filepath.Join(sourceRepo, baseLibraryPath), "-o", filepath.Join(sourceRepo, overlayPath), "-y", filepath.Join(sourceRepo, outputPath), filepath.Join(sourceRepo, sourceOfTruth)}

	if _, err := runCmd(hydrateCliBin, args, "", true); err != nil {
		return err
	}

	if _, err := runCmd(hydrateCliBin, args, "", true); err != nil {
		return err
	}

	return nil
}
