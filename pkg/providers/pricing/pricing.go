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

package pricing

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/tools"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	utilsobject "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils/object"
)

//go:embed initial-on-demand-prices.json
var initialOnDemandPricesData []byte

const defaultRegion = "cn-qingdao"

type Provider interface {
	LivenessProbe(*http.Request) error
	InstanceTypes() []string
	OnDemandPrice(string) (float64, bool)
	SpotPrice(string, string) (float64, bool)
	UpdateOnDemandPricing(context.Context) error
	UpdateSpotPricing(context.Context) error
}

// DefaultProvider provides actual pricing data to the AlibabaCloud provider to allow it to make more informed decisions
// regarding which instances to launch. This is initialized at startup with a periodically updated static price list to
// support running in locations where pricing data is unavailable.  In those cases the static pricing data provides a
// relative ordering that is still more accurate than our previous pricing model.  In the event that a pricing update
// fails, the previous pricing information is retained and used which may be the static initial pricing data if pricing
// updates never succeed.
type DefaultProvider struct {
	muPriceLastUpdatedTimestamp sync.RWMutex
	priceLastUpdatedTimestamp   time.Time
	alibabaCloudPriceClient     tools.QueryClientInterface

	region string
	cm     *pretty.ChangeMonitor

	muOnDemand     sync.RWMutex
	onDemandPrices map[string]float64

	muSpot             sync.RWMutex
	spotPrices         map[string]zonal
	spotPricingUpdated bool
}

// zonalPricing is used to capture the per-zone price
// for spot data as well as the default price
// based on on-demand price when the provisioningController first
// comes up
type zonal struct {
	defaultPrice float64 // Used until we get the spot pricing data
	prices       map[string]float64
}

func newZonalPricing(defaultPrice float64) zonal {
	z := zonal{
		prices: map[string]float64{},
	}
	z.defaultPrice = defaultPrice
	return z
}

const (
	// defaultPriceQueryClient defines the default query endpoint
	// we do not use the alibabacloud sdk because it has a rate limit which not satisfied with our request frequency
	// you can build your own query server with repo: https://github.com/cloudpilot-ai/priceserver
	// TODO: update to production endpoint latter
	defaultPriceQueryEndpoint = "https://pre-price.cloudpilot.ai"
)

func NewDefaultProvider(ctx context.Context, region string) (*DefaultProvider, error) {
	queryClient, err := tools.NewQueryClient(defaultPriceQueryEndpoint, tools.AlibabaCloudProvider, region)
	if err != nil {
		log.FromContext(ctx).Error(err, "unable to create query client")
		return nil, err
	}
	p := &DefaultProvider{
		region:                  region,
		alibabaCloudPriceClient: queryClient,

		cm: pretty.NewChangeMonitor(),
	}
	// sets the pricing data from the static default state for the provider
	p.Reset()

	return p, nil
}

// InstanceTypes returns the list of all instance types for which either a spot or on-demand price is known.
func (p *DefaultProvider) InstanceTypes() []string {
	p.muOnDemand.RLock()
	p.muSpot.RLock()
	defer p.muOnDemand.RUnlock()
	defer p.muSpot.RUnlock()
	return lo.Union(lo.Keys(p.onDemandPrices), lo.Keys(p.spotPrices))
}

// OnDemandPrice returns the last known on-demand price for a given instance type, returning an error if there is no
// known on-demand pricing for the instance type.
func (p *DefaultProvider) OnDemandPrice(instanceType string) (float64, bool) {
	p.muOnDemand.RLock()
	defer p.muOnDemand.RUnlock()
	price, ok := p.onDemandPrices[instanceType]
	if !ok {
		return 0.0, false
	}
	return price, true
}

// SpotPrice returns the last known spot price for a given instance type and zone, returning an error
// if there is no known spot pricing for that instance type or zone
func (p *DefaultProvider) SpotPrice(instanceType string, zone string) (float64, bool) {
	p.muSpot.RLock()
	defer p.muSpot.RUnlock()
	if val, ok := p.spotPrices[instanceType]; ok {
		if !p.spotPricingUpdated {
			return val.defaultPrice, true
		}
		if price, ok := p.spotPrices[instanceType].prices[zone]; ok {
			return price, true
		}
		return 0.0, false
	}
	return 0.0, false
}

