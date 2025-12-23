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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	valkeyv1alpha1 "github.com/e3b0c442/valkey-operator/api/v1alpha1"
)

var _ = Describe("Valkey Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		const namespace = "default"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}
		valkey := &valkeyv1alpha1.Valkey{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Valkey")
			err := k8sClient.Get(ctx, typeNamespacedName, valkey)
			if err != nil && errors.IsNotFound(err) {
				resource := &valkeyv1alpha1.Valkey{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: valkeyv1alpha1.ValkeySpec{},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("cleaning up the Valkey resource")
			resource := &valkeyv1alpha1.Valkey{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			By("cleaning up the Service if it exists")
			service := &corev1.Service{}
			err = k8sClient.Get(ctx, typeNamespacedName, service)
			if err == nil {
				Expect(k8sClient.Delete(ctx, service)).To(Succeed())
			}

			By("cleaning up the StatefulSet if it exists")
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			if err == nil {
				Expect(k8sClient.Delete(ctx, statefulSet)).To(Succeed())
			}
		})

		It("should create a headless Service when it doesn't exist", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Service was created")
			service := &corev1.Service{}
			err = k8sClient.Get(ctx, typeNamespacedName, service)
			Expect(err).NotTo(HaveOccurred())
			Expect(service.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(service.Spec.Ports).To(HaveLen(1))
			Expect(service.Spec.Ports[0].Port).To(Equal(int32(6379)))
			Expect(service.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(6379)))
		})

		It("should set Progressing condition with CreatingService reason when creating Service", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Progressing condition is set")
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal("CreatingService"))
		})

		It("should create StatefulSet after Service exists", func() {
			By("Reconciling to create Service first")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to transition to CreatingStatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to create StatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet was created")
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(*statefulSet.Spec.Replicas).To(Equal(int32(1)))
			Expect(statefulSet.Spec.ServiceName).To(Equal(resourceName))
			Expect(statefulSet.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:latest"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Ports).To(HaveLen(1))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(6379)))
		})

		It("should set Progressing condition with CreatingStatefulSet reason when creating StatefulSet", func() {
			By("Reconciling to create Service first")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to transition to CreatingStatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to create StatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Progressing condition is updated")
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal("CreatingStatefulSet"))
		})

		It("should transition to WaitingForReady state when StatefulSet exists", func() {
			By("Reconciling to create Service")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling to transition to CreatingStatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling to create StatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to transition to WaitingForReady")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Progressing condition is updated to WaitingForReady")
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal("WaitingForReady"))
		})

		It("should set Available condition when StatefulSet is ready", func() {
			By("Reconciling to create Service")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling to transition to CreatingStatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling to create StatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling to transition to WaitingForReady")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("updating StatefulSet to be ready")
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			statefulSet.Status.Replicas = 1
			statefulSet.Status.ReadyReplicas = 1
			err = k8sClient.Status().Update(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again to check readiness")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Available condition is set and Progressing is False")
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available := findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should only perform one state transition per reconcile loop", func() {
			By("Reconciling the resource")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying only Service was created, not StatefulSet")
			service := &corev1.Service{}
			err = k8sClient.Get(ctx, typeNamespacedName, service)
			Expect(err).NotTo(HaveOccurred())

			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should be idempotent when reconciling existing resources", func() {
			By("Reconciling multiple times")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying only one Service exists")
			serviceList := &corev1.ServiceList{}
			err = k8sClient.List(ctx, serviceList)
			Expect(err).NotTo(HaveOccurred())
			valkeyServices := []corev1.Service{}
			for _, svc := range serviceList.Items {
				if svc.Name == resourceName && svc.Namespace == namespace {
					valkeyServices = append(valkeyServices, svc)
				}
			}
			Expect(valkeyServices).To(HaveLen(1))
		})

		It("should not reset state machine when Available is True", func() {
			By("Setting up a Valkey that is already Available")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Create Service and StatefulSet
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Set StatefulSet to ready and reconcile to set Available
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			originalServiceUID := statefulSet.UID
			statefulSet.Status.Replicas = 1
			statefulSet.Status.ReadyReplicas = 1
			err = k8sClient.Status().Update(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Available is True
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available := findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
			availableTransitionTime := available.LastTransitionTime

			// Reconcile again - should not reset to CreatingService
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Available is still True and Progressing is still False
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available = findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			// Verify StatefulSet still exists with same UID (not recreated)
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(statefulSet.UID).To(Equal(originalServiceUID))

			// Verify Available condition LastTransitionTime was preserved (not updated)
			Expect(available.LastTransitionTime).To(Equal(availableTransitionTime))
		})

		It("should preserve LastTransitionTime when condition status and reason don't change", func() {
			By("Setting a condition with a known LastTransitionTime")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Get the valkey resource
			err := k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())

			// Manually set a condition with a known LastTransitionTime (1 hour ago)
			oldTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			valkey.Status.Conditions = []metav1.Condition{
				{
					Type:               "TestCondition",
					Status:             metav1.ConditionTrue,
					Reason:             "TestReason",
					Message:            "Test message",
					LastTransitionTime: oldTime,
					ObservedGeneration: valkey.Generation,
				},
			}
			err = k8sClient.Status().Update(ctx, valkey)
			Expect(err).NotTo(HaveOccurred())

			// Wait a bit to ensure time would be different if we updated it
			time.Sleep(100 * time.Millisecond)

			// Set the same condition again with same status and reason
			controllerReconciler.setCondition(valkey, "TestCondition", metav1.ConditionTrue, "TestReason", "Test message")
			err = k8sClient.Status().Update(ctx, valkey)
			Expect(err).NotTo(HaveOccurred())

			// Get and verify LastTransitionTime was preserved
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			testCondition := findCondition(valkey.Status.Conditions, "TestCondition")
			Expect(testCondition).NotTo(BeNil())
			// LastTransitionTime should be the same as the old time (within a small margin for clock skew)
			Expect(testCondition.LastTransitionTime.Time).To(BeTemporally("~", oldTime.Time, 5*time.Second))
		})

		It("should handle StatefulSet deletion when Available is True", func() {
			By("Setting up a Valkey that is Available")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Create Service and StatefulSet and make it ready
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Set StatefulSet to ready
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			statefulSet.Status.Replicas = 1
			statefulSet.Status.ReadyReplicas = 1
			err = k8sClient.Status().Update(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Available is True
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available := findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))

			By("Deleting the StatefulSet externally")
			err = k8sClient.Delete(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling should detect deletion and reset state machine")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify state machine was reset
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available = findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionFalse))
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(reasonCreatingStatefulSet))

			By("Reconciling again should recreate the StatefulSet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify StatefulSet was recreated
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set Available=False when StatefulSet becomes not-ready", func() {
			By("Setting up a Valkey that is Available")
			controllerReconciler := &ValkeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Create Service and StatefulSet and make it ready
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Set StatefulSet to ready
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			statefulSet.Status.Replicas = 1
			statefulSet.Status.ReadyReplicas = 1
			err = k8sClient.Status().Update(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Available is True
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available := findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))

			By("Making StatefulSet not-ready")
			statefulSet.Status.ReadyReplicas = 0
			err = k8sClient.Status().Update(ctx, statefulSet)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling should detect not-ready and set Available=False")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Available is False and Progressing is True
			err = k8sClient.Get(ctx, typeNamespacedName, valkey)
			Expect(err).NotTo(HaveOccurred())
			available = findCondition(valkey.Status.Conditions, conditionTypeAvailable)
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionFalse))
			progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(reasonWaitingForReady))
		})
	})
})
