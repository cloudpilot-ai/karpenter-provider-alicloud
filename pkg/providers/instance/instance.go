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
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/imagefamily"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/vswitch"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils/alierrors"
)

const (
	// TODO: After that open up the configuration options
	instanceTypeFlexibilityThreshold = 5 // falling back to on-demand without flexibility risks insufficient capacity errors
	maxInstanceTypes                 = 20
)

type Provider interface {
	Create(context.Context, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim, []*cloudprovider.InstanceType) (*Instance, error)
	Get(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Delete(context.Context, string) error
	CreateTags(context.Context, string, map[string]string) error
}

type DefaultProvider struct {
	ecsClient       *ecsclient.Client
	region          string
	clusterEndpoint string

	imageFamily imagefamily.Resolver

	vSwitchProvider vswitch.Provider
}

func NewDefaultProvider(ctx context.Context, region, clusterEndpoint string, ecsClient *ecsclient.Client,
	imageFamily imagefamily.Resolver,
	vSwitchProvider vswitch.Provider) *DefaultProvider {
	return &DefaultProvider{
		ecsClient:       ecsClient,
		region:          region,
		clusterEndpoint: clusterEndpoint,

		imageFamily: imageFamily,

		vSwitchProvider: vSwitchProvider,
	}
}

func (p *DefaultProvider) Create(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType,
) (*Instance, error) {
	schedulingRequirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	// Only filter the instances if there are no minValues in the requirement.
	if !schedulingRequirements.HasMinValues() {
		instanceTypes = p.filterInstanceTypes(nodeClaim, instanceTypes)
	}
	instanceTypes, err := cloudprovider.InstanceTypes(instanceTypes).Truncate(schedulingRequirements, maxInstanceTypes)
	if err != nil {
		return nil, fmt.Errorf("truncating instance types, %w", err)
	}
	tags := getTags(ctx, nodeClass, nodeClaim)
	launchInstance, err := p.launchInstance(ctx, nodeClass, nodeClaim, instanceTypes, tags)
	if err != nil {
		return nil, err
	}

	return p.Get(ctx, *launchInstance.InstanceIds.InstanceId[0])
}

func (p *DefaultProvider) Get(ctx context.Context, id string) (*Instance, error) {
	describeInstancesRequest := &ecsclient.DescribeInstancesRequest{
		RegionId:    &p.region,
		InstanceIds: tea.String("[\"" + id + "\"]"),
	}
	runtime := &util.RuntimeOptions{}

	resp, err := p.ecsClient.DescribeInstancesWithOptions(describeInstancesRequest, runtime)
	if err != nil {
		return nil, err
	}

	if resp == nil || resp.Body == nil || resp.Body.Instances == nil {
		return nil, fmt.Errorf("failed to get instance %s", id)
	}

	if len(resp.Body.Instances.Instance) != 1 {
		return nil, fmt.Errorf("expected a single instance, %w", err)
	}

	return NewInstance(resp.Body.Instances.Instance[0]), nil
}

func (p *DefaultProvider) List(ctx context.Context) ([]*Instance, error) {
	var instances []*Instance

	describeInstancesRequest := &ecsclient.DescribeInstancesRequest{
		Tag: []*ecsclient.DescribeInstancesRequestTag{
			{
				Key: tea.String(karpv1.NodePoolLabelKey),
			},
			{
				Key: tea.String(v1alpha1.LabelNodeClass),
			},
			{
				Key:   tea.String("kubernetes.io/cluster"),
				Value: tea.String(options.FromContext(ctx).ClusterName),
			},
		},
		RegionId: p.ecsClient.RegionId,
	}

	runtime := &util.RuntimeOptions{}

	for {
		// TODO: limit 1000
		/* Refer https://api.aliyun.com/api/Ecs/2014-05-26/DescribeInstances
		If you use one tag to filter resources, the number of resources queried under that tag cannot exceed 1000;
		if you use multiple tags to filter resources, the number of resources queried with multiple tags bound at the
		same time cannot exceed 1000. If the number of resources exceeds 1000, use the ListTagResources interface to query.
		*/
		resp, err := p.ecsClient.DescribeInstancesWithOptions(describeInstancesRequest, runtime)
		if err != nil {
			return nil, err
		}

		if resp == nil || resp.Body == nil || resp.Body.NextToken == nil || resp.Body.Instances == nil ||
			*resp.Body.NextToken == "" || len(resp.Body.Instances.Instance) == 0 {
			break
		}

		describeInstancesRequest.NextToken = resp.Body.NextToken
		for i := range resp.Body.Instances.Instance {
			instances = append(instances, NewInstance(resp.Body.Instances.Instance[i]))
		}
	}

	return instances, nil
}

func (p *DefaultProvider) Delete(ctx context.Context, id string) error {
	deleteInstanceRequest := &ecsclient.DeleteInstanceRequest{
		InstanceId: tea.String(id),
	}

	runtime := &util.RuntimeOptions{}
	if _, err := p.ecsClient.DeleteInstanceWithOptions(deleteInstanceRequest, runtime); err != nil {
		if alierrors.IsNotFound(err) {
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance already terminated"))
		}

		if _, e := p.Get(ctx, id); e != nil {
			if cloudprovider.IsNodeClaimNotFoundError(e) {
				return e
			}
			err = multierr.Append(err, e)
		}

		return fmt.Errorf("terminating instance, %w", err)
	}

	return nil
}

func (p *DefaultProvider) CreateTags(ctx context.Context, id string, tags map[string]string) error {
	ecsTags := make([]*ecsclient.AddTagsRequestTag, 0, len(tags))
	for k, v := range tags {
		ecsTags = append(ecsTags, &ecsclient.AddTagsRequestTag{
			Key:   tea.String(k),
			Value: tea.String(v),
		})
	}

	addTagsRequest := &ecsclient.AddTagsRequest{
		RegionId:     &p.region,
		ResourceType: tea.String("instance"),
		ResourceId:   tea.String(id),
		Tag:          ecsTags,
	}

	runtime := &util.RuntimeOptions{}
	if _, err := p.ecsClient.AddTagsWithOptions(addTagsRequest, runtime); err != nil {
		if alierrors.IsNotFound(err) {
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("tagging instance, %w", err))
		}
		return fmt.Errorf("tagging instance, %w", err)
	}

	return nil
}

