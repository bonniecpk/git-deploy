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
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variable keys whose values determine the behavior of the Git deployer.
// Cloud Deploy transforms a deploy parameter "customTarget/gitRepo" into an
// environment variable of the form "CLOUD_DEPLOY_customTarget_gitRepo".
const (
	gitSourceRepoEnvKey                   = "CLOUD_DEPLOY_customTarget_gitSourceRepo"
	gitSourceBranchEnvKey                 = "CLOUD_DEPLOY_customTarget_gitSourceBranch"
	gitSecretEnvKey                       = "CLOUD_DEPLOY_customTarget_gitSecret"
	gitUsernameEnvKey                     = "CLOUD_DEPLOY_customTarget_gitUsername"
	gitEmailEnvKey                        = "CLOUD_DEPLOY_customTarget_gitEmail"
	gitCommitMessageEnvKey                = "CLOUD_DEPLOY_customTarget_gitCommitMessage"
	gitOutputRepoEnvKey                   = "CLOUD_DEPLOY_customTarget_gitOutputRepo"
	gitOutputBranchEnvKey                 = "CLOUD_DEPLOY_customTarget_gitOutputBranch"
	gitPullRequestTitleEnvKey             = "CLOUD_DEPLOY_customTarget_gitPullRequestTitle"
	gitPullRequestBodyEnvKey              = "CLOUD_DEPLOY_customTarget_gitPullRequestBody"
	gitEnablePullRequestMergeEnvKey       = "CLOUD_DEPLOY_customTarget_gitEnablePullRequestMerge"
	hydrationSourceOfTruthEnvKey          = "CLOUD_DEPLOY_customTarget_hydrationSourceOfTruth"
	hydrationBaseDirEnvKey                = "CLOUD_DEPLOY_customTarget_hydrationBaseDir"
	hydrationOverlayDirEnvKey             = "CLOUD_DEPLOY_customTarget_hydrationOverlayDir"
	hydrationOutputDirEnvKey              = "CLOUD_DEPLOY_customTarget_hydrationOutputDir"
	hydrationClusterGroupEnvKey           = "CLOUD_DEPLOY_customTarget_hydrationClusterGroup"
	hydrationBatchSizeEnvKey              = "CLOUD_DEPLOY_customTarget_hydrationBatchSize"
	hydrationWaitTimeBetweenBatchesEnvKey = "CLOUD_DEPLOY_customTarget_hydrationWaitTimeBetweenBatches"
	hydrationPlatformRevisionEnvKey       = "platform-revision"
	hydrationWorkloadRevisionEnvKey       = "workload-revision"

	matchClustersHavingAnyListedTagEnvKey  = "match-clusters-having-any-listed-tag"
	matchClustersHavingAllListedTagsEnvKey = "match-clusters-having-all-listed-tags"
)

const (
	// Default committer username when not provided.
	defaultUsername = "Cloud Deploy"

	// Default wait time between batches
	defaultWaitTimeBetweenBatches = 30 * time.Second

	// Default source of truth path
	defaultSourceOfTruth = "source_of_truth.csv"

	// Default base dir
	defaultBaseDir = "base_library/"

	// Default overlay dir
	defaultOverlayDir = "overlays/"

	// Default output dir
	defaultOutputDir = "output"
)

type params struct {
	// The URI of the source Git repository, e.g. "github.com/{owner}/{repository}".
	gitSourceRepo string
	// Target branch to read source information
	gitSourceBranch string
	// The name of the Secret Manager SecretVersion resource used for cloning the Git repository
	// and optionally opening pull requests.
	gitSecret string
	// The committer username. If not provided then defaults to "Cloud Deploy".
	gitUsername string
	// The commiter email. If not provided then the email address is left empty.
	gitEmail string
	// The commit message to use. If not provided then defaults to:
	// "Delivery Pipeline: {pipeline-id} Release: {release-id} Rollout: {rollout-id}"
	gitCommitMessage string
	// The URI of the source Git repository, e.g. "github.com/{owner}/{repository}".
	gitOutputRepo string
	// Target branch in output repository
	gitOutputBranch string
	// The title of the pull request. If not provided then defaults to:
	// "Cloud Deploy: Release {release-id}, Rollout {rollout-id}"
	gitPullRequestTitle string
	// The body of the pull request. If not provided then defaults to:
	// "Project: {project-num}
	//  Location: {location}
	// 	Delivery Pipeline: {pipeline-id}
	//  Target: {target-id}
	//	Release: {release-id}
	//	Rollout: {rollout-id}"
	gitPullRequestBody string
	// Whether to merge the pull request opened against the gitDestintionBranch.
	enablePullRequestMerge bool
	// Cluster Group of this target
	hydrationClusterGroup string
	// target platform revision being rolled out
	hydrationPlatformRevision string
	// target workload revision being rolled out
	hydrationWorkloadRevision string
	// number of clusters to include per batch
	hydrationBatchSize int
	// time to wait between batches
	hydrationWaitTimeBetweenBatches time.Duration
	// path to source of truth in source repository
	hydrationSourceOfTruth string
	// path to base templates
	hydrationBaseDir string
	// path to overlays
	hydrationOverlaysDir string
	// path to render outputs
	hydrationOutputDir string
	// match clusters having any of these tags
	matchClustersHavingAnyListedTag []string
	// match clusters having all of these tags
	matchClustersHavingAllListedTags []string
}

