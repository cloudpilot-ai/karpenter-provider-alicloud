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
	"time"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils"
)

const (
	// Ref: https://api.aliyun.com/api/Ecs/2014-05-26/DescribeInstances
	InstanceStatusPending  = "Pending"
	InstanceStatusRunning  = "Running"
	InstanceStatusStarting = "Starting"
	InstanceStatusStopping = "Stopping"
	InstanceStatusStopped  = "Stopped"
)

// Instance is an internal data representation of either an ecsclient.DescribeInstancesResponseBodyInstancesInstance
// It contains all the common data that is needed to inject into the Machine from either of these responses
type Instance struct {
	CreationTime     time.Time         `json:"creationTime"`
	Status           string            `json:"status"`
	ID               string            `json:"id"`
	ImageID          string            `json:"imageId"`
	Type             string            `json:"type"`
	Region           string            `json:"region"`
	Zone             string            `json:"zone"`
	CapacityType     string            `json:"capacityType"`
	SecurityGroupIDs []string          `json:"securityGroupIds"`
	VSwitchID        string            `json:"vSwitchId"`
	Tags             map[string]string `json:"tags"`
}

func NewInstance(out *ecsclient.DescribeInstancesResponseBodyInstancesInstance) *Instance {
	creationTime, err := utils.ParseISO8601(*out.CreationTime)
	if err != nil {
		log.Log.Error(err, "Failed to parse creation time")
	}

	return &Instance{
		CreationTime: creationTime,
		Status:       *out.Status,
		ID:           *out.InstanceId,
		ImageID:      *out.ImageId,
		Type:         *out.InstanceType,
		Region:       *out.RegionId,
		Zone:         *out.ZoneId,
		CapacityType: utils.GetCapacityTypes(*out.SpotStrategy),
		SecurityGroupIDs: lo.Map(out.SecurityGroupIds.SecurityGroupId, func(securitygroup *string, _ int) string {
			return *securitygroup
		}),
		VSwitchID: *out.VpcAttributes.VSwitchId,
		Tags: lo.SliceToMap(out.Tags.Tag, func(tag *ecsclient.DescribeInstancesResponseBodyInstancesInstanceTagsTag) (string, string) {
			return *tag.TagKey, *tag.TagValue
		}),
	}
}
