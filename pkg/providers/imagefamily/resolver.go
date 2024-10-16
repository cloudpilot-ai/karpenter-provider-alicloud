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
	"fmt"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

// Options define the static launch template parameters
type Options struct {
	ClusterName     string
	ClusterEndpoint string
	// Level-triggered fields that may change out of sync.
	SecurityGroups []v1alpha1.SecurityGroup
	Tags           map[string]string
	Labels         map[string]string `hash:"ignore"`
	NodeClassName  string
}

// LaunchTemplate holds the dynamically generated launch template parameters
type LaunchTemplate struct {
	*Options
	UserData      string
	ImageID       string
	InstanceTypes []*cloudprovider.InstanceType `hash:"ignore"`
	SystemDisk    *v1alpha1.SystemDisk
	CapacityType  string
	// TODO: need more field, HttpTokens, RamRole, NetworkInterface, DataDisk, ...
}

type Resolver interface {
	Resolve(*v1alpha1.ECSNodeClass, *karpv1.NodeClaim, []*cloudprovider.InstanceType, string, *Options) ([]*LaunchTemplate, error)
}

// DefaultResolver is able to fill-in dynamic launch template parameters
type DefaultResolver struct{}

// NewDefaultResolver constructs a new launch template DefaultResolver
func NewDefaultResolver() *DefaultResolver {
	return &DefaultResolver{}
}

// Resolve generates launch templates using the static options and dynamically generates launch template parameters.
// Multiple ResolvedTemplates are returned based on the instanceTypes passed in to support special Images for certain instance types like GPUs.
func (r DefaultResolver) Resolve(nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string, options *Options) ([]*LaunchTemplate, error) {
	imageFamily := GetAMIFamily(nodeClass.ImageFamily(), options)
	if len(nodeClass.Status.Images) == 0 {
		return nil, fmt.Errorf("no images exist given constraints")
	}
	if imageFamily == nil {
		return nil, fmt.Errorf("image family not found")
	}
	mappedImages := MapToInstanceTypes(instanceTypes, nodeClass.Status.Images)
	if len(mappedImages) == 0 {
		return nil, fmt.Errorf("no instance types satisfy requirements of images %v", lo.Uniq(lo.Map(nodeClass.Status.Images, func(a v1alpha1.Image, _ int) string { return a.ID })))
	}
	var resolvedTemplates []*LaunchTemplate
	for imageID, instanceTypes := range mappedImages {
		// TODO: instanceTypes group by MaxPod
		resolved := r.resolveLaunchTemplate(nodeClass, nodeClaim, instanceTypes, capacityType, imageFamily, imageID, options)
		resolvedTemplates = append(resolvedTemplates, resolved)
	}
	return resolvedTemplates, nil
}

func GetAMIFamily(family string, options *Options) ImageFamily {
	switch family {
	case v1alpha1.ImageFamilyCustom:
		return &Custom{Options: options}
	case v1alpha1.ImageFamilyAliyun3:
		return &Aliyun3{Options: options}
	default:
		return nil
	}
}

// MapToInstanceTypes returns a map of ImageIDs that are the most recent on creationDate to compatible instancetypes
func MapToInstanceTypes(instanceTypes []*cloudprovider.InstanceType, images []v1alpha1.Image) map[string][]*cloudprovider.InstanceType {
	imageIDs := map[string][]*cloudprovider.InstanceType{}
	for _, instanceType := range instanceTypes {
		for _, img := range images {
			if err := instanceType.Requirements.Compatible(
				scheduling.NewNodeSelectorRequirements(img.Requirements...),
				scheduling.AllowUndefinedWellKnownLabels,
			); err == nil {
				imageIDs[img.ID] = append(imageIDs[img.ID], instanceType)
				break
			}
		}
	}
	return imageIDs
}

func (r DefaultResolver) resolveLaunchTemplate(nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string,
	imageFamily ImageFamily, imageID string, options *Options) *LaunchTemplate {
	kubeletConfig := &v1alpha1.KubeletConfiguration{}
	if nodeClass.Spec.KubeletConfiguration != nil {
		kubeletConfig = nodeClass.Spec.KubeletConfiguration.DeepCopy()
	}

	taints := lo.Flatten([][]corev1.Taint{
		nodeClaim.Spec.Taints,
		nodeClaim.Spec.StartupTaints,
	})
	if _, found := lo.Find(taints, func(t corev1.Taint) bool {
		return t.MatchTaint(&karpv1.UnregisteredNoExecuteTaint)
	}); !found {
		taints = append(taints, karpv1.UnregisteredNoExecuteTaint)
	}

	resolved := &LaunchTemplate{
		Options: options,
		UserData: imageFamily.UserData(
			kubeletConfig,
			taints,
			options.Labels,
			instanceTypes,
			nodeClass.Spec.UserData,
		),
		SystemDisk:    nodeClass.Spec.SystemDisk,
		ImageID:       imageID,
		InstanceTypes: instanceTypes,
		CapacityType:  capacityType,
	}
	if resolved.SystemDisk == nil {
		resolved.SystemDisk = imageFamily.DefaultSystemDisk()
	}
	return resolved
}
