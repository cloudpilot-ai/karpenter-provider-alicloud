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

package launchtemplate

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/securitygroup"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/vswitch"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils"
)

type Provider interface {
	EnsureAll(context.Context, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim,
		[]*cloudprovider.InstanceType, string, map[string]string) ([]*LaunchTemplate, error)
	DeleteAll(context.Context, *v1alpha1.ECSNodeClass) error
	InvalidateCache(context.Context, string, string)
}

type LaunchTemplate struct {
	Name          string
	InstanceTypes []*cloudprovider.InstanceType
	ImageID       string
}

type DefaultProvider struct {
	sync.Mutex
	region string
	ecsapi ecs.Client
	cache  *cache.Cache
	cm     *pretty.ChangeMonitor
}

// TODO: add imagefamily args later
func NewDefaultProvider(ctx context.Context, cache *cache.Cache, region string, ecsapi ecs.Client,
	securityGroupProvider securitygroup.Provider, subnetProvider vswitch.Provider,
	caBundle *string, startAsync <-chan struct{}, kubeDNSIP net.IP, clusterEndpoint string) *DefaultProvider {
	l := &DefaultProvider{
		region: region,
		ecsapi: ecsapi,
		cache:  cache,
		cm:     pretty.NewChangeMonitor(),
	}
	l.cache.OnEvicted(l.cachedEvictedFunc(ctx))
	go func() {
		// only hydrate cache once elected leader
		select {
		case <-startAsync:
		case <-ctx.Done():
			return
		}
		l.hydrateCache(ctx)
	}()
	return l
}

func (p *DefaultProvider) EnsureAll(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType, capacityType string, tags map[string]string) ([]*LaunchTemplate, error) {
	//TODO implement me
	panic("implement me")
}

func (p *DefaultProvider) DeleteAll(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) error {
	clusterName := options.FromContext(ctx).ClusterName
	tags := []*ecs.DescribeLaunchTemplatesRequestTemplateTag{{
		Key:   tea.String(v1alpha1.TagManagedLaunchTemplate),
		Value: tea.String(clusterName),
	}, {
		Key:   tea.String(v1alpha1.LabelNodeClass),
		Value: tea.String(nodeClass.Name),
	}}

	var ltNames []*string
	if err := p.describeLaunchTemplates(&ecs.DescribeLaunchTemplatesRequest{RegionId: tea.String(p.region), TemplateTag: tags}, func(lt *ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet) {
		ltNames = append(ltNames, lt.LaunchTemplateName)
	}); err != nil {
		log.FromContext(ctx).Error(err, "describe LaunchTemplates error")
		return fmt.Errorf("fetching launch templates, %w", err)
	}

	var deleteErr error
	for _, name := range ltNames {
		_, err := p.ecsapi.DeleteLaunchTemplateWithOptions(&ecs.DeleteLaunchTemplateRequest{RegionId: tea.String(p.region), LaunchTemplateName: name}, &util.RuntimeOptions{})
		deleteErr = multierr.Append(deleteErr, err)
	}
	if len(ltNames) > 0 {
		log.FromContext(ctx).WithValues("launchTemplates", utils.PrettySlice(lo.FromSlicePtr(ltNames), 5)).V(1).Info("deleted launch templates")
	}
	if deleteErr != nil {
		return fmt.Errorf("deleting launch templates, %w", deleteErr)
	}
	return nil
}

// InvalidateCache deletes a launch template from cache if it exists
func (p *DefaultProvider) InvalidateCache(ctx context.Context, ltName string, ltID string) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("launch-template-name", ltName, "launch-template-id", ltID))
	p.Lock()
	defer p.Unlock()
	defer p.cache.OnEvicted(p.cachedEvictedFunc(ctx))
	p.cache.OnEvicted(nil)
	log.FromContext(ctx).V(1).Info("invalidating launch template in the cache because it no longer exists")
	p.cache.Delete(ltName)
}

// hydrateCache queries for existing Launch Templates created by Karpenter for the current cluster and adds to the LT cache.
// Any error during hydration will result in a panic
func (p *DefaultProvider) hydrateCache(ctx context.Context) {
	clusterName := options.FromContext(ctx).ClusterName
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("tag-key", v1alpha1.TagManagedLaunchTemplate, "tag-value", clusterName))
	tags := []*ecs.DescribeLaunchTemplatesRequestTemplateTag{{
		Key:   tea.String(v1alpha1.TagManagedLaunchTemplate),
		Value: tea.String(clusterName),
	}}
	if err := p.describeLaunchTemplates(&ecs.DescribeLaunchTemplatesRequest{RegionId: tea.String(p.region), TemplateTag: tags}, func(lt *ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet) {
		p.cache.SetDefault(*lt.LaunchTemplateName, lt)
	}); err != nil {
		log.FromContext(ctx).Error(err, "unable to hydrate the AWS launch template cache")
	}
	log.FromContext(ctx).WithValues("count", p.cache.ItemCount()).V(1).Info("hydrated launch template cache")
}

func (p *DefaultProvider) cachedEvictedFunc(ctx context.Context) func(string, interface{}) {
	return func(key string, lt interface{}) {
		p.Lock()
		defer p.Unlock()
		if _, expiration, _ := p.cache.GetWithExpiration(key); expiration.After(time.Now()) {
			return
		}
		launchTemplate := lt.(*ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet)
		// no guarantee of deletion
		if _, err := p.ecsapi.DeleteLaunchTemplateWithOptions(&ecs.DeleteLaunchTemplateRequest{RegionId: tea.String(p.region), LaunchTemplateId: launchTemplate.LaunchTemplateId, LaunchTemplateName: launchTemplate.LaunchTemplateName}, &util.RuntimeOptions{}); err != nil {
			log.FromContext(ctx).WithValues("launch-template", launchTemplate).Error(err, "failed to delete launch template") // If the LaunchTemplate does not exist, no error is returned.
			return
		}
		log.FromContext(ctx).WithValues("id", *launchTemplate.LaunchTemplateId, "name", *launchTemplate.LaunchTemplateName).V(1).Info("deleted launch template")
	}
}

func (p *DefaultProvider) describeLaunchTemplates(request *ecs.DescribeLaunchTemplatesRequest, process func(*ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet)) error {
	runtime := &util.RuntimeOptions{}
	request.PageSize = tea.Int32(50)
	for pageNumber := int32(1); pageNumber < 500; pageNumber++ {
		request.PageNumber = tea.Int32(pageNumber)
		output, err := p.ecsapi.DescribeLaunchTemplatesWithOptions(request, runtime)
		if err != nil {
			return err
		} else if output == nil || output.Body == nil || output.Body.TotalCount == nil || output.Body.LaunchTemplateSets == nil {
			return fmt.Errorf("unexpected null value was returned")
		}

		for i := range output.Body.LaunchTemplateSets.LaunchTemplateSet {
			process(output.Body.LaunchTemplateSets.LaunchTemplateSet[i])
		}

		if *output.Body.TotalCount < pageNumber*50 || len(output.Body.LaunchTemplateSets.LaunchTemplateSet) < 50 {
			break
		}
	}
	return nil
}
