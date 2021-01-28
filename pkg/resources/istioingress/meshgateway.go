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

package istioingress

import (
	"fmt"

	istioOperatorApi "github.com/banzaicloud/istio-operator/pkg/apis/istio/v1beta1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/banzaicloud/kafka-operator/api/v1beta1"
	"github.com/banzaicloud/kafka-operator/pkg/resources/templates"
	"github.com/banzaicloud/kafka-operator/pkg/util"
	istioingressutils "github.com/banzaicloud/kafka-operator/pkg/util/istioingress"
	kafkautils "github.com/banzaicloud/kafka-operator/pkg/util/kafka"
)

func (r *Reconciler) meshgateway(log logr.Logger, externalListenerConfig v1beta1.ExternalListenerConfig,
	ingressConfig v1beta1.IngressConfig, ingressConfigName string) runtime.Object {

	mgateway := &istioOperatorApi.MeshGateway{
		ObjectMeta: templates.ObjectMeta(
			fmt.Sprintf(istioingressutils.MeshGatewayNameTemplate, externalListenerConfig.Name, r.KafkaCluster.Name),
			labelsForIstioIngress(r.KafkaCluster.Name, externalListenerConfig.Name), r.KafkaCluster),
		Spec: istioOperatorApi.MeshGatewaySpec{
			MeshGatewayConfiguration: istioOperatorApi.MeshGatewayConfiguration{
				Labels:             labelsForIstioIngress(r.KafkaCluster.Name, externalListenerConfig.Name),
				ServiceAnnotations: ingressConfig.GetServiceAnnotations(),
				BaseK8sResourceConfigurationWithHPAWithoutImage: istioOperatorApi.BaseK8sResourceConfigurationWithHPAWithoutImage{
					ReplicaCount: util.Int32Pointer(ingressConfig.IstioIngressConfig.GetReplicas()),
					MinReplicas:  util.Int32Pointer(ingressConfig.IstioIngressConfig.GetReplicas()),
					MaxReplicas:  util.Int32Pointer(ingressConfig.IstioIngressConfig.GetReplicas()),
					BaseK8sResourceConfiguration: istioOperatorApi.BaseK8sResourceConfiguration{
						Resources:      ingressConfig.IstioIngressConfig.GetResources(),
						NodeSelector:   ingressConfig.IstioIngressConfig.NodeSelector,
						Tolerations:    ingressConfig.IstioIngressConfig.Tolerations,
						PodAnnotations: ingressConfig.IstioIngressConfig.Annotations,
					},
				},
				ServiceType: externalListenerConfig.GetServiceType(),
			},
			Ports: kafkautils.GetExposedServicePorts(externalListenerConfig,
				util.GetBrokerIdsFromStatusAndSpec(r.KafkaCluster.Status.BrokersState, r.KafkaCluster.Spec.Brokers, log),
				r.KafkaCluster.Spec, ingressConfigName, log, "tcp-"),
			Type: istioOperatorApi.GatewayTypeIngress,
		},
	}

	return mgateway
}
