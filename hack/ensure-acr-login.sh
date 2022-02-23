#!/usr/bin/env bash

# Copyright 2021 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail
set +o xtrace

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${REPO_ROOT}" || exit 1

if [[ "${REGISTRY:-}" =~ capzci\.azurecr\.io ]]; then
    # if we are using the prow Azure Container Registry, login.
    "${REPO_ROOT}/hack/ensure-azcli.sh"
    : "${AZURE_SUBSCRIPTION_ID:?Environment variable empty or not defined.}"
    az account set -s "${AZURE_SUBSCRIPTION_ID}"
    az acr login --name capzci
    # TODO(mainred): When using ACR, `az acr login` impacts the authentication of `docker buildx build --push` when the
    # ACR, capzci in our case, has anonymous pull enabled.
    # Use `docker login` as a suggested workaround and remove this target when the issue is resolved.
    # Issue link: https://github.com/Azure/acr/issues/582
    # Failed building link: https://prow.k8s.io/view/gs/kubernetes-jenkins/pr-logs/pull/kubernetes-sigs_cloud-provider-azure/974/pull-cloud-provider-azure-e2e-ccm-capz/1480459040440979456
    docker login -u "${AZURE_CLIENT_ID}" -p "${AZURE_CLIENT_SECRET}" capzci.azurecr.io
else
    docker login -u "${AZURE_CLIENT_ID}" -p "${AZURE_CLIENT_SECRET}" ${REGISTRY}
fi
