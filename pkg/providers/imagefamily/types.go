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

package imagefamily

import (
	"context"
	"math"
	"sort"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

var (
	minTime time.Time = time.Unix(math.MinInt64, 0)
)

type Image struct {
	Name         string
	ImageID      string
	CreationTime string
	Requirements scheduling.Requirements
}

type Images []Image

// Sort orders the Iamges by creation date in descending order.
// If creation date is nil or two Images have the same creation date, the Images will be sorted by ID, which is guaranteed to be unique, in ascending order.
func (a Images) Sort() {
	sort.Slice(a, func(i, j int) bool {
		itime := parseTimeWithDefault(a[i].CreationTime, minTime)
		jtime := parseTimeWithDefault(a[j].CreationTime, minTime)

		if itime.Unix() != jtime.Unix() {
			return itime.Unix() > jtime.Unix()
		}
		return a[i].ImageID < a[j].ImageID
	})
}

func parseTimeWithDefault(dateStr string, defaultTime time.Time) time.Time {
	if dateStr == "" {
		return defaultTime
	}
	return lo.Must(time.Parse(time.RFC3339, dateStr))
}

type DescribeImageQuery struct {
	FilterFunc func(string) bool
	BaseQuery  DescribeImageQueryBase
}

type DescribeImageQueryBase struct {
	*ecs.DescribeImagesRequest
	// KnownRequirements is a map from image IDs to a set of known requirements.
	// When discovering image IDs via OOS we know additional requirements which aren't surfaced by ecs:DescribeImage (e.g. GPU / Neuron compatibility)
	// Sometimes, an image may have multiple sets of known requirements.
	KnownRequirements []scheduling.Requirements
}

func (q *DescribeImageQuery) RequirementsForImageWithArchitecture(arch string) []scheduling.Requirements {
	if len(q.BaseQuery.KnownRequirements) > 0 {
		return lo.Map(q.BaseQuery.KnownRequirements, func(r scheduling.Requirements, _ int) scheduling.Requirements {
			r.Add(scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, arch))
			return r
		})
	}
	return []scheduling.Requirements{scheduling.NewRequirements(scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, arch))}
}

// ImageFamily can be implemented to override the default logic for generating dynamic launch template parameters
// TODO: add OOSProvider
type ImageFamily interface {
	DescribeImageQuery(ctx context.Context) (DescribeImageQuery, error)
	UserData(kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, instanceTypes []*cloudprovider.InstanceType, customUserData *string) string
	DefaultSystemDisk() *v1alpha1.SystemDisk
}
