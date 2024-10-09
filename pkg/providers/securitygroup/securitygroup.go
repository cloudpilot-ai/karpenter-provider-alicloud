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

package securitygroup

import (
	"context"
	"fmt"
	"sync"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

type Provider interface {
	List(context.Context, *v1alpha1.ECSNodeClass) ([]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup, error)
}

type DefaultProvider struct {
	sync.Mutex
	region string
	ecsapi ecs.Client
	cache  *cache.Cache
	cm     *pretty.ChangeMonitor
	// TODO: Alibaba Cloud security groups have a limit on the number of IP addresses, may need to prevent miss
	// And the available IPs returned by the API are not real-time. It is likely that an IP cache like VSwitchProvider will be needed later.
}

func NewDefaultProvider(region string, ecsapi ecs.Client, cache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		region: region,
		ecsapi: ecsapi,
		cm:     pretty.NewChangeMonitor(),
		// TODO: Remove cache cache when we utilize the security groups from the ECSNodeClass.status
		cache: cache,
	}
}

func (p *DefaultProvider) List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) ([]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup, error) {
	p.Lock()
	defer p.Unlock()

	// Get SecurityGroups
	filterSets := getFilterSets(nodeClass.Spec.SecurityGroupSelectorTerms)
	securityGroups, err := p.getSecurityGroups(filterSets)
	if err != nil {
		return nil, err
	}
	securityGroupIDs := lo.Map(securityGroups, func(s *ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup, _ int) string {
		return *s.SecurityGroupId
	})
	if p.cm.HasChanged(fmt.Sprintf("security-groups/%s", nodeClass.Name), securityGroupIDs) {
		log.FromContext(ctx).
			WithValues("security-groups", securityGroupIDs).
			V(1).Info("discovered security groups")
	}
	return securityGroups, nil
}

func (p *DefaultProvider) getSecurityGroups(filterSets []*ecs.DescribeSecurityGroupsRequest) ([]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup, error) {
	hash, err := hashstructure.Hash(filterSets, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	if err != nil {
		return nil, err
	}
	if sg, ok := p.cache.Get(fmt.Sprint(hash)); ok {
		// Ensure what's returned from this function is a shallow-copy of the slice (not a deep-copy of the data itself)
		// so that modifications to the ordering of the data don't affect the original
		return append([]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{}, sg.([]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup)...), nil
	}
	securityGroups := map[string]*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{}
	for _, filter := range filterSets {
		if err := p.describeSecurityGroups(filter, func(securityGroup *ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup) {
			securityGroups[lo.FromPtr(securityGroup.SecurityGroupId)] = securityGroup
		}); err != nil {
			return nil, fmt.Errorf("describing security groups %+v, %w", filter, err)
		}
	}
	p.cache.SetDefault(fmt.Sprint(hash), lo.Values(securityGroups))
	return lo.Values(securityGroups), nil
}

func (p *DefaultProvider) describeSecurityGroups(request *ecs.DescribeSecurityGroupsRequest, process func(*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup)) error {
	runtime := &util.RuntimeOptions{}
	request.RegionId = tea.String(p.region)
	request.MaxResults = tea.Int32(100)
	request.IsQueryEcsCount = tea.Bool(true)
	for {
		output, err := p.ecsapi.DescribeSecurityGroupsWithOptions(request, runtime)
		if err != nil {
			return err
		} else if output.Body == nil || output.Body.SecurityGroups == nil {
			return fmt.Errorf("unexpected null value was returned")
		}
		for i := range output.Body.SecurityGroups.SecurityGroup {
			process(output.Body.SecurityGroups.SecurityGroup[i])
		}
		request.NextToken = output.Body.NextToken
		if request.NextToken == nil || *request.NextToken == "" || len(output.Body.SecurityGroups.SecurityGroup) == 0 {
			break
		}
	}
	return nil
}

func getFilterSets(terms []v1alpha1.SecurityGroupSelectorTerm) []*ecs.DescribeSecurityGroupsRequest {
	var filterSets []*ecs.DescribeSecurityGroupsRequest
	for _, term := range terms {
		if term.ID != "" {
			filterSets = append(filterSets, &ecs.DescribeSecurityGroupsRequest{SecurityGroupId: tea.String(term.ID)})
			continue
		}
		if term.Name != "" {
			filterSets = append(filterSets, &ecs.DescribeSecurityGroupsRequest{SecurityGroupName: tea.String(term.Name)})
			continue
		}

		var tags []*ecs.DescribeSecurityGroupsRequestTag
		for k, v := range term.Tags {
			tag := &ecs.DescribeSecurityGroupsRequestTag{Key: tea.String(k)}
			if v != "*" {
				tag.Value = tea.String(v)
			}
			tags = append(tags, tag)
		}
		filterSets = append(filterSets, &ecs.DescribeSecurityGroupsRequest{Tag: tags})
	}

	return filterSets
}
