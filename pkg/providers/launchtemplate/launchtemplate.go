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
	"strings"
	"sync"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/imagefamily"
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
	ID            string
	InstanceTypes []*cloudprovider.InstanceType
	ImageID       string
}

type DefaultProvider struct {
	sync.Mutex
	region string
	ecsapi *ecs.Client
	cache  *cache.Cache
	cm     *pretty.ChangeMonitor

	ClusterEndpoint       string
	imageFamily           imagefamily.Resolver
	securityGroupProvider securitygroup.Provider
	vSwitchProvider       vswitch.Provider
}

func NewDefaultProvider(ctx context.Context, cache *cache.Cache, region string, ecsapi *ecs.Client, imageFamily imagefamily.Resolver,
	securityGroupProvider securitygroup.Provider, vSwitchProvider vswitch.Provider,
	caBundle *string, startAsync <-chan struct{}, kubeDNSIP net.IP, clusterEndpoint string) *DefaultProvider {
	l := &DefaultProvider{
		region: region,
		ecsapi: ecsapi,
		cache:  cache,
		cm:     pretty.NewChangeMonitor(),

		ClusterEndpoint:       clusterEndpoint,
		imageFamily:           imageFamily,
		securityGroupProvider: securityGroupProvider,
		vSwitchProvider:       vSwitchProvider,
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
	p.Lock()
	defer p.Unlock()

	imageOptions, err := p.resolveImageOptions(ctx, nodeClass, lo.Assign(nodeClaim.Labels, map[string]string{karpv1.CapacityTypeLabelKey: capacityType}), tags)
	if err != nil {
		return nil, err
	}
	resolvedLaunchTemplates, err := p.imageFamily.Resolve(ctx, nodeClass, nodeClaim, instanceTypes, capacityType, imageOptions)
	if err != nil {
		return nil, err
	}
	var launchTemplates []*LaunchTemplate
	for _, resolvedLaunchTemplate := range resolvedLaunchTemplates {
		// Ensure the launch template exists, or create it
		id, err := p.ensureLaunchTemplate(ctx, resolvedLaunchTemplate)
		if err != nil {
			return nil, err
		}
		launchTemplates = append(launchTemplates, &LaunchTemplate{Name: LaunchTemplateName(resolvedLaunchTemplate), ID: id, InstanceTypes: resolvedLaunchTemplate.InstanceTypes, ImageID: resolvedLaunchTemplate.ImageID})
	}
	return launchTemplates, nil
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
		ClusterEndpoint: p.ClusterEndpoint,
		SecurityGroups:  nodeClass.Status.SecurityGroups,
		Tags:            tags,
		Labels:          labels,
		NodeClassName:   nodeClass.Name,
	}, nil
}

func (p *DefaultProvider) ensureLaunchTemplate(ctx context.Context, options *imagefamily.LaunchTemplate) (string, error) {
	name := LaunchTemplateName(options)
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("launch-template-name", name))
	// Read from cache
	if launchTemplateID, ok := p.cache.Get(name); ok {
		p.cache.SetDefault(name, launchTemplateID)
		return launchTemplateID.(string), nil
	}
	// Attempt to find an existing LT.
	runtime := &util.RuntimeOptions{}
	output, err := p.ecsapi.DescribeLaunchTemplatesWithOptions(&ecs.DescribeLaunchTemplatesRequest{
		RegionId:           tea.String(p.region),
		LaunchTemplateName: []*string{tea.String(name)},
	}, runtime)

	if err != nil {
		return "", fmt.Errorf("describing launch templates, %w", err)
	} else if output == nil || output.Body == nil || output.Body.LaunchTemplateSets == nil {
		return "", fmt.Errorf("unexpected null value was returned")
	}

	// Create LT if one doesn't exist
	var launchTemplateID string
	if len(output.Body.LaunchTemplateSets.LaunchTemplateSet) == 0 {
		launchTemplateID, err = p.createLaunchTemplate(ctx, options)
		if err != nil {
			return "", fmt.Errorf("creating launch template, %w", err)
		}
	} else if len(output.Body.LaunchTemplateSets.LaunchTemplateSet) != 1 {
		return "", fmt.Errorf("expected to find one launch template, but found %d", len(output.Body.LaunchTemplateSets.LaunchTemplateSet))
	} else {
		if p.cm.HasChanged("launchtemplate-"+name, name) {
			log.FromContext(ctx).V(1).Info("discovered launch template")
		}
		launchTemplateID = *output.Body.LaunchTemplateSets.LaunchTemplateSet[0].LaunchTemplateId
	}
	p.cache.SetDefault(name, launchTemplateID)
	return launchTemplateID, nil
}

