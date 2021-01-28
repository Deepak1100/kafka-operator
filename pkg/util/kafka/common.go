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

package kafka

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/banzaicloud/kafka-operator/api/v1alpha1"
	"github.com/banzaicloud/kafka-operator/api/v1beta1"
	"github.com/banzaicloud/kafka-operator/pkg/util"
)

const (
	// AllBrokerServiceTemplate template for Kafka headless service
	AllBrokerServiceTemplate = "%s-all-broker"
	// HeadlessServiceTemplate template for Kafka headless service
	HeadlessServiceTemplate = "%s-headless"

	// property name in the ConfigMap's Data field for the broker configuration
	ConfigPropertyName            = "broker-config"
	securityProtocolMapConfigName = "listener.security.protocol.map"
)

// PerBrokerConfigs configurations will not trigger rolling upgrade when updated
var PerBrokerConfigs = []string{
	// currently hardcoded in configmap.go
	"ssl.client.auth",

	// listener related config change will trigger rolling upgrade anyways due to pod spec change
	"listeners",
	"advertised.listeners",

	securityProtocolMapConfigName,
}

// LabelsForKafka returns the labels for selecting the resources
// belonging to the given kafka CR name.
func LabelsForKafka(name string) map[string]string {
	return map[string]string{"app": "kafka", "kafka_cr": name}
}

// commonAclString is the raw representation of an ACL allowing Describe on a Topic
var commonAclString = "User:%s,Topic,%s,%s,Describe,Allow,*"

// createAclString is the raw representation of an ACL allowing Create on a Topic
var createAclString = "User:%s,Topic,%s,%s,Create,Allow,*"

// writeAclString is the raw representation of an ACL allowing Write on a Topic
var writeAclString = "User:%s,Topic,%s,%s,Write,Allow,*"

// reacAclString is the raw representation of an ACL allowing Read on a Topic
var readAclString = "User:%s,Topic,%s,%s,Read,Allow,*"

// readGroupAclString is the raw representation of an ACL allowing Read on ConsumerGroups
var readGroupAclString = "User:%s,Group,LITERAL,*,Read,Allow,*"

// GrantsToACLStrings converts a user DN and a list of topic grants to raw strings
// for a CR status
func GrantsToACLStrings(dn string, grants []v1alpha1.UserTopicGrant) []string {
	acls := make([]string, 0)
	for _, x := range grants {
		if x.PatternType == "" {
			x.PatternType = v1alpha1.KafkaPatternTypeDefault
		}
		patternType := strings.ToUpper(string(x.PatternType))
		cmn := fmt.Sprintf(commonAclString, dn, patternType, x.TopicName)
		if !util.StringSliceContains(acls, cmn) {
			acls = append(acls, cmn)
		}
		switch x.AccessType {
		case v1alpha1.KafkaAccessTypeRead:
			readAcl := fmt.Sprintf(readAclString, dn, patternType, x.TopicName)
			readGroupAcl := fmt.Sprintf(readGroupAclString, dn)
			for _, y := range []string{readAcl, readGroupAcl} {
				if !util.StringSliceContains(acls, y) {
					acls = append(acls, y)
				}
			}
		case v1alpha1.KafkaAccessTypeWrite:
			createAcl := fmt.Sprintf(createAclString, dn, patternType, x.TopicName)
			writeAcl := fmt.Sprintf(writeAclString, dn, patternType, x.TopicName)
			for _, y := range []string{createAcl, writeAcl} {
				if !util.StringSliceContains(acls, y) {
					acls = append(acls, y)
				}
			}
		}
	}
	return acls
}

func ShouldRefreshOnlyPerBrokerConfigs(currentConfigs, desiredConfigs map[string]string, log logr.Logger) bool {
	touchedConfigs := collectTouchedConfigs(currentConfigs, desiredConfigs, log)

	if _, ok := touchedConfigs[securityProtocolMapConfigName]; ok {
		if listenersSecurityProtocolChanged(currentConfigs, desiredConfigs) {
			return false
		}
	}

	for _, perBrokerConfig := range PerBrokerConfigs {
		delete(touchedConfigs, perBrokerConfig)
	}

	return len(touchedConfigs) == 0
}

