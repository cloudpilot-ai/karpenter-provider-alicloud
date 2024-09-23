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
	"net/http"
	"sync"

	"github.com/patrickmn/go-cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type Provider interface {
	LivenessProbe(*http.Request) error
	// List(context.Context, *v1.KubeletConfiguration, *v1.ECSNodeClass) ([]*cloudprovider.InstanceType, error)
	UpdateInstanceTypes(ctx context.Context) error
	UpdateInstanceTypeOfferings(ctx context.Context) error
}

type DefaultProvider struct {
	region string
	// ec2api          ec2iface.ECSAPI
	// subnetProvider  subnet.Provider
	// pricingProvider pricing.Provider

	// Values stored *before* considering insufficient capacity errors from the unavailableOfferings cache.
	// Fully initialized Instance Types are also cached based on the set of all instance types, zones, unavailableOfferings cache,
	// ECSNodeClass, and kubelet configuration from the NodePool

	muInstanceTypeInfo sync.RWMutex
	// TODO @engedaam: Look into only storing the needed ECSInstanceTypeInfo
	// instanceTypesInfo []*ec2.InstanceTypeInfo

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

func NewDefaultProvider(region string, instanceTypesCache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		// ec2api:                ec2api,
		region: region,
		// subnetProvider:        subnetProvider,
		// pricingProvider:       pricingProvider,
		// instanceTypesInfo:     []*ec2.InstanceTypeInfo{},
		instanceTypeOfferings: map[string]sets.Set[string]{},
		instanceTypesCache:    instanceTypesCache,
		// unavailableOfferings:  unavailableOfferingsCache,
		cm:                  pretty.NewChangeMonitor(),
		instanceTypesSeqNum: 0,
	}
}

// func (p *DefaultProvider) List(ctx context.Context, kc *v1.KubeletConfiguration, nodeClass *v1.ECSNodeClass) ([]*cloudprovider.InstanceType, error) {

func (p *DefaultProvider) LivenessProbe(req *http.Request) error {

	// TODO: implement me
	return nil
}

func (p *DefaultProvider) UpdateInstanceTypes(ctx context.Context) error {

	// TODO: implement me
	return nil
}

func (p *DefaultProvider) UpdateInstanceTypeOfferings(ctx context.Context) error {

	// TODO: implement me
	return nil
}
