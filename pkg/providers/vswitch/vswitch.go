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

package vswitch

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

type Provider interface {
	LivenessProbe(*http.Request) error
	List(context.Context, *v1alpha1.ECSNodeClass) ([]*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch, error)
	ZonalVSwitchesForLaunch(context.Context, *v1alpha1.ECSNodeClass, []*cloudprovider.InstanceType, string) (map[string]*VSwitch, error)
	UpdateInflightIPs(*ecs.CreateAutoProvisioningGroupRequest, *ecs.DescribeInstancesResponseBodyInstances, []*cloudprovider.InstanceType, []*VSwitch, string)
}

type DefaultProvider struct {
	sync.Mutex
	vpcapi                  *vpc.Client
	cache                   *cache.Cache
	availableIPAddressCache *cache.Cache
	cm                      *pretty.ChangeMonitor
	inflightIPs             map[string]int64
}

type VSwitch struct {
	ID                      string
	ZoneID                  string
	AvailableIPAddressCount int64
}

func NewDefaultProvider(vpcapi *vpc.Client, cache *cache.Cache, availableIPAddressCache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		vpcapi: vpcapi,
		cm:     pretty.NewChangeMonitor(),
		// TODO: Remove cache when we utilize the resolved vSwitches from the ECSNodeClass.status
		// VSwitches are sorted on AvailableIpAddressCount, descending order
		cache:                   cache,
		availableIPAddressCache: availableIPAddressCache,
		// inflightIPs is used to track IPs from known launched instances
		inflightIPs: map[string]int64{},
	}
}

