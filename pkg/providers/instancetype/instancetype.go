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
	"net/http"
	"sync"
	"sync/atomic"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/pricing"
)

type Provider interface {
	LivenessProbe(*http.Request) error
	List(context.Context, *v1alpha1.KubeletConfiguration, *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error)
	UpdateInstanceTypes(ctx context.Context) error
	UpdateInstanceTypeOfferings(ctx context.Context) error
}

type DefaultProvider struct {
	region    string
	ecsClient *ecsclient.Client
	// subnetProvider  subnet.Provider
	pricingProvider pricing.Provider

	// Values stored *before* considering insufficient capacity errors from the unavailableOfferings cache.
	// Fully initialized Instance Types are also cached based on the set of all instance types, zones, unavailableOfferings cache,
	// ECSNodeClass, and kubelet configuration from the NodePool

	muInstanceTypeInfo sync.RWMutex

	instanceTypesInfo []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType

	muInstanceTypeOfferings sync.RWMutex
	instanceTypeOfferings   map[string]sets.Set[string]

	instanceTypesCache *cache.Cache

	// unavailableOfferings *awscache.UnavailableOfferings
	cm *pretty.ChangeMonitor
	// instanceTypesSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypesSeqNum uint64
	// instanceTypeOfferingsSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypeOfferingsSeqNum uint64
}

func NewDefaultProvider(region string, ecsClient *ecsclient.Client, instanceTypesCache *cache.Cache, pricingProvider pricing.Provider) *DefaultProvider {
	return &DefaultProvider{
		ecsClient: ecsClient,
		region:    region,
		// subnetProvider:        subnetProvider,
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

	return p.pricingProvider.LivenessProbe(req)
}

func (p *DefaultProvider) List(ctx context.Context, kc *v1alpha1.KubeletConfiguration, nodeClass *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error) {

	// TODO: implement me
	return nil, nil
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
	// calls to EC2 when we could have just made one call.

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

	if resp.Body == nil || resp.Body.AvailableZones == nil || len(resp.Body.AvailableZones.AvailableZone) == 0 {
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

		if resp.Body == nil || resp.Body.InstanceTypes == nil {
			return nil, errors.New("DescribeInstanceTypesWithOptions failed to return any instance types")
		}

		if resp.Body.NextToken == nil || *resp.Body.NextToken == "" || len(resp.Body.InstanceTypes.InstanceType) == 0 {
			break
		}

		describeInstanceTypesRequest.NextToken = resp.Body.NextToken
		InstanceTypes = append(InstanceTypes, resp.Body.InstanceTypes.InstanceType...)
	}

	return InstanceTypes, nil
}
