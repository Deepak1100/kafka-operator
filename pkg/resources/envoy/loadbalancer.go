// Copyright © 2019 Banzai Cloud
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

package envoy

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	corev1 "k8s.io/api/core/v1"

	"github.com/banzaicloud/kafka-operator/api/v1beta1"
	"github.com/banzaicloud/kafka-operator/pkg/resources/templates"
	"github.com/banzaicloud/kafka-operator/pkg/util"
	envoyutils "github.com/banzaicloud/kafka-operator/pkg/util/envoy"
	kafkautils "github.com/banzaicloud/kafka-operator/pkg/util/kafka"
)

// loadBalancer return a Loadbalancer service for Envoy
func (r *Reconciler) loadBalancer(log logr.Logger, extListener v1beta1.ExternalListenerConfig,
	ingressConfig v1beta1.IngressConfig, ingressConfigName string) runtime.Object {

	// Determine Service Name from the configuration
	var serviceName string
	if ingressConfigName == util.IngressConfigGlobalName {
		serviceName = fmt.Sprintf(envoyutils.EnvoyServiceName, extListener.Name, r.KafkaCluster.GetName())
	} else {
		serviceName = fmt.Sprintf(envoyutils.EnvoyServiceNameWithScope, extListener.Name, ingressConfigName,r.KafkaCluster.GetName())
	}

	exposedPorts := getExposedServicePorts(extListener,
		util.GetBrokerIdsFromStatusAndSpec(r.KafkaCluster.Status.BrokersState, r.KafkaCluster.Spec.Brokers, log),
		r.KafkaCluster.Spec, ingressConfigName, log)

	service := &corev1.Service{
		ObjectMeta: templates.ObjectMetaWithAnnotations(
			serviceName,
			labelsForEnvoyIngress(r.KafkaCluster.GetName(), extListener.Name),
			ingressConfig.IngressServiceSettings.GetServiceAnnotations(), r.KafkaCluster),
		Spec: corev1.ServiceSpec{
			Selector:                 labelsForEnvoyIngress(r.KafkaCluster.GetName(), extListener.Name),
			Type:                     corev1.ServiceTypeLoadBalancer,
			Ports:                    exposedPorts,
			LoadBalancerSourceRanges: ingressConfig.EnvoyConfig.GetLoadBalancerSourceRanges(),
			LoadBalancerIP:           ingressConfig.EnvoyConfig.LoadBalancerIP,
			ExternalTrafficPolicy:    ingressConfig.ExternalTrafficPolicy,
		},
	}
	return service
}

func getExposedServicePorts(extListener v1beta1.ExternalListenerConfig, brokersIds []int,
	kafkaClusterSpec v1beta1.KafkaClusterSpec, ingressConfigName string,log logr.Logger) []corev1.ServicePort {
	var exposedPorts []corev1.ServicePort
	for _, brokerId := range brokersIds {
		brokerConfig, err := util.GetBrokerConfig(kafkaClusterSpec.Brokers[brokerId], kafkaClusterSpec)
		if err != nil {
			log.Error(err, "could not determine brokerConfig")
		}
		if len(brokerConfig.BrokerIdBindings) == 0 ||
			util.StringSliceContains(brokerConfig.BrokerIdBindings, ingressConfigName){

			exposedPorts = append(exposedPorts, corev1.ServicePort{
				Name:       fmt.Sprintf("broker-%d", brokerId),
				Port:       extListener.ExternalStartingPort + int32(brokerId),
				TargetPort: intstr.FromInt(int(extListener.ExternalStartingPort) + brokerId),
				Protocol:   corev1.ProtocolTCP,
			})
		}
	}

	exposedPorts = append(exposedPorts, corev1.ServicePort{
		Name:       fmt.Sprintf(kafkautils.AllBrokerServiceTemplate, "tcp"),
		TargetPort: intstr.FromInt(int(extListener.GetAnyCastPort())),
		Port:       extListener.GetAnyCastPort(),
	})

	return exposedPorts
}
