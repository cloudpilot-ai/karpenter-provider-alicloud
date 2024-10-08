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

package instance

import (
	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// Instance is an internal data representation of either an ecsclient.DescribeInstancesResponseBodyInstancesInstance
// It contains all the common data that is needed to inject into the Machine from either of these responses
type Instance struct {
	CreationTime     string
	State            string
	ID               string
	ImageID          string
	Type             string
	Zone             string
	CapacityType     string
	SecurityGroupIDs []string
	SubnetID         string
	Tags             map[string]string
}

func NewInstance(out *ecsclient.DescribeInstancesResponseBodyInstancesInstance) *Instance {
	return &Instance{
		CreationTime: *out.CreationTime,
		State:        *out.Status,
		ID:           *out.InstanceId,
		ImageID:      *out.ImageId,
		Type:         *out.InstanceType,
		Zone:         *out.ZoneId,
		CapacityType: lo.Ternary(out.SpotStrategy != nil && *out.SpotStrategy != "NoSpot", karpv1.CapacityTypeSpot, karpv1.CapacityTypeOnDemand),
		SecurityGroupIDs: lo.Map(out.SecurityGroupIds.SecurityGroupId, func(securitygroup *string, _ int) string {
			return *securitygroup
		}),
		SubnetID: *out.VpcAttributes.VSwitchId,
		Tags: lo.SliceToMap(out.Tags.Tag, func(tag *ecsclient.DescribeInstancesResponseBodyInstancesInstanceTagsTag) (string, string) {
			return *tag.TagKey, *tag.TagValue
		}),
	}
}
