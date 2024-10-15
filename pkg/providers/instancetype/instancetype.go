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

package instancetype

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	kcache "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/cache"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/pricing"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/vswitch"
)

type Provider interface {
	LivenessProbe(*http.Request) error
	List(context.Context, *v1alpha1.KubeletConfiguration, *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error)
	UpdateInstanceTypes(ctx context.Context) error
	UpdateInstanceTypeOfferings(ctx context.Context) error
}

type defaultProviderOptions struct {
	instanceTypeFilterMap func(*cloudprovider.InstanceType, *v1alpha1.ECSNodeClass) (*cloudprovider.InstanceType, bool)
}

var WithInstanceTypeFilterMap = func(instanceTypeFilterMap func(*cloudprovider.InstanceType, *v1alpha1.ECSNodeClass) (*cloudprovider.InstanceType, bool)) func(*defaultProviderOptions) {
	return func(opts *defaultProviderOptions) {
		opts.instanceTypeFilterMap = instanceTypeFilterMap
	}
}

type DefaultProvider struct {
	defaultProviderOptions
	region          string
	ecsClient       *ecsclient.Client
	vSwitchProvider vswitch.Provider
	pricingProvider pricing.Provider

	// Values stored *before* considering insufficient capacity errors from the unavailableOfferings cache.
	// Fully initialized Instance Types are also cached based on the set of all instance types, zones, unavailableOfferings cache,
	// ECSNodeClass, and kubelet configuration from the NodePool

	muInstanceTypeInfo sync.RWMutex

	instanceTypesInfo []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType

	muInstanceTypeOfferings sync.RWMutex
	instanceTypeOfferings   map[string]sets.Set[string]

	instanceTypesCache *cache.Cache

	unavailableOfferings *kcache.UnavailableOfferings
	cm                   *pretty.ChangeMonitor
	// instanceTypesSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypesSeqNum uint64
	// instanceTypeOfferingsSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypeOfferingsSeqNum uint64
}

func NewDefaultProvider(region string, ecsClient *ecsclient.Client, instanceTypesCache *cache.Cache, pricingProvider pricing.Provider, vSwitchProvider vswitch.Provider) *DefaultProvider {
	return &DefaultProvider{
		ecsClient:             ecsClient,
		region:                region,
		vSwitchProvider:       vSwitchProvider,
		pricingProvider:       pricingProvider,
		instanceTypesInfo:     []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{},
		instanceTypeOfferings: map[string]sets.Set[string]{},
		instanceTypesCache:    instanceTypesCache,
		// unavailableOfferings:  unavailableOfferingsCache,
		cm:                  pretty.NewChangeMonitor(),
		instanceTypesSeqNum: 0,
	}
}

func (p *DefaultProvider) LivenessProbe(req *http.Request) error {
	if err := p.vSwitchProvider.LivenessProbe(req); err != nil {
		return err
	}
	return p.pricingProvider.LivenessProbe(req)
}