// filterInstanceTypes is used to provide filtering on the list of potential instance types to further limit it to those
// that make the most sense given our specific Alibaba Cloud cloudprovider.
func (p *DefaultProvider) filterInstanceTypes(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	instanceTypes = filterExoticInstanceTypes(instanceTypes)
	// If we could potentially launch either a spot or on-demand node, we want to filter out the spot instance types that
	// are more expensive than the cheapest on-demand type.
	if p.isMixedCapacityLaunch(nodeClaim, instanceTypes) {
		instanceTypes = filterUnwantedSpot(instanceTypes)
	}
	return instanceTypes
}

// filterExoticInstanceTypes is used to eliminate less desirable instance types (like GPUs) from the list of possible instance types when
// a set of more appropriate instance types would work. If a set of more desirable instance types is not found, then the original slice
// of instance types are returned.
func filterExoticInstanceTypes(instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	var genericInstanceTypes []*cloudprovider.InstanceType
	for _, it := range instanceTypes {
		// deprioritize metal even if our opinionated filter isn't applied due to something like an instance family
		// requirement
		if _, ok := lo.Find(it.Requirements.Get(v1alpha1.LabelInstanceSize).Values(), func(size string) bool { return strings.Contains(size, "metal") }); ok {
			continue
		}
		if !resources.IsZero(it.Capacity[v1alpha1.ResourceAMDGPU]) ||
			!resources.IsZero(it.Capacity[v1alpha1.ResourceNVIDIAGPU]) {
			continue
		}
		genericInstanceTypes = append(genericInstanceTypes, it)
	}
	// if we got some subset of instance types, then prefer to use those
	if len(genericInstanceTypes) != 0 {
		return genericInstanceTypes
	}
	return instanceTypes
}

// isMixedCapacityLaunch returns true if nodepools and available offerings could potentially allow either a spot or
// and on-demand node to launch
func (p *DefaultProvider) isMixedCapacityLaunch(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) bool {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	// requirements must allow both
	if !requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) ||
		!requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
		return false
	}
	hasSpotOfferings := false
	hasODOffering := false
	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		for _, instanceType := range instanceTypes {
			for _, offering := range instanceType.Offerings.Available() {
				if requirements.Compatible(offering.Requirements, scheduling.AllowUndefinedWellKnownLabels) != nil {
					continue
				}
				if offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any() == karpv1.CapacityTypeSpot {
					hasSpotOfferings = true
				} else {
					hasODOffering = true
				}
			}
		}
	}
	return hasSpotOfferings && hasODOffering
}

