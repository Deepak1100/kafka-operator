// Copyright © 2020 Banzai Cloud
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

package tests

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	// logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/banzaicloud/kafka-operator/api/v1beta1"
	"github.com/banzaicloud/kafka-operator/pkg/util"
)

var _ = Describe("KafkaCluster", func() {
	var (
		count        uint64 = 0
		namespace    string
		namespaceObj *corev1.Namespace
		kafkaCluster *v1beta1.KafkaCluster
	)

	BeforeEach(func() {
		atomic.AddUint64(&count, 1)

		namespace = fmt.Sprintf("kafka-%v", count)
		namespaceObj = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		kafkaCluster = createMinimalKafkaClusterCR(fmt.Sprintf("kafkacluster-%d", count), namespace)
		kafkaCluster.Spec.ListenersConfig.ExternalListeners[0].HostnameOverride = ""
		kafkaCluster.Spec.CruiseControlConfig = v1beta1.CruiseControlConfig{
			TopicConfig: &v1beta1.TopicConfig{
				Partitions:        7,
				ReplicationFactor: 2,
			},
			Config: "some.config=value",
		}
		kafkaCluster.Spec.ReadOnlyConfig = ""
		// Set some Kafka pod and container related SecurityContext values
		defaultGroup := kafkaCluster.Spec.BrokerConfigGroups[defaultBrokerConfigGroup]
		defaultGroup.PodSecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: util.BoolPointer(false),
		}
		defaultGroup.SecurityContext = &corev1.SecurityContext{
			Privileged: util.BoolPointer(true),
		}
		kafkaCluster.Spec.BrokerConfigGroups[defaultBrokerConfigGroup] = defaultGroup
		// Set some CruiseControl pod and container related SecurityContext values
		kafkaCluster.Spec.CruiseControlConfig.PodSecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: util.BoolPointer(false),
		}
		kafkaCluster.Spec.CruiseControlConfig.SecurityContext = &corev1.SecurityContext{
			Privileged: util.BoolPointer(true),
		}
	})

	JustBeforeEach(func() {
		By("creating namespace " + namespace)
		err := k8sClient.Create(context.TODO(), namespaceObj)
		Expect(err).NotTo(HaveOccurred())

		By("creating kafka cluster object " + kafkaCluster.Name + " in namespace " + namespace)
		err = k8sClient.Create(context.TODO(), kafkaCluster)
		Expect(err).NotTo(HaveOccurred())

		// assign host to envoy LB
		envoyLBService := &corev1.Service{}
		Eventually(func() error {
			return k8sClient.Get(context.TODO(), types.NamespacedName{
				Name:      fmt.Sprintf("envoy-loadbalancer-test-%s", kafkaCluster.Name),
				Namespace: namespace,
			}, envoyLBService)
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		envoyLBService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
			Hostname: "test.host.com",
		}}

		// logf.Log.V(-1).Info("envoy service updated", "spec", envoyLBService)
		err = k8sClient.Status().Update(context.TODO(), envoyLBService)
		Expect(err).NotTo(HaveOccurred())
	})

	JustAfterEach(func() {
		// in the tests the CC topic might not get deleted

		By("deleting Kafka cluster object " + kafkaCluster.Name + " in namespace " + namespace)
		err := k8sClient.Delete(context.TODO(), kafkaCluster)
		Expect(err).NotTo(HaveOccurred())

		kafkaCluster = nil
	})
	When("using default configuration", func() {
		JustBeforeEach(func() {
			waitForClusterRunningState(kafkaCluster, namespace)
		})
		It("should reconciles objects properly", func() {
			expectEnvoy(kafkaCluster)
			expectKafkaMonitoring(kafkaCluster)
			expectCruiseControlMonitoring(kafkaCluster)
			expectKafka(kafkaCluster)
			expectCruiseControl(kafkaCluster)
		})
	})
	Context("creating an another external listener", func() {
		BeforeEach(func() {
			kafkaCluster.Spec.ListenersConfig.ExternalListeners =
				append(kafkaCluster.Spec.ListenersConfig.ExternalListeners, v1beta1.ExternalListenerConfig{
					CommonListenerSpec: v1beta1.CommonListenerSpec{
						Type:          "plaintext",
						Name:          "external",
						ContainerPort: 9095,
					},
					AccessMethod:         corev1.ServiceTypeLoadBalancer,
					ExternalStartingPort: 19095,
				})
		})
		JustBeforeEach(func() {
			// assign host to envoy LB
			envoyTestLBService := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      fmt.Sprintf("envoy-loadbalancer-external-%s", kafkaCluster.Name),
					Namespace: namespace,
				}, envoyTestLBService)
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			envoyTestLBService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
				Hostname: "external.host.com",
			}}

			// logf.Log.V(-1).Info("envoy service updated", "spec", envoyTestLBService)
			err := k8sClient.Status().Update(context.TODO(), envoyTestLBService)
			Expect(err).NotTo(HaveOccurred())

			waitForClusterRunningState(kafkaCluster, namespace)
		})
		When("using empty external listener bindings", func() {
			It("should handle reconciles objects properly", func() {
				expectEmptyExternalListenerBindings(kafkaCluster)
			})
		})
		When("using external listener bindings from broker group config", func() {
			BeforeEach(func() {
				defaultBConfigGroup := kafkaCluster.Spec.BrokerConfigGroups["default"]
				defaultBConfigGroup.ExternalListenerBindings = []string{"test"}
				kafkaCluster.Spec.BrokerConfigGroups["default"] = defaultBConfigGroup
			})
			It("should handle object reconcile properly", func() {
				expectExternalListenerGroupBindings(kafkaCluster)
			})
		})
		When("using external listener binding list from direct broker config", func() {
			BeforeEach(func() {
				kafkaCluster.Spec.Brokers[0].BrokerConfig = &v1beta1.BrokerConfig{ExternalListenerBindings: []string{"external"}}
				kafkaCluster.Spec.Brokers[1].BrokerConfig = &v1beta1.BrokerConfig{ExternalListenerBindings: []string{"test"}}
			})
			It("should reconcile objects properly", func() {
				expectExternalListenerBindings(kafkaCluster, count)
			})
		})
	})
})

func expectKafkaMonitoring(kafkaCluster *v1beta1.KafkaCluster) {
	configMap := corev1.ConfigMap{}
	configMapName := fmt.Sprintf("%s-kafka-jmx-exporter", kafkaCluster.Name)
	Eventually(func() error {
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: configMapName, Namespace: kafkaCluster.Namespace}, &configMap)
		return err
	}).Should(Succeed())

	Expect(configMap.Labels).To(And(HaveKeyWithValue("app", "kafka-jmx"), HaveKeyWithValue("kafka_cr", kafkaCluster.Name)))
	Expect(configMap.Data).To(HaveKeyWithValue("config.yaml", Not(BeEmpty())))
}

func expectCruiseControlMonitoring(kafkaCluster *v1beta1.KafkaCluster) {
	configMap := corev1.ConfigMap{}
	configMapName := fmt.Sprintf("%s-cc-jmx-exporter", kafkaCluster.Name)
	// logf.Log.Info("name", "name", configMapName)
	Eventually(func() error {
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: configMapName, Namespace: kafkaCluster.Namespace}, &configMap)
		return err
	}).Should(Succeed())

	Expect(configMap.Labels).To(And(HaveKeyWithValue("app", "cruisecontrol-jmx"), HaveKeyWithValue("kafka_cr", kafkaCluster.Name)))
	Expect(configMap.Data).To(HaveKeyWithValue("config.yaml", kafkaCluster.Spec.MonitoringConfig.CCJMXExporterConfig))
}
