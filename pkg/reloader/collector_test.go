// Copyright © 2023 Cisco
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reloader

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestWorkloadSecretsStore(t *testing.T) {
	store := newWorkloadSecrets()
	workload1 := workload{
		name:      "test",
		namespace: "default",
		kind:      "Deployment",
	}
	workload2 := workload{
		name:      "test2",
		namespace: "default",
		kind:      "DaemonSet",
	}

	// add workload secrets
	store.Store(workload1, []string{"secret/data/accounts/aws", "secret/data/mysql"})
	store.Store(workload2, []string{"secret/data/accounts/aws", "secret/data/docker"})

	// check if workload secrets are stored
	t.Run("GetWorkloadSecretsMap", func(t *testing.T) {
		assert.Equal(t,
			map[workload][]string{
				workload1: {"secret/data/accounts/aws", "secret/data/mysql"},
				workload2: {"secret/data/accounts/aws", "secret/data/docker"},
			},
			store.GetWorkloadSecretsMap(),
		)
	})

	t.Run("GetSecretWorkloadsMap", func(t *testing.T) {
		// check secret to workloads map creation
		secretWorkloadsMap := store.GetSecretWorkloadsMap()
		// comparing slices as order is not guaranteed
		assert.ElementsMatch(t, secretWorkloadsMap["secret/data/accounts/aws"], []workload{workload1, workload2})
		assert.ElementsMatch(t, secretWorkloadsMap["secret/data/mysql"], []workload{workload1})
		assert.ElementsMatch(t, secretWorkloadsMap["secret/data/docker"], []workload{workload2})
	})

	t.Run("delete from workloadSecrets map", func(t *testing.T) {
		// check workload secret deleting
		store.Delete(workload1)
		assert.Equal(t, map[workload][]string{
			workload2: {"secret/data/accounts/aws", "secret/data/docker"}}, store.GetWorkloadSecretsMap())
	})
}

func TestCollectSecretsFromContainerEnvVars(t *testing.T) {
	containers := []corev1.Container{
		{
			Name: "container1",
			Env: []corev1.EnvVar{
				// this should be ignored
				{
					Name:  "ENV1",
					Value: "value1",
				},
				// this should be present in the result only once
				{
					Name:  "AWS_SECRET_ACCESS_KEY",
					Value: "vault:secret/data/accounts/aws#AWS_SECRET_ACCESS_KEY",
				},
				// this should be present in the result
				{
					Name:  "MYSQL_PASSWORD",
					Value: "vault:secret/data/mysql#${.MYSQL_PASSWORD}",
				},
			},
		},
		{
			Name: "container2",
			Env: []corev1.EnvVar{
				// this should be ignored (no prefix)
				{
					Name:  "GCP_SECRET",
					Value: "secret/data/accounts/gcp#GCP_SECRET",
				},
				// this should be ignored (no secret value)
				{
					Name:  "AZURE_SECRET",
					Value: "vault:secret/data/accounts/azure",
				},
				// this should be present in the result only once
				{
					Name:  "AWS_SECRET_ACCESS_KEY",
					Value: "vault:secret/data/accounts/aws#AWS_SECRET_ACCESS_KEY",
				},
				// this should be ignored, as it is versioned
				{
					Name:  "DOCKER_REPO_PASSWORD",
					Value: "vault:secret/data/dockerrepo#${.DOCKER_REPO_PASSWORD}#1",
				},
			},
		},
	}

	result := collectSecretsFromContainerEnvVars(containers)

	assert.Equal(t, []string{"secret/data/accounts/aws", "secret/data/mysql"}, result)
}
