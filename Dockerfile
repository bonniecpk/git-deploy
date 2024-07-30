# Copyright 2023 Google LLC

# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at

#     https://www.apache.org/licenses/LICENSE-2.0

# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG GCLOUD_VERSION=456.0.0
ARG GO_VERSION=1.21

# TODO: update with McD repo
ARG HYDRATOR_IMAGE=us-docker.pkg.dev/kwpark1-test-123/mcd-tools/hydrator
ARG HYDRATOR_VERSION=latest

FROM golang:${GO_VERSION} AS go-build
ARG COMMIT_SHA=unknown
WORKDIR /app
COPY go.mod go.sum ./
COPY *.go ./
COPY providers/*.go ./providers/
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-X github.com/GoogleCloudPlatform/cloud-deploy-samples/custom-targets/util/clouddeploy.GitCommit=${COMMIT_SHA}" -o /git-deployer

FROM ${HYDRATOR_IMAGE}:${HYDRATOR_VERSION} AS release
RUN apk add git
COPY --from=go-build /git-deployer /bin/git-deployer
# Override hydrator's entrypoint
ENTRYPOINT ["/usr/bin/env"]
CMD ["/bin/git-deployer"]