// filterUnwantedSpot is used to filter out spot types that are more expensive than the cheapest on-demand type that we
// could launch during mixed capacity-type launches
func filterUnwantedSpot(instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	cheapestOnDemand := math.MaxFloat64
	// first, find the price of our cheapest available on-demand instance type that could support this node
	for _, it := range instanceTypes {
		for _, o := range it.Offerings.Available() {
			if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Any() == karpv1.CapacityTypeOnDemand && o.Price < cheapestOnDemand {
				cheapestOnDemand = o.Price
			}
		}
	}

	// Filter out any types where the cheapest offering, which should be spot, is more expensive than the cheapest
	// on-demand instance type that would have worked. This prevents us from getting a larger more-expensive spot
	// instance type compared to the cheapest sufficiently large on-demand instance type
	instanceTypes = lo.Filter(instanceTypes, func(item *cloudprovider.InstanceType, index int) bool {
		available := item.Offerings.Available()
		if len(available) == 0 {
			return false
		}
		return available.Cheapest().Price <= cheapestOnDemand
	})
	return instanceTypes
}

func getTags(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim) map[string]string {
	staticTags := map[string]string{
		fmt.Sprintf("kubernetes.io/cluster/%s", options.FromContext(ctx).ClusterName): "owned",
		karpv1.NodePoolLabelKey:       nodeClaim.Labels[karpv1.NodePoolLabelKey],
		v1alpha1.ECSClusterNameTagKey: options.FromContext(ctx).ClusterName,
		v1alpha1.LabelNodeClass:       nodeClass.Name,
	}
	return lo.Assign(nodeClass.Spec.Tags, staticTags)
}

func (p *DefaultProvider) launchInstance(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType,
	tags map[string]string) (*ecsclient.CreateAutoProvisioningGroupResponseBodyLaunchResultsLaunchResult, error) {
	if err := p.checkODFallback(nodeClaim, instanceTypes); err != nil {
		log.FromContext(ctx).Error(err, "failed while checking on-demand fallback")
	}
	capacityType := p.getCapacityType(nodeClaim, instanceTypes)
	zonalVSwitchs, err := p.vSwitchProvider.ZonalVSwitchesForLaunch(ctx, nodeClass, instanceTypes, capacityType)
	if err != nil {
		return nil, fmt.Errorf("getting vSwitches, %w", err)
	}

	createAutoProvisioningGroupRequest, err := p.getProvisioningGroup(ctx, nodeClass, nodeClaim, instanceTypes, zonalVSwitchs, capacityType, tags)
	if err != nil {
		return nil, fmt.Errorf("getting provisioning group, %w", err)
	}

	runtime := &util.RuntimeOptions{}
	resp, err := p.ecsClient.CreateAutoProvisioningGroupWithOptions(createAutoProvisioningGroupRequest, runtime)
	if err != nil {
		return nil, fmt.Errorf("creating auto provisioning group, %w", err)
	}

	return resp.Body.LaunchResults.LaunchResult[0], nil
}

// getCapacityType selects spot if both constraints are flexible and there is an
// available offering. The Alibaba Cloud Provider defaults to [ on-demand ], so spot
// must be explicitly included in capacity type requirements.
func (p *DefaultProvider) getCapacityType(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) string {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		requirements[karpv1.CapacityTypeLabelKey] = scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot)
		for _, instanceType := range instanceTypes {
			for _, offering := range instanceType.Offerings.Available() {
				if requirements.Compatible(offering.Requirements, scheduling.AllowUndefinedWellKnownLabels) == nil {
					return karpv1.CapacityTypeSpot
				}
			}
		}
	}
	return karpv1.CapacityTypeOnDemand
}