// determineParams returns the params provided in the execution environment via environment variables.
func determineParams() (*params, error) {
	params := &params{}
	// Required parameters:
	sourceRepo := os.Getenv(gitSourceRepoEnvKey)
	if len(sourceRepo) == 0 {
		return nil, fmt.Errorf("parameter %q is required", gitSourceRepoEnvKey)
	}
	params.gitSourceRepo = sourceRepo

	outputRepo := os.Getenv(gitOutputRepoEnvKey)
	if len(outputRepo) == 0 {
		return nil, fmt.Errorf("parameter %q is required", gitOutputRepoEnvKey)
	}
	params.gitOutputRepo = outputRepo

	secret := os.Getenv(gitSecretEnvKey)
	if len(secret) == 0 {
		return nil, fmt.Errorf("parameter %q is required", gitSecretEnvKey)
	}
	params.gitSecret = secret

	srcBranch := os.Getenv(gitSourceBranchEnvKey)
	if len(srcBranch) == 0 {
		return nil, fmt.Errorf("parameter %q is required", gitSourceBranchEnvKey)
	}
	params.gitSourceBranch = srcBranch

	outputBranch := os.Getenv(gitOutputBranchEnvKey)
	if len(outputBranch) == 0 {
		return nil, fmt.Errorf("parameter %q is required", gitOutputBranchEnvKey)
	}
	params.gitOutputBranch = outputBranch

	clusterGroup := os.Getenv(hydrationClusterGroupEnvKey)
	if len(clusterGroup) == 0 {
		return nil, fmt.Errorf("parameter %q is required", hydrationClusterGroupEnvKey)
	}
	params.hydrationClusterGroup = clusterGroup

	platformRevision := os.Getenv(hydrationPlatformRevisionEnvKey)
	workloadRevision := os.Getenv(hydrationWorkloadRevisionEnvKey)
	if len(platformRevision) == 0 && len(workloadRevision) == 0 {
		return nil, fmt.Errorf("at least one of parameter %q and %q is required", hydrationPlatformRevisionEnvKey, hydrationWorkloadRevisionEnvKey)
	}

	params.hydrationPlatformRevision = platformRevision
	params.hydrationWorkloadRevision = workloadRevision

	batchSize, err := strconv.Atoi(os.Getenv(hydrationBatchSizeEnvKey))
	if err != nil {
		return nil, fmt.Errorf("parameter %q is required", hydrationBatchSizeEnvKey)
	}

	params.hydrationBatchSize = batchSize

	waitTime := defaultWaitTimeBetweenBatches
	st := os.Getenv(hydrationWaitTimeBetweenBatchesEnvKey)
	if len(st) != 0 {
		var err error
		waitTime, err = time.ParseDuration(st)
		if err != nil {
			return nil, fmt.Errorf("failed to parse parameter %q: %v", hydrationWaitTimeBetweenBatchesEnvKey, err)
		}
	}

	params.hydrationWaitTimeBetweenBatches = waitTime

	// Optional parameters:
	params.gitUsername = os.Getenv(gitUsernameEnvKey)
	if len(params.gitUsername) == 0 {
		params.gitUsername = defaultUsername
	}

	params.hydrationSourceOfTruth = os.Getenv(hydrationSourceOfTruthEnvKey)
	if len(params.hydrationSourceOfTruth) == 0 {
		params.hydrationSourceOfTruth = defaultSourceOfTruth
	}

	params.hydrationBaseDir = os.Getenv(hydrationBaseDirEnvKey)
	if len(params.hydrationBaseDir) == 0 {
		params.hydrationBaseDir = defaultBaseDir
	}

	params.hydrationOverlaysDir = os.Getenv(hydrationOverlayDirEnvKey)
	if len(params.hydrationOverlaysDir) == 0 {
		params.hydrationOverlaysDir = defaultOverlayDir
	}

	params.hydrationOutputDir = os.Getenv(hydrationOutputDirEnvKey)
	if len(params.hydrationOutputDir) == 0 {
		params.hydrationOutputDir = defaultOutputDir
	}

	params.gitEmail = os.Getenv(gitEmailEnvKey)
	params.gitCommitMessage = os.Getenv(gitCommitMessageEnvKey)
	params.gitPullRequestTitle = os.Getenv(gitPullRequestTitleEnvKey)
	params.gitPullRequestBody = os.Getenv(gitPullRequestBodyEnvKey)

	enablePRMerge := false
	prm, ok := os.LookupEnv(gitEnablePullRequestMergeEnvKey)
	if ok {
		var err error
		enablePRMerge, err = strconv.ParseBool(prm)
		if err != nil {
			return nil, fmt.Errorf("failed to parse parameter %q: %v", gitEnablePullRequestMergeEnvKey, err)
		}
	}
	params.enablePullRequestMerge = enablePRMerge

	params.matchClustersHavingAnyListedTag = []string{}
	anyListedTagValue := os.Getenv(matchClustersHavingAnyListedTagEnvKey)
	if len(anyListedTagValue) > 0 && anyListedTagValue != "" {
		params.matchClustersHavingAnyListedTag = strings.Split(anyListedTagValue, ",")
	}

	params.matchClustersHavingAllListedTags = []string{}
	allListedTagValue := os.Getenv(matchClustersHavingAllListedTagsEnvKey)
	if len(matchClustersHavingAllListedTagsEnvKey) > 0 && allListedTagValue != "" {
		params.matchClustersHavingAllListedTags = strings.Split(allListedTagValue, ",")
	}

	return params, nil
}