func (p *DefaultProvider) createLaunchTemplate(ctx context.Context, options *imagefamily.LaunchTemplate) (string, error) {
	runtime := &util.RuntimeOptions{}
	output, err := p.ecsapi.CreateLaunchTemplateWithOptions(&ecs.CreateLaunchTemplateRequest{
		RegionId:           tea.String(p.region),
		LaunchTemplateName: tea.String(LaunchTemplateName(options)),
		ImageId:            tea.String(options.ImageID),
		SecurityGroupIds:   lo.Map(options.SecurityGroups, func(s v1alpha1.SecurityGroup, _ int) *string { return tea.String(s.ID) }),
		UserData:           tea.String(options.UserData),
		SystemDisk: lo.Ternary(options.SystemDisk == nil, nil, &ecs.CreateLaunchTemplateRequestSystemDisk{
			Category:             options.SystemDisk.Category,
			Size:                 options.SystemDisk.Size,
			DiskName:             options.SystemDisk.DiskName,
			PerformanceLevel:     options.SystemDisk.PerformanceLevel,
			AutoSnapshotPolicyId: options.SystemDisk.AutoSnapshotPolicyID,
			BurstingEnabled:      options.SystemDisk.BurstingEnabled,
		}),
		// TODO: more params needed, HttpTokens, RamRole, NetworkInterface, ImageOwnerAlias ...
		Tag: lo.MapToSlice(options.Tags, func(k, v string) *ecs.CreateLaunchTemplateRequestTag {
			return &ecs.CreateLaunchTemplateRequestTag{Key: tea.String(k), Value: tea.String(v)}
		}),
		TemplateTag: lo.MapToSlice(lo.Assign(options.Tags, map[string]string{v1alpha1.TagManagedLaunchTemplate: options.ClusterName, v1alpha1.LabelNodeClass: options.NodeClassName}), func(k, v string) *ecs.CreateLaunchTemplateRequestTemplateTag {
			return &ecs.CreateLaunchTemplateRequestTemplateTag{Key: tea.String(k), Value: tea.String(v)}
		}),
	}, runtime)
	if err != nil {
		return "", err
	} else if output == nil || output.Body == nil || output.Body.LaunchTemplateId == nil {
		return "", fmt.Errorf("unexpected null output")
	}
	log.FromContext(ctx).WithValues("id", tea.StringValue(output.Body.LaunchTemplateId)).V(1).Info("created launch template")
	return *output.Body.LaunchTemplateId, nil
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
		p.cache.SetDefault(*lt.LaunchTemplateName, *lt.LaunchTemplateId)
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
		launchTemplateID := lt.(string)
		// no guarantee of deletion
		if _, err := p.ecsapi.DeleteLaunchTemplateWithOptions(&ecs.DeleteLaunchTemplateRequest{RegionId: tea.String(p.region), LaunchTemplateId: tea.String(launchTemplateID), LaunchTemplateName: tea.String(key)}, &util.RuntimeOptions{}); err != nil {
			log.FromContext(ctx).WithValues("launch-template", launchTemplateID).Error(err, "failed to delete launch template") // If the LaunchTemplate does not exist, no error is returned.
			return
		}
		log.FromContext(ctx).WithValues("id", launchTemplateID, "name", key).V(1).Info("deleted launch template")
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

func LaunchTemplateName(options *imagefamily.LaunchTemplate) string {
	return fmt.Sprintf("%s/%d", apis.Group, lo.Must(hashstructure.Hash(options, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})))
}