func populateInitialSpotPricing(pricing map[string]float64) map[string]zonal {
	m := map[string]zonal{}
	for it, price := range pricing {
		m[it] = newZonalPricing(price)
	}
	return m
}

func (p *DefaultProvider) LivenessProbe(_ *http.Request) error {
	// ensure we don't deadlock and nolint for the empty critical section
	p.muOnDemand.Lock()
	p.muSpot.Lock()
	p.muPriceLastUpdatedTimestamp.Lock()
	//nolint: staticcheck
	p.muOnDemand.Unlock()
	p.muSpot.Unlock()
	p.muPriceLastUpdatedTimestamp.Unlock()
	return nil
}

func (p *DefaultProvider) Reset() {
	initialOnDemandPrices := *utilsobject.JSONUnmarshal[map[string]map[string]float64](initialOnDemandPricesData)
	// see if we've got region specific pricing data
	staticPricing, ok := initialOnDemandPrices[p.region]
	if !ok {
		// and if not, fall back to the always available eastus
		staticPricing = initialOnDemandPrices[defaultRegion]
	}

	p.onDemandPrices = staticPricing
	// default our spot pricing to the same as the on-demand pricing until a price update
	p.spotPrices = populateInitialSpotPricing(staticPricing)
	p.spotPricingUpdated = false
}

func (p *DefaultProvider) UpdateOnDemandPricing(ctx context.Context) error {
	if err := p.syncPricingData(ctx); err != nil {
		return err
	}

	prices := p.alibabaCloudPriceClient.ListInstancesDetails(p.region)
	if prices == nil || len(prices.InstanceTypePrices) == 0 {
		err := fmt.Errorf("no price info available for region %s", p.region)
		log.FromContext(ctx).Error(err, "failed to get on-demand pricing data from alibaba cloud")
		return err
	}

	p.muOnDemand.Lock()
	defer p.muOnDemand.Unlock()
	p.onDemandPrices = lo.MapEntries(prices.InstanceTypePrices, func(key string, value *apis.InstanceTypePrice) (string, float64) {
		return key, value.OnDemandPricePerHour
	})

	return nil
}
func (p *DefaultProvider) UpdateSpotPricing(ctx context.Context) error {
	if err := p.syncPricingData(ctx); err != nil {
		return err
	}

	prices := p.alibabaCloudPriceClient.ListInstancesDetails(p.region)
	if prices == nil || len(prices.InstanceTypePrices) == 0 {
		err := fmt.Errorf("no price info available for region %s", p.region)
		log.FromContext(ctx).Error(err, "failed to get spot pricing data from alibaba cloud")
		return err
	}

	totalOfferings := 0
	p.muSpot.Lock()
	defer p.muSpot.Unlock()
	for instanceType, priceInfo := range prices.InstanceTypePrices {
		if _, ok := p.spotPrices[instanceType]; !ok {
			p.spotPrices[instanceType] = newZonalPricing(0)
		}
		for zone, price := range priceInfo.SpotPricePerHour {
			p.spotPrices[instanceType].prices[zone] = price
		}
		totalOfferings += len(priceInfo.SpotPricePerHour)
	}

	p.spotPricingUpdated = true
	if p.cm.HasChanged("spot-prices", p.spotPrices) {
		log.FromContext(ctx).WithValues(
			"instance-type-count", len(p.onDemandPrices),
			"offering-count", totalOfferings).V(1).Info("updated spot pricing with instance types and offerings")
	}

	return nil
}

func (p *DefaultProvider) syncPricingData(ctx context.Context) error {
	p.muPriceLastUpdatedTimestamp.Lock()
	lastUpdatedTime := p.priceLastUpdatedTimestamp
	p.muPriceLastUpdatedTimestamp.Unlock()

	if lastUpdatedTime.Add(time.Minute * 5).Before(time.Now()) {
		if err := p.alibabaCloudPriceClient.Sync(); err != nil {
			log.FromContext(ctx).Error(err, "failed to sync pricing data from alibaba cloud")
			return err
		}
		p.muPriceLastUpdatedTimestamp.Lock()
		p.priceLastUpdatedTimestamp = time.Now()
		p.muPriceLastUpdatedTimestamp.Unlock()
	}

	return nil
}