func (p *DefaultProvider) getProvisioningGroup(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType, zonalVSwitchs map[string]*vswitch.VSwitch, capacityType string, tags map[string]string) (*ecsclient.CreateAutoProvisioningGroupRequest, error) {

	launchTemplates, err := p.EnsureAll(ctx, nodeClass, nodeClaim, instanceTypes, capacityType, tags)
	if err != nil {
		return nil, fmt.Errorf("getting launch templates, %w", err)
	}

	if len(launchTemplates) == 0 {
		return nil, fmt.Errorf("no launch templates are currently available given the constraints")
	}

	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	requirements[karpv1.CapacityTypeLabelKey] = scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType)

	launchtemplate := launchTemplates[0]
	var launchTemplateConfigs []*ecsclient.CreateAutoProvisioningGroupRequestLaunchTemplateConfig
	for i := range launchtemplate.InstanceTypes {
		if i > maxInstanceTypes-1 {
			break
		}

		vSwitchID := p.getVSwitchID(launchtemplate.InstanceTypes[i], zonalVSwitchs, requirements, capacityType)
		if vSwitchID == "" {
			return nil, errors.New("vSwitchID not found")
		}

		launchTemplateConfig := &ecsclient.CreateAutoProvisioningGroupRequestLaunchTemplateConfig{
			InstanceType:     &launchtemplate.InstanceTypes[i].Name,
			VSwitchId:        &vSwitchID,
			WeightedCapacity: tea.Float64(1),
		}

		launchTemplateConfigs = append(launchTemplateConfigs, launchTemplateConfig)
	}

	createAutoProvisioningGroupRequest := &ecsclient.CreateAutoProvisioningGroupRequest{
		RegionId:                        tea.String(p.region),
		TotalTargetCapacity:             tea.String("1"),
		SpotAllocationStrategy:          tea.String("lowest-price"),
		PayAsYouGoAllocationStrategy:    tea.String("lowest-price"),
		LaunchTemplateConfig:            launchTemplateConfigs,
		ExcessCapacityTerminationPolicy: tea.String("termination"),
		AutoProvisioningGroupType:       tea.String("instant"),
		LaunchConfiguration: &ecsclient.CreateAutoProvisioningGroupRequestLaunchConfiguration{
			ImageId:          tea.String(launchtemplate.ImageID),
			SecurityGroupIds: launchtemplate.SecurityGroupIds,

			// TODO: AutoProvisioningGroup is not compatible with SecurityGroupIds, waiting for Aliyun developers to fix it,
			// so here we only take the first one.
			SecurityGroupId: launchtemplate.SecurityGroupIds[0],
		},
		SystemDiskConfig: []*ecsclient.CreateAutoProvisioningGroupRequestSystemDiskConfig{
			{DiskCategory: launchtemplate.SystemDisk.Category},
		},
	}

	if capacityType == karpv1.CapacityTypeSpot {
		createAutoProvisioningGroupRequest.SpotTargetCapacity = tea.String("1")
		createAutoProvisioningGroupRequest.PayAsYouGoTargetCapacity = tea.String("0")
	} else {
		createAutoProvisioningGroupRequest.SpotTargetCapacity = tea.String("0")
		createAutoProvisioningGroupRequest.PayAsYouGoTargetCapacity = tea.String("1")
	}

	return createAutoProvisioningGroupRequest, nil
}

func (p *DefaultProvider) checkODFallback(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) error {
	// only evaluate for on-demand fallback if the capacity type for the request is OD and both OD and spot are allowed in requirements
	if p.getCapacityType(nodeClaim, instanceTypes) != karpv1.CapacityTypeOnDemand ||
		!scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		return nil
	}

	if len(instanceTypes) < instanceTypeFlexibilityThreshold {
		return fmt.Errorf("at least %d instance types are recommended when flexible to spot but requesting on-demand, "+
			"the current provisioning request only has %d instance type options", instanceTypeFlexibilityThreshold, len(instanceTypes))
	}
	return nil
}