// Security protocol cannot be updated for existing listener
// a rolling upgrade should be triggered in this case
func listenersSecurityProtocolChanged(currentConfigs, desiredConfigs map[string]string) bool {
	// added or deleted config is ok
	if currentConfigs[securityProtocolMapConfigName] == "" || desiredConfigs[securityProtocolMapConfigName] == "" {
		return false
	}
	currentListenerProtocolMap := make(map[string]string)
	for _, listenerConfig := range strings.Split(currentConfigs[securityProtocolMapConfigName], ",") {
		listenerProtocol := strings.Split(listenerConfig, ":")
		if len(listenerProtocol) != 2 {
			continue
		}
		currentListenerProtocolMap[strings.TrimSpace(listenerProtocol[0])] = strings.TrimSpace(listenerProtocol[1])
	}
	for _, listenerConfig := range strings.Split(desiredConfigs[securityProtocolMapConfigName], ",") {
		desiredListenerProtocol := strings.Split(listenerConfig, ":")
		if len(desiredListenerProtocol) != 2 {
			continue
		}
		if currentListenerProtocolValue, ok := currentListenerProtocolMap[strings.TrimSpace(desiredListenerProtocol[0])]; ok {
			if currentListenerProtocolValue != strings.TrimSpace(desiredListenerProtocol[1]) {
				return true
			}
		}
	}
	return false
}

// collects are the config keys that are either added, removed or updated
// between the current and the desired ConfigMap
func collectTouchedConfigs(currentConfigs, desiredConfigs map[string]string, log logr.Logger) map[string]struct{} {
	touchedConfigs := make(map[string]struct{})

	currentConfigsCopy := make(map[string]string)
	for k, v := range currentConfigs {
		currentConfigsCopy[k] = v
	}

	for configName, desiredValue := range desiredConfigs {
		if currentValue, ok := currentConfigsCopy[configName]; !ok || currentValue != desiredValue {
			// new or updated config
			touchedConfigs[configName] = struct{}{}
		}
		delete(currentConfigsCopy, configName)
	}

	for configName := range currentConfigsCopy {
		// deleted config
		touchedConfigs[configName] = struct{}{}
	}

	log.V(1).Info("configs have been changed", "configs", touchedConfigs)
	return touchedConfigs
}

func GetExposedServicePorts(extListener v1beta1.ExternalListenerConfig, brokersIds []int, kafkaClusterSpec v1beta1.KafkaClusterSpec,
	ingressConfigName string, log logr.Logger, portNamePrefix string) []corev1.ServicePort {
	var exposedPorts []corev1.ServicePort
	var err error
	for _, brokerId := range brokersIds {
		var brokerConfig *v1beta1.BrokerConfig
		// This check is used in case of broker delete. In case of broker delete there is some time when the CC removes the broker
		// gracefully which means we have to generate the port for that broker as well. At that time the status contains
		// but the broker spec does not contain the required config values.
		if len(kafkaClusterSpec.Brokers)-1 < brokerId {
			brokerConfig = &v1beta1.BrokerConfig{}
		} else {
			brokerConfig, err = util.GetBrokerConfig(kafkaClusterSpec.Brokers[brokerId], kafkaClusterSpec)
			if err != nil {
				log.Error(err, "could not determine brokerConfig")
				continue
			}
		}
		if len(brokerConfig.BrokerIdBindings) == 0 ||
			util.StringSliceContains(brokerConfig.BrokerIdBindings, ingressConfigName) {

			exposedPorts = append(exposedPorts, corev1.ServicePort{
				Name:       fmt.Sprintf("%sbroker-%d", portNamePrefix, brokerId),
				Port:       extListener.ExternalStartingPort + int32(brokerId),
				TargetPort: intstr.FromInt(int(extListener.ExternalStartingPort) + brokerId),
				Protocol:   corev1.ProtocolTCP,
			})
		}
	}

	exposedPorts = append(exposedPorts, corev1.ServicePort{
		Name:       fmt.Sprintf(AllBrokerServiceTemplate, "tcp"),
		TargetPort: intstr.FromInt(int(extListener.GetAnyCastPort())),
		Port:       extListener.GetAnyCastPort(),
	})

	return exposedPorts
}
