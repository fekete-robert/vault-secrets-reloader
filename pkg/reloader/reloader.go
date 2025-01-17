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
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) runReloader(ctx context.Context) {
	reloaderLogger := c.logger.WithContext(ctx).WithField("worker", "reloader")
	reloaderLogger.Info("Reloader started")

	if len(c.workloadSecrets.GetWorkloadSecretsMap()) == 0 {
		reloaderLogger.Info("No workloads to reload")
		return
	}

	err := c.initVaultClient()
	if err != nil {
		reloaderLogger.Error("failed to initialize Vault client: ", err)
		return
	}

	// Create a secretWorkloads map and compare the currently used secrets' version
	// with the one stored in the secretVersions map, while creating a new secretVersions map
	workloadsToReload := make(map[workload]bool)
	newSecretVersions := make(map[string]int)
	for secretPath, workloads := range c.workloadSecrets.GetSecretWorkloadsMap() {
		reloaderLogger.Debug("Checking secret: ", secretPath)
		// Get current secret version
		currentVersion, err := getSecretVersionFromVault(c.vaultClient.Logical(), secretPath)
		if err != nil {
			switch err.(type) {
			case ErrSecretNotFound:
				if !c.vaultConfig.IgnoreMissingSecrets {
					reloaderLogger.Error(err)
				}
				if c.vaultConfig.IgnoreMissingSecrets {
					reloaderLogger.Warnf(
						"Path not found: %s - We couldn't find a secret path. This is not an error since missing secrets can be ignored according to the configuration you've set (env: VAULT_IGNORE_MISSING_SECRETS).",
						secretPath,
					)
				}
				continue

			default:
				reloaderLogger.Error("failed to get secret version from Vault: ", err)
				continue
			}
		}

		// Compare current version with the secretVersions map
		if c.secretVersions[secretPath] == 0 {
			reloaderLogger.Debugf("Secret %s not found in secretVersions map, creating it", secretPath)
			newSecretVersions[secretPath] = currentVersion
			continue
		}
		if c.secretVersions[secretPath] == currentVersion {
			reloaderLogger.Debugf("Secret %s did not change", secretPath)
			newSecretVersions[secretPath] = currentVersion
			continue
		}
		reloaderLogger.Debugf("Secret version stored: %d current: %d", c.secretVersions[secretPath], currentVersion)
		for _, workload := range workloads {
			workloadsToReload[workload] = true
		}
		newSecretVersions[secretPath] = currentVersion
	}

	// Reloading workloads
	for workload := range workloadsToReload {
		reloaderLogger.Info("Reloading workload: ", workload)
		err := c.reloadWorkload(workload)
		if err != nil {
			reloaderLogger.Error("failed reloading workload: ", workload, err)
		}
	}

	// Replace secretVersions map with the new one so we don't keep deleted secrets in the map
	c.secretVersions = newSecretVersions
	reloaderLogger.Debug("Updated secretVersions map: ", newSecretVersions)

	if len(workloadsToReload) == 0 {
		reloaderLogger.Info("No workloads to reload")
	}
}

func (c *Controller) reloadWorkload(workload workload) error {
	// Reload object based on its type
	switch workload.kind {
	case DeploymentKind:
		deployment, err := c.kubeClient.AppsV1().Deployments(workload.namespace).Get(context.Background(), workload.name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		incrementReloadCountAnnotation(&deployment.Spec.Template)

		_, err = c.kubeClient.AppsV1().Deployments(workload.namespace).Update(context.Background(), deployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}

	case DaemonSetKind:
		daemonSet, err := c.kubeClient.AppsV1().DaemonSets(workload.namespace).Get(context.Background(), workload.name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		incrementReloadCountAnnotation(&daemonSet.Spec.Template)

		_, err = c.kubeClient.AppsV1().DaemonSets(workload.namespace).Update(context.Background(), daemonSet, metav1.UpdateOptions{})
		if err != nil {
			return err
		}

	case StatefulSetKind:
		statefulSet, err := c.kubeClient.AppsV1().StatefulSets(workload.namespace).Get(context.Background(), workload.name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		incrementReloadCountAnnotation(&statefulSet.Spec.Template)

		_, err = c.kubeClient.AppsV1().StatefulSets(workload.namespace).Update(context.Background(), statefulSet, metav1.UpdateOptions{})
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown object type: %s", workload.kind)
	}

	return nil
}

func incrementReloadCountAnnotation(podTemplate *corev1.PodTemplateSpec) {
	version := "1"

	if reloadCount := podTemplate.GetAnnotations()[ReloadCountAnnotationName]; reloadCount != "" {
		count, err := strconv.Atoi(reloadCount)
		if err == nil {
			count++
			version = strconv.Itoa(count)
		}
	}

	podTemplate.GetAnnotations()[ReloadCountAnnotationName] = version
}
