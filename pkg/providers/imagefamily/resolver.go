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
	"fmt"
	"sync"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

type InstanceTypeAvailableSystemDisk struct {
	availableSystemDisk sets.Set[string]
	// todo: verify availability zone
	// availableZone sets.Set[string]
}

func newInstanceTypeAvailableSystemDisk() *InstanceTypeAvailableSystemDisk {
	return &InstanceTypeAvailableSystemDisk{}
}

func (s *InstanceTypeAvailableSystemDisk) AddAvailableSystemDisk(systemDisks ...string) {
	s.availableSystemDisk.Insert(systemDisks...)
}

func (s *InstanceTypeAvailableSystemDisk) Compatible(systemDisk string) bool {
	return s.availableSystemDisk.Has(systemDisk)
}

type Resolver interface {
	Resolve(context.Context, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim, []*cloudprovider.InstanceType, string, *Options) ([]*LaunchTemplate, error)
}

// DefaultResolver is able to fill-in dynamic launch template parameters
type DefaultResolver struct {
	sync.Mutex
	region string
	ecsapi ecs.Client
	cache  *cache.Cache
}

// NewDefaultResolver constructs a new launch template DefaultResolver
func NewDefaultResolver(region string, ecsapi ecs.Client, cache *cache.Cache) *DefaultResolver {
	return &DefaultResolver{
		region: region,
		ecsapi: ecsapi,
		cache:  cache,
	}
}

// Resolve generates launch templates using the static options and dynamically generates launch template parameters.
// Multiple ResolvedTemplates are returned based on the instanceTypes passed in to support special Images for certain instance types like GPUs.
func (r *DefaultResolver) Resolve(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string, options *Options) ([]*LaunchTemplate, error) {
	imageFamily := GetImageFamily(nodeClass.ImageFamily(), options)
	if len(nodeClass.Status.Images) == 0 {
		return nil, fmt.Errorf("no images exist given constraints")
	}
	if imageFamily == nil {
		return nil, fmt.Errorf("image family not found")
	}

	instanceTypes = r.filterInstanceTypesBySystemDisk(ctx, nodeClass, instanceTypes)
	if len(instanceTypes) == 0 {
		return nil, fmt.Errorf("no instance types exist given system disk")
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

func GetImageFamily(family string, options *Options) ImageFamily {
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

func (r *DefaultResolver) resolveLaunchTemplate(nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string,
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

// todo: check system disk stock, currently only checking compatibility
func (r *DefaultResolver) filterInstanceTypesBySystemDisk(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	r.Lock()
	defer r.Unlock()

	if nodeClass.Spec.SystemDisk == nil || nodeClass.Spec.SystemDisk.Category == nil {
		return instanceTypes
	}
	expectDiskCategory := *nodeClass.Spec.SystemDisk.Category

	var result []*cloudprovider.InstanceType
	for i, instanceType := range instanceTypes {
		if availableSystemDisk, ok := r.cache.Get(instanceType.Name); ok {
			if availableSystemDisk.(*InstanceTypeAvailableSystemDisk).Compatible(expectDiskCategory) {
				result = append(result, instanceTypes[i])
			}
			continue
		}

		availableSystemDisk := newInstanceTypeAvailableSystemDisk()
		if err := r.describeAvailableSystemDisk(&ecs.DescribeAvailableResourceRequest{
			RegionId:            tea.String(r.region),
			DestinationResource: tea.String("SystemDisk"),
			InstanceType:        tea.String(instanceType.Name),
		}, func(resource *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			if *resource.Status == "Available" && *resource.Value != "" {
				availableSystemDisk.AddAvailableSystemDisk(*resource.Value)
			}
		}); err != nil {
			log.FromContext(ctx).Error(err, "describe available system disk failed")
			continue
		}

		if availableSystemDisk.Compatible(expectDiskCategory) {
			result = append(result, instanceTypes[i])
		} else {
			log.FromContext(ctx).V(1).Info("%s is not compatible with NodeClass %s SystemDisk %s", instanceType, nodeClass.Name, expectDiskCategory)
		}
		r.cache.SetDefault(instanceType.Name, availableSystemDisk)
	}
	return result
}

//nolint:gocyclo
func (r *DefaultResolver) describeAvailableSystemDisk(request *ecs.DescribeAvailableResourceRequest, process func(*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource)) error {
	runtime := &util.RuntimeOptions{}
	output, err := r.ecsapi.DescribeAvailableResourceWithOptions(request, runtime)
	if err != nil {
		return err
	} else if output == nil || output.Body == nil || output.Body.AvailableZones == nil {
		return fmt.Errorf("unexpected null value was returned")
	}
	for _, az := range output.Body.AvailableZones.AvailableZone {
		// todo: ClosedWithStock
		if *az.Status != "Available" || *az.StatusCategory != "WithStock" || az.AvailableResources == nil {
			continue
		}

		for _, ar := range az.AvailableResources.AvailableResource {
			if ar.SupportedResources == nil {
				continue
			}
			for _, sr := range ar.SupportedResources.SupportedResource {
				process(sr)
			}
		}
	}
	return nil
}