func (p *DefaultProvider) List(ctx context.Context, kc *v1alpha1.KubeletConfiguration, nodeClass *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error) {
	p.muInstanceTypeInfo.RLock()
	p.muInstanceTypeOfferings.RLock()
	defer p.muInstanceTypeInfo.RUnlock()
	defer p.muInstanceTypeOfferings.RUnlock()

	if kc == nil {
		kc = &v1alpha1.KubeletConfiguration{}
	}
	if len(p.instanceTypesInfo) == 0 {
		return nil, errors.New("no instance types found")
	}
	if len(p.instanceTypeOfferings) == 0 {
		return nil, errors.New("no instance types offerings found")
	}
	if len(nodeClass.Status.VSwitches) == 0 {
		return nil, errors.New("no vswitches found")
	}

	vSwitchsZones := sets.New(lo.Map(nodeClass.Status.VSwitches, func(s v1alpha1.VSwitch, _ int) string {
		return s.ZoneID
	})...)

	// Compute fully initialized instance types hash key
	vSwitchZonesHash, _ := hashstructure.Hash(vSwitchsZones, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	kcHash, _ := hashstructure.Hash(kc, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	key := fmt.Sprintf("%d-%d-%d-%016x-%016x",
		p.instanceTypesSeqNum,
		p.instanceTypeOfferingsSeqNum,
		p.unavailableOfferings.SeqNum,
		vSwitchZonesHash,
		kcHash,
	)

	if item, ok := p.instanceTypesCache.Get(key); ok {
		// Ensure what's returned from this function is a shallow-copy of the slice (not a deep-copy of the data itself)
		// so that modifications to the ordering of the data don't affect the original
		return append([]*cloudprovider.InstanceType{}, item.([]*cloudprovider.InstanceType)...), nil
	}

	// Get all zones across all offerings
	// We don't use this in the cache key since this is produced from our instanceTypeOfferings which we do cache
	allZones := sets.New[string]()
	for _, offeringZones := range p.instanceTypeOfferings {
		for zone := range offeringZones {
			allZones.Insert(zone)
		}
	}

	if p.cm.HasChanged("zones", allZones) {
		log.FromContext(ctx).WithValues("zones", allZones.UnsortedList()).V(1).Info("discovered zones")
	}

	result := lo.FilterMap(p.instanceTypesInfo, func(i *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, _ int) (*cloudprovider.InstanceType, bool) {

		// !!! Important !!!
		// Any changes to the values passed into the NewInstanceType method will require making updates to the cache key
		// so that Karpenter is able to cache the set of InstanceTypes based on values that alter the set of instance types
		// !!! Important !!!
		return p.instanceTypeFilterMap(NewInstanceType(ctx, i, kc, p.region,
			p.createOfferings(ctx, *i.InstanceTypeId, allZones, p.instanceTypeOfferings[*i.InstanceTypeId], nodeClass.Status.VSwitches),
		), nodeClass)
	})

	p.instanceTypesCache.SetDefault(key, result)
	return result, nil
}

func (p *DefaultProvider) UpdateInstanceTypes(ctx context.Context) error {
	// DO NOT REMOVE THIS LOCK ----------------------------------------------------------------------------
	// We lock here so that multiple callers to getInstanceTypeOfferings do not result in cache misses and multiple
	// calls to ECS when we could have just made one call.

	p.muInstanceTypeInfo.Lock()
	defer p.muInstanceTypeInfo.Unlock()

	instanceTypes, err := getAllInstanceTypes(p.ecsClient)
	if err != nil {
		return err
	}

	if p.cm.HasChanged("instance-types", instanceTypes) {
		// Only update instanceTypesSeqNun with the instance types have been changed
		// This is to not create new keys with duplicate instance types option
		atomic.AddUint64(&p.instanceTypesSeqNum, 1)
		log.FromContext(ctx).WithValues(
			"count", len(instanceTypes)).V(1).Info("discovered instance types")
	}
	p.instanceTypesInfo = instanceTypes

	return nil
}

func (p *DefaultProvider) UpdateInstanceTypeOfferings(ctx context.Context) error {
	// DO NOT REMOVE THIS LOCK ----------------------------------------------------------------------------
	// We lock here so that multiple callers to getInstanceTypeOfferings do not result in cache misses and multiple
	// calls to ECS when we could have just made one call.

	p.muInstanceTypeOfferings.Lock()
	defer p.muInstanceTypeOfferings.Unlock()

	// Get offerings from ECS
	instanceTypeOfferings := map[string]sets.Set[string]{}
	describeAvailableResourceRequest := &ecsclient.DescribeAvailableResourceRequest{
		RegionId:            tea.String(p.region),
		DestinationResource: tea.String("InstanceType"),
	}
	runtime := &util.RuntimeOptions{}

	// TODO: we may use other better API in the future.
	resp, err := p.ecsClient.DescribeAvailableResourceWithOptions(describeAvailableResourceRequest, runtime)
	if err != nil {
		return err
	}

	if resp == nil || resp.Body == nil || resp.Body.AvailableZones == nil || len(resp.Body.AvailableZones.AvailableZone) == 0 {
		return errors.New("DescribeAvailableResourceWithOptions failed to return any instance types")
	}

	for _, az := range resp.Body.AvailableZones.AvailableZone {
		// TODO: Later, `ClosedWithStock` will be tested to determine if `ClosedWithStock` should be added.
		if *az.StatusCategory == "WithStock" { // WithStock, ClosedWithStock, WithoutStock, ClosedWithoutStock
			processAvailableResources(az, instanceTypeOfferings)
		}
	}

	if p.cm.HasChanged("instance-type-offering", instanceTypeOfferings) {
		// Only update instanceTypesSeqNun with the instance type offerings  have been changed
		// This is to not create new keys with duplicate instance type offerings option
		atomic.AddUint64(&p.instanceTypeOfferingsSeqNum, 1)
		log.FromContext(ctx).WithValues("instance-type-count", len(instanceTypeOfferings)).V(1).Info("discovered offerings for instance types")
	}
	p.instanceTypeOfferings = instanceTypeOfferings
	return nil
}

func processAvailableResources(az *ecsclient.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZone, instanceTypeOfferings map[string]sets.Set[string]) {
	if az.AvailableResources == nil || az.AvailableResources.AvailableResource == nil {
		return
	}

	for _, ar := range az.AvailableResources.AvailableResource {
		if ar.SupportedResources == nil || ar.SupportedResources.SupportedResource == nil {
			continue
		}

		for _, sr := range ar.SupportedResources.SupportedResource {
			// TODO: Later, `ClosedWithStock` will be tested to determine if `ClosedWithStock` should be added.
			if *sr.StatusCategory == "WithStock" { // WithStock, ClosedWithStock, WithoutStock, ClosedWithoutStock
				if _, ok := instanceTypeOfferings[*sr.Value]; !ok {
					instanceTypeOfferings[*sr.Value] = sets.New[string]()
				}
				instanceTypeOfferings[*sr.Value].Insert(*az.ZoneId)
			}
		}
	}
}

func getAllInstanceTypes(client *ecsclient.Client) ([]*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, error) {
	var InstanceTypes []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType

	describeInstanceTypesRequest := &ecsclient.DescribeInstanceTypesRequest{
		/*
			Reference: https://api.aliyun.com/api/Ecs/2014-05-26/DescribeInstanceTypes caveat:
			The maximum value of Max Results (maximum number of entries per page) parameter is 100,
			for users who have called this API in 2022, the maximum value of Max Results parameter is still 1600,
			on and after November 15, 2023, we will reduce the maximum value of Max Results parameter to 100 for all users,
			and no longer support 1600, if If you do not pass the Next Token parameter for paging when you call this API,
			only the first page of the specification (no more than 100 items) will be returned by default.
		*/
		MaxResults: tea.Int64(100),
	}
	runtime := &util.RuntimeOptions{}

	for {
		resp, err := client.DescribeInstanceTypesWithOptions(describeInstanceTypesRequest, runtime)
		if err != nil {
			return nil, err
		}

		if resp == nil || resp.Body == nil || resp.Body.NextToken == nil || resp.Body.InstanceTypes == nil ||
			*resp.Body.NextToken == "" || len(resp.Body.InstanceTypes.InstanceType) == 0 {
			break
		}

		describeInstanceTypesRequest.NextToken = resp.Body.NextToken
		InstanceTypes = append(InstanceTypes, resp.Body.InstanceTypes.InstanceType...)
	}

	return InstanceTypes, nil
}

// createOfferings creates a set of mutually exclusive offerings for a given instance type. This provider maintains an
// invariant that each offering is mutually exclusive. Specifically, there is an offering for each permutation of zone
// and capacity type. ZoneID is also injected into the offering requirements, when available, but there is a 1-1
// mapping between zone and zoneID so this does not change the number of offerings.
//
// Each requirement on the offering is guaranteed to have a single value. To get the value for a requirement on an
// offering, you can do the following thanks to this invariant:
//
//	offering.Requirements.Get(v1.TopologyLabelZone).Any()
func (p *DefaultProvider) createOfferings(_ context.Context, instanceType string, zones, instanceTypeZones sets.Set[string],
	vswitchs []v1alpha1.VSwitch) []cloudprovider.Offering {

	var offerings []cloudprovider.Offering
	for zone := range zones {
		odPrice, odOK := p.pricingProvider.OnDemandPrice(instanceType)
		spotPrice, spotOK := p.pricingProvider.SpotPrice(instanceType, zone)

		vswitch, hasVSwitch := lo.Find(vswitchs, func(s v1alpha1.VSwitch) bool {
			return s.ZoneID == zone
		})

		if odOK {
			isUnavailable := p.unavailableOfferings.IsUnavailable(instanceType, zone, v1beta1.CapacityTypeOnDemand)
			available := !isUnavailable && odOK && instanceTypeZones.Has(zone) && hasVSwitch

			offerings = append(offerings, p.createOffering(zone, v1beta1.CapacityTypeOnDemand, &vswitch, odPrice, available))
		}

		if spotOK {
			isUnavailable := p.unavailableOfferings.IsUnavailable(instanceType, zone, v1beta1.CapacityTypeSpot)
			available := !isUnavailable && spotOK && instanceTypeZones.Has(zone) && hasVSwitch

			offerings = append(offerings, p.createOffering(zone, v1beta1.CapacityTypeSpot, &vswitch, spotPrice, available))
		}
	}
	return offerings
}

func (p *DefaultProvider) createOffering(zone, capacityType string, vswitch *v1alpha1.VSwitch,
	price float64, available bool) cloudprovider.Offering {

	offering := cloudprovider.Offering{
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
		),
		Price:     price,
		Available: available,
	}
	if vswitch.ZoneID != "" {
		offering.Requirements.Add(scheduling.NewRequirement(v1alpha1.LabelTopologyZoneID, corev1.NodeSelectorOpIn, vswitch.ZoneID))
	}

	return offering
}

func (p *DefaultProvider) Reset() {
	p.instanceTypesInfo = []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{}
	p.instanceTypeOfferings = map[string]sets.Set[string]{}
	p.instanceTypesCache.Flush()
}