func (p *DefaultProvider) List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) ([]*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch, error) {
	p.Lock()
	defer p.Unlock()

	if len(nodeClass.Spec.VSwitchSelectorTerms) == 0 {
		return []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{}, nil
	}
	hash, err := hashstructure.Hash(nodeClass.Spec.VSwitchSelectorTerms, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	if err != nil {
		return nil, err
	}
	if switches, ok := p.cache.Get(fmt.Sprint(hash)); ok {
		// Ensure what's returned from this function is a shallow-copy of the slice (not a deep-copy of the data itself)
		// so that modifications to the ordering of the data don't affect the original
		return append([]*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{}, switches.([]*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch)...), nil
	}

	// Ensure that all the vSwitches that are returned here are unique
	vSwitches := map[string]*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{}
	for _, selectorTerms := range nodeClass.Spec.VSwitchSelectorTerms {
		var tags []*vpc.DescribeVSwitchesRequestTag
		var vSwitchID *string

		if len(selectorTerms.ID) > 0 {
			vSwitchID = tea.String(selectorTerms.ID)
		}
		for k, v := range selectorTerms.Tags {
			// Value: nil selector all switches, '' selector specify switch
			tag := &vpc.DescribeVSwitchesRequestTag{Key: tea.String(k)}
			if v != "*" {
				tag.Value = tea.String(v)
			}
			tags = append(tags, tag)
		}

		// API Rate Limits: 360/60(s), Max selector items: 30
		// TODO: additional rate limits
		if err = p.describeVSwitches(tags, vSwitchID, func(vSwitch *vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch) {
			vSwitches[lo.FromPtr(vSwitch.VSwitchId)] = vSwitch
			// switches can be leaked here, if a switch is never called received from ecs
			// we are accepting it for now, as this will be an insignificant amount of memory
			p.availableIPAddressCache.SetDefault(lo.FromPtr(vSwitch.VSwitchId), lo.FromPtr(vSwitch.AvailableIpAddressCount))

			delete(p.inflightIPs, lo.FromPtr(vSwitch.VSwitchId)) // remove any previously tracked IP addresses since we just refreshed from ECS
		}); err != nil {
			return nil, fmt.Errorf("describing vSwitches %s, %w", pretty.Concise(selectorTerms), err)
		}
	}

	p.cache.SetDefault(fmt.Sprint(hash), lo.Values(vSwitches))
	if p.cm.HasChanged(fmt.Sprintf("vSwitches/%s", nodeClass.Name), lo.Keys(vSwitches)) {
		log.FromContext(ctx).
			WithValues("vSwitches", lo.Map(lo.Values(vSwitches), func(v *vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch, _ int) v1alpha1.VSwitch {
				return v1alpha1.VSwitch{
					ID:     lo.FromPtr(v.VSwitchId),
					ZoneID: lo.FromPtr(v.ZoneId),
				}
			})).V(1).Info("discovered vSwitches")
	}
	return lo.Values(vSwitches), nil
}

// ZonalVSwitchesForLaunch returns a mapping of zone to the vSwitch with the most available IP addresses and deducts the passed ips from the available count
func (p *DefaultProvider) ZonalVSwitchesForLaunch(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, instanceTypes []*cloudprovider.InstanceType, capacityType string) (map[string]*VSwitch, error) {
	if len(nodeClass.Status.VSwitches) == 0 {
		return nil, fmt.Errorf("no vSwitches matched selector %v", nodeClass.Spec.VSwitchSelectorTerms)
	}

	p.Lock()
	defer p.Unlock()

	availableIPAddressCount := map[string]int64{}
	for _, vSwitch := range nodeClass.Status.VSwitches {
		if availableIP, ok := p.availableIPAddressCache.Get(vSwitch.ID); ok {
			availableIPAddressCount[vSwitch.ID] = availableIP.(int64)
		}
	}

	zonalVSwitches := map[string]*VSwitch{}
	for _, vSwitch := range nodeClass.Status.VSwitches {
		if v, ok := zonalVSwitches[vSwitch.ZoneID]; ok {
			currentZonalVSwitchIPAddressCount := v.AvailableIPAddressCount
			newZonalVSwitchIPAddressCount := availableIPAddressCount[vSwitch.ID]
			if ips, ok := p.inflightIPs[v.ID]; ok {
				currentZonalVSwitchIPAddressCount = ips
			}
			if ips, ok := p.inflightIPs[vSwitch.ID]; ok {
				newZonalVSwitchIPAddressCount = ips
			}

			if currentZonalVSwitchIPAddressCount >= newZonalVSwitchIPAddressCount {
				continue
			}
		}
		zonalVSwitches[vSwitch.ZoneID] = &VSwitch{ID: vSwitch.ID, ZoneID: vSwitch.ZoneID, AvailableIPAddressCount: availableIPAddressCount[vSwitch.ID]}
	}

	for _, vSwitch := range zonalVSwitches {
		predictedIPsUsed := p.minPods(instanceTypes, scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, vSwitch.ZoneID),
		))
		prevIPs := vSwitch.AvailableIPAddressCount
		if trackedIPs, ok := p.inflightIPs[vSwitch.ID]; ok {
			prevIPs = trackedIPs
		}
		p.inflightIPs[vSwitch.ID] = prevIPs - predictedIPsUsed
	}
	return zonalVSwitches, nil
}

