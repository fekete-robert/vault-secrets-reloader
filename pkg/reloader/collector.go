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
	"regexp"
	"slices"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
)

type workloadSecretsStore interface {
	Store(workload workload, secrets []string)
	Delete(workload workload)
	GetWorkloadSecretsMap() map[workload][]string
	GetSecretWorkloadsMap() map[string][]workload
}

type workload struct {
	name      string
	namespace string
	kind      string
}

type workloadSecrets struct {
	sync.RWMutex
	workloadSecretsMap map[workload][]string
}

func newWorkloadSecrets() workloadSecretsStore {
	return &workloadSecrets{
		workloadSecretsMap: make(map[workload][]string),
	}
}

func (w *workloadSecrets) Store(workload workload, secrets []string) {
	w.Lock()
	defer w.Unlock()
	w.workloadSecretsMap[workload] = secrets
}

func (w *workloadSecrets) Delete(workload workload) {
	w.Lock()
	defer w.Unlock()
	delete(w.workloadSecretsMap, workload)
}

func (w *workloadSecrets) GetWorkloadSecretsMap() map[workload][]string {
	return w.workloadSecretsMap
}

func (w *workloadSecrets) GetSecretWorkloadsMap() map[string][]workload {
	w.Lock()
	defer w.Unlock()
	secretWorkloads := make(map[string][]workload)
	for workload, secretPaths := range w.workloadSecretsMap {
		for _, secretPath := range secretPaths {
			secretWorkloads[secretPath] = append(secretWorkloads[secretPath], workload)
		}
	}
	return secretWorkloads
}

func (c *Controller) collectWorkloadSecrets(workload workload, template corev1.PodTemplateSpec) {
	collectorLogger := c.logger.WithField("worker", "collector")
	containers := []corev1.Container{}
	containers = append(containers, template.Spec.Containers...)
	containers = append(containers, template.Spec.InitContainers...)

	vaultSecretPaths := collectSecretsFromContainerEnvVars(containers)
	if len(vaultSecretPaths) == 0 {
		collectorLogger.Debug("No Vault secret paths found in container env vars")
		return
	}
	collectorLogger.Debugf("Vault secret paths found: %v", vaultSecretPaths)

	// Add workload and secrets to workloadSecrets map
	c.workloadSecrets.Store(workload, vaultSecretPaths)
	collectorLogger.Infof("Collected secrets from %s %s/%s", workload.kind, workload.namespace, workload.name)
}

func collectSecretsFromContainerEnvVars(containers []corev1.Container) []string {
	// Iterate through all environment variables and extract secrets
	var vaultSecrets []string
	for _, container := range containers {
		for _, env := range container.Env {
			// Skip if env var does not contain a vault secret or is a secret with pinned version
			if hasVaultPrefix(env.Value) && unversionedSecretValue(env.Value) {
				secret := regexp.MustCompile(`vault:(.*?)#`).FindStringSubmatch(env.Value)[1]
				if secret != "" {
					vaultSecrets = append(vaultSecrets, secret)
				}
			}
		}
	}

	// Remove duplicates
	slices.Sort(vaultSecrets)
	return slices.Compact(vaultSecrets)
}

// copied from bank-vaults/vault-secrets-webhook/pkg/webhook/common.go
func hasVaultPrefix(value string) bool {
	return strings.HasPrefix(value, "vault:") || strings.HasPrefix(value, ">>vault:")
}

// implementation based on bank-vaults/vault-secrets-webhook/internal/injector/injector.go
func unversionedSecretValue(value string) bool {
	split := strings.SplitN(value, "#", 3)
	return len(split) == 2
}
