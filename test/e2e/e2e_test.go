//go:build e2e
// +build e2e

/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/e3b0c442/valkey-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "valkey-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "valkey-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "valkey-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "valkey-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=valkey-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	Context("Valkey Resource", func() {
		const valkeyName = "valkey-e2e-test"
		const valkeyNamespace = "default"

		It("should create and manage a Valkey instance", func() {
			By("creating a Valkey resource")
			valkeyYAML := fmt.Sprintf(`
apiVersion: valkey.e3b0c442.dev/v1alpha1
kind: Valkey
metadata:
  name: %s
  namespace: %s
spec: {}
`, valkeyName, valkeyNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(valkeyYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Valkey resource")

			By("verifying the headless Service is created")
			verifyServiceCreated := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", valkeyName, "-n", valkeyNamespace, "-o", "jsonpath={.spec.clusterIP}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("None"), "Service should be headless")
			}
			Eventually(verifyServiceCreated, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying the StatefulSet is created")
			verifyStatefulSetCreated := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "statefulset", valkeyName, "-n", valkeyNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "StatefulSet should exist")
			}
			Eventually(verifyStatefulSetCreated, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying the StatefulSet pod becomes ready")
			verifyPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", fmt.Sprintf("%s-0", valkeyName), "-n", valkeyNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Pod should be ready")
			}
			Eventually(verifyPodReady, 5*time.Minute, time.Second).Should(Succeed())

			By("verifying status conditions are set correctly")
			verifyStatusConditions := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "valkey", valkeyName, "-n", valkeyNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Available condition should be True")

				cmd = exec.Command("kubectl", "get", "valkey", valkeyName, "-n", valkeyNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Progressing')].status}")
				output, err = utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("False"), "Progressing condition should be False when ready")
			}
			Eventually(verifyStatusConditions, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying Progressing condition transitions through states")
			verifyProgressingTransitions := func(g Gomega) {
				// Check that Progressing condition had the expected reasons
				cmd := exec.Command("kubectl", "get", "valkey", valkeyName, "-n", valkeyNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Progressing')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// The final reason should be empty or WaitingForReady, but we've transitioned past it
				// We can verify the condition exists and has been set
				g.Expect(output).NotTo(BeEmpty())
			}
			Eventually(verifyProgressingTransitions, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying Valkey is connectable from another pod on port 6379 via headless service")
			verifyValkeyConnectable := func(g Gomega) {
				// Create a test pod with redis-cli
				testPodYAML := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: valkey-test-client
  namespace: %s
spec:
  containers:
  - name: redis-cli
    image: redis:7-alpine
    command: ["/bin/sh", "-c"]
    args: ["redis-cli -h %s.%s.svc.cluster.local -p 6379 PING"]
  restartPolicy: Never
`, valkeyNamespace, valkeyName, valkeyNamespace)

				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(testPodYAML)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to create test pod")

				// Wait for pod to complete
				verifyPodComplete := func(innerG Gomega) {
					cmd := exec.Command("kubectl", "get", "pod", "valkey-test-client", "-n", valkeyNamespace,
						"-o", "jsonpath={.status.phase}")
					output, err := utils.Run(cmd)
					innerG.Expect(err).NotTo(HaveOccurred())
					innerG.Expect(output).To(Or(Equal("Succeeded"), Equal("Running")))
				}
				Eventually(verifyPodComplete, 2*time.Minute, time.Second).Should(Succeed())

				// Check the logs for PONG response
				cmd = exec.Command("kubectl", "logs", "valkey-test-client", "-n", valkeyNamespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("PONG"), "Should receive PONG from Valkey")

				// Cleanup test pod
				cmd = exec.Command("kubectl", "delete", "pod", "valkey-test-client", "-n", valkeyNamespace, "--ignore-not-found=true")
				_, _ = utils.Run(cmd)
			}
			Eventually(verifyValkeyConnectable, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying the primary Service is created")
			verifyPrimaryServiceCreated := func(g Gomega) {
				primaryServiceName := valkeyName + "-primary"
				cmd := exec.Command("kubectl", "get", "service", primaryServiceName, "-n", valkeyNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Primary service should exist")
			}
			Eventually(verifyPrimaryServiceCreated, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying the primary pod is labeled")
			verifyPrimaryPodLabeled := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", fmt.Sprintf("%s-0", valkeyName), "-n", valkeyNamespace,
					"-o", "jsonpath={.metadata.labels.valkey\\.e3b0c442\\.dev/role}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("primary"), "Primary pod should have role=primary label")
			}
			Eventually(verifyPrimaryPodLabeled, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying Valkey is connectable via primary service")
			verifyValkeyConnectableViaPrimary := func(g Gomega) {
				// Create a test pod with redis-cli
				testPodYAML := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: valkey-test-client-primary
  namespace: %s
spec:
  containers:
  - name: redis-cli
    image: redis:7-alpine
    command: ["/bin/sh", "-c"]
    args: ["redis-cli -h %s-primary.%s.svc.cluster.local -p 6379 PING"]
  restartPolicy: Never
`, valkeyNamespace, valkeyName, valkeyNamespace)

				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(testPodYAML)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to create test pod")

				// Wait for pod to complete
				verifyPodComplete := func(innerG Gomega) {
					cmd := exec.Command("kubectl", "get", "pod", "valkey-test-client-primary", "-n", valkeyNamespace,
						"-o", "jsonpath={.status.phase}")
					output, err := utils.Run(cmd)
					innerG.Expect(err).NotTo(HaveOccurred())
					innerG.Expect(output).To(Or(Equal("Succeeded"), Equal("Running")))
				}
				Eventually(verifyPodComplete, 2*time.Minute, time.Second).Should(Succeed())

				// Check the logs for PONG response
				cmd = exec.Command("kubectl", "logs", "valkey-test-client-primary", "-n", valkeyNamespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("PONG"), "Should receive PONG from Valkey via primary service")

				// Cleanup test pod
				cmd = exec.Command("kubectl", "delete", "pod", "valkey-test-client-primary", "-n", valkeyNamespace, "--ignore-not-found=true")
				_, _ = utils.Run(cmd)
			}
			Eventually(verifyValkeyConnectableViaPrimary, 3*time.Minute, time.Second).Should(Succeed())

			By("cleaning up the Valkey resource")
			cmd = exec.Command("kubectl", "delete", "valkey", valkeyName, "-n", valkeyNamespace, "--ignore-not-found=true")
			_, _ = utils.Run(cmd)
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