// UpdateInflightIPs is used to refresh the in-memory IP usage by adding back unused IPs after a CreateAutoProvisioningGroup response is returned
func (p *DefaultProvider) UpdateInflightIPs(createAutoProvisioningGroupRequest *ecs.CreateAutoProvisioningGroupRequest, createAutoProvisioningGroupResponse *ecs.DescribeInstancesResponseBodyInstances, instanceTypes []*cloudprovider.InstanceType,
	vSwitches []*VSwitch, capacityType string) {
	p.Lock()
	defer p.Unlock()

	// Process the CreateAutoProvisioningGroupRequest to pull out all the requested VSwitchIDs
	reqeustVSwitches := lo.Compact(lo.Uniq(lo.Map(createAutoProvisioningGroupRequest.LaunchTemplateConfig, func(req *ecs.CreateAutoProvisioningGroupRequestLaunchTemplateConfig, _ int) string {
		if req == nil {
			return ""
		}
		return lo.FromPtr(req.VSwitchId)
	})))

	// Process the CreateAutoProvisioningGroupResponse to pull out all the fulfilled VSwitchIDs
	var responseVSwitches []string
	if createAutoProvisioningGroupResponse != nil {
		responseVSwitches = lo.Compact(lo.Uniq(lo.Map(createAutoProvisioningGroupResponse.Instance, func(instance *ecs.DescribeInstancesResponseBodyInstancesInstance, _ int) string {
			if instance == nil || instance.VpcAttributes == nil || instance.VpcAttributes.VSwitchId == nil {
				return ""
			}
			return lo.FromPtr(instance.VpcAttributes.VSwitchId)
		})))
	}

	// Find the VSwitches that were included in the input but not chosen by Fleet, so we need to add the inflight IPs back to them
	vSwitchIDsToAddBackIPs, _ := lo.Difference(reqeustVSwitches, responseVSwitches)

	// Aggregate all the cached vSwitches ip address count
	cachedAvailableIPAddressMap := lo.MapEntries(p.availableIPAddressCache.Items(), func(k string, v cache.Item) (string, int64) {
		return k, v.Object.(int64)
	})

	// Update the inflight IP tracking of vSwitches stored in the cache that have not be synchronized since the initial
	// deduction of IP addresses before the instance launch
	for cachedVSwitchID, cachedIPAddressCount := range cachedAvailableIPAddressMap {
		if !lo.Contains(vSwitchIDsToAddBackIPs, cachedVSwitchID) {
			continue
		}
		originalVSwitch, ok := lo.Find(vSwitches, func(vSwitch *VSwitch) bool {
			return vSwitch.ID == cachedVSwitchID
		})
		if !ok {
			continue
		}
		// If the cached vSwitch IP address count hasn't changed from the original vSwitch used to
		// launch the instance, then we need to update the tracked IPs
		if originalVSwitch.AvailableIPAddressCount == cachedIPAddressCount {
			// other IPs deducted were opportunistic and need to be readded since Fleet didn't pick those vSwitches to launch into
			if ips, ok := p.inflightIPs[originalVSwitch.ID]; ok {
				minPods := p.minPods(instanceTypes, scheduling.NewRequirements(
					scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, originalVSwitch.ZoneID),
				))
				p.inflightIPs[originalVSwitch.ID] = ips + minPods
			}
		}
	}
}

func (p *DefaultProvider) LivenessProbe(_ *http.Request) error {
	p.Lock()
	//nolint: staticcheck
	p.Unlock()
	return nil
}

func (p *DefaultProvider) describeVSwitches(tags []*vpc.DescribeVSwitchesRequestTag, id *string, process func(*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch)) error {
	runtime := &util.RuntimeOptions{}
	describeVSwitchesRequest := &vpc.DescribeVSwitchesRequest{
		Tag:       tags,
		VSwitchId: id,
		PageSize:  tea.Int32(50),
	}
	for pageNumber := int32(1); pageNumber < 360; pageNumber++ {
		describeVSwitchesRequest.PageNumber = tea.Int32(pageNumber)
		output, err := p.vpcapi.DescribeVSwitchesWithOptions(describeVSwitchesRequest, runtime)
		if err != nil {
			return err
		} else if output.Body == nil || output.Body.TotalCount == nil || output.Body.VSwitches == nil {
			return fmt.Errorf("unexpected null value was returned")
		}

		for i := range output.Body.VSwitches.VSwitch {
			process(output.Body.VSwitches.VSwitch[i])
		}

		if *output.Body.TotalCount < pageNumber*50 || len(output.Body.VSwitches.VSwitch) < 50 {
			break
		}
	}
	return nil
}

func (p *DefaultProvider) minPods(instanceTypes []*cloudprovider.InstanceType, reqs scheduling.Requirements) int64 {
	// filter for instance types available in the zone and capacity type being requested
	filteredInstanceTypes := lo.Filter(instanceTypes, func(it *cloudprovider.InstanceType, _ int) bool {
		return it.Offerings.Available().HasCompatible(reqs)
	})
	if len(filteredInstanceTypes) == 0 {
		return 0
	}
	// Get minimum pods to use when selecting a vSwitch and deducting what will be launched
	pods, _ := lo.MinBy(filteredInstanceTypes, func(i *cloudprovider.InstanceType, j *cloudprovider.InstanceType) bool {
		return i.Capacity.Pods().Cmp(*j.Capacity.Pods()) < 0
	}).Capacity.Pods().AsInt64()
	return pods
}
