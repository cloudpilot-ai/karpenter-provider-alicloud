/*
Copyright 2024 The CloudPilot AI Authors.

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

package utils

import (
	"fmt"
	"regexp"
	"time"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// alibabacloud ack node spec providerID format, ref: https://github.com/cloudpilot-ai/karpenter-provider-alicloud/pull/35#discussion_r1794805184
// eg: cn-zhangjiakou.i-xxxx
var instanceIDRegex = regexp.MustCompile(`(?P<AZ>.*)\.(?P<InstanceID>.*)`)

// ParseInstanceID parses the provider ID stored on the node to get the instance ID
// associated with a node
func ParseInstanceID(providerID string) (string, error) {
	matches := instanceIDRegex.FindStringSubmatch(providerID)
	if matches == nil {
		return "", fmt.Errorf("parsing instance id %s", providerID)
	}
	for i, name := range instanceIDRegex.SubexpNames() {
		if name == "InstanceID" {
			return matches[i], nil
		}
	}
	return "", fmt.Errorf("parsing instance id %s", providerID)
}

// ParseISO8601 parses the given time string into a time.Time object
func ParseISO8601(timeStr string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04Z", timeStr)
}

// GetAllSingleValuedRequirementLabels converts instanceType.Requirements to labels
// Like   instanceType.Requirements.Labels() it uses single-valued requirements
// Unlike instanceType.Requirements.Labels() it does not filter out restricted Node labels
func GetAllSingleValuedRequirementLabels(instanceType *cloudprovider.InstanceType) map[string]string {
	labels := map[string]string{}
	if instanceType == nil {
		return labels
	}
	for key, req := range instanceType.Requirements {
		if req.Len() == 1 {
			labels[key] = req.Values()[0]
		}
	}
	return labels
}