func (p *DefaultProvider) getVSwitchID(instanceType *cloudprovider.InstanceType,
	zonalVSwitchs map[string]*vswitch.VSwitch, reqs scheduling.Requirements, capacityType string) string {
	cheapestVSwitchId := ""
	cheapestPrice := math.MaxFloat64

	// For different AZ, the spot price may differ. So we need to get the cheapest vSwitch in the zone
	for i := range instanceType.Offerings {
		if reqs.Compatible(instanceType.Offerings[i].Requirements, scheduling.AllowUndefinedWellKnownLabels) != nil {
			continue
		}
		vswitch, ok := zonalVSwitchs[instanceType.Offerings[i].Requirements.Get(corev1.LabelTopologyZone).Any()]
		if !ok {
			continue
		}
		if capacityType == karpv1.CapacityTypeOnDemand {
			return vswitch.ID
		}

		if instanceType.Offerings[i].Price < cheapestPrice {
			cheapestVSwitchId = vswitch.ID
			cheapestPrice = instanceType.Offerings[i].Price
		}
	}

	return cheapestVSwitchId
}

type LaunchTemplate struct {
	InstanceTypes    []*cloudprovider.InstanceType
	ImageID          string
	SecurityGroupIds []*string
	SystemDisk       *v1alpha1.SystemDisk
}

func (p *DefaultProvider) EnsureAll(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string, tags map[string]string) ([]*LaunchTemplate, error) {
	imageOptions, err := p.resolveImageOptions(ctx, nodeClass, lo.Assign(nodeClaim.Labels, map[string]string{karpv1.CapacityTypeLabelKey: capacityType}), tags)
	if err != nil {
		return nil, err
	}
	resolvedLaunchTemplates, err := p.imageFamily.Resolve(ctx, nodeClass, nodeClaim, instanceTypes, capacityType, imageOptions)
	if err != nil {
		return nil, err
	}

	launchTemplates := make([]*LaunchTemplate, len(resolvedLaunchTemplates))
	for i := range resolvedLaunchTemplates {
		launchTemplates[i] = &LaunchTemplate{
			InstanceTypes:    resolvedLaunchTemplates[i].InstanceTypes,
			ImageID:          resolvedLaunchTemplates[i].ImageID,
			SecurityGroupIds: lo.Map(resolvedLaunchTemplates[i].SecurityGroups, func(s v1alpha1.SecurityGroup, _ int) *string { return tea.String(s.ID) }),
			SystemDisk: lo.Ternary(resolvedLaunchTemplates[i].SystemDisk == nil, nil, &v1alpha1.SystemDisk{
				Category:             resolvedLaunchTemplates[i].SystemDisk.Category,
				Size:                 resolvedLaunchTemplates[i].SystemDisk.Size,
				DiskName:             resolvedLaunchTemplates[i].SystemDisk.DiskName,
				PerformanceLevel:     resolvedLaunchTemplates[i].SystemDisk.PerformanceLevel,
				AutoSnapshotPolicyID: resolvedLaunchTemplates[i].SystemDisk.AutoSnapshotPolicyID,
				BurstingEnabled:      resolvedLaunchTemplates[i].SystemDisk.BurstingEnabled,
			}),
		}
	}
	return launchTemplates, nil
}

func (p *DefaultProvider) resolveImageOptions(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, labels, tags map[string]string) (*imagefamily.Options, error) {
	// Remove any labels passed into userData that are prefixed with "node-restriction.kubernetes.io" or "kops.k8s.io" since the kubelet can't
	// register the node with any labels from this domain: https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#noderestriction
	for k := range labels {
		labelDomain := karpv1.GetLabelDomain(k)
		if strings.HasSuffix(labelDomain, corev1.LabelNamespaceNodeRestriction) || strings.HasSuffix(labelDomain, "kops.k8s.io") {
			delete(labels, k)
		}
	}
	// Relying on the status rather than an API call means that Karpenter is subject to a race
	// condition where ECSNodeClass spec changes haven't propagated to the status once a node
	// has launched.
	// If a user changes their ECSNodeClass and shortly after Karpenter launches a node,
	// in the worst case, the node could be drifted and re-created.
	// TODO @aengeda: add status generation fields to gate node creation until the status is updated from a spec change
	// Get constrained security groups
	if len(nodeClass.Status.SecurityGroups) == 0 {
		return nil, fmt.Errorf("no security groups are present in the status")
	}
	return &imagefamily.Options{
		ClusterName:     options.FromContext(ctx).ClusterName,
		ClusterEndpoint: p.clusterEndpoint,
		SecurityGroups:  nodeClass.Status.SecurityGroups,
		Tags:            tags,
		Labels:          labels,
		NodeClassName:   nodeClass.Name,
	}, nil
}
