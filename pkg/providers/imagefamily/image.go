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
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/version"
)

type Provider interface {
	List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (Images, error)
}

type DefaultProvider struct {
	sync.Mutex
	cache           *cache.Cache
	region          string
	ecsapi          ecs.Client
	cm              *pretty.ChangeMonitor
	versionProvider version.Provider
	// TODO: add OOSProvider
}

func NewDefaultProvider(region string, versionProvider version.Provider, ecsapi ecs.Client, cache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		cache:           cache,
		region:          region,
		ecsapi:          ecsapi,
		cm:              pretty.NewChangeMonitor(),
		versionProvider: versionProvider,
	}
}

// List Get Returning a list of Images with its associated requirements
func (p *DefaultProvider) List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (Images, error) {
	p.Lock()
	defer p.Unlock()
	queries, err := p.DescribeImageQueries(ctx, nodeClass)
	if err != nil {
		return nil, fmt.Errorf("getting image queries, %w", err)
	}
	images, err := p.GetImages(queries)
	if err != nil {
		return nil, err
	}
	images.Sort()
	uniqueImages := lo.Uniq(lo.Map(images, func(a Image, _ int) string { return a.ImageID }))
	if p.cm.HasChanged(fmt.Sprintf("images/%s", nodeClass.Name), uniqueImages) {
		log.FromContext(ctx).WithValues(
			"ids", uniqueImages).V(1).Info("discovered images")
	}
	return images, nil
}

func (p *DefaultProvider) DescribeImageQueries(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) ([]DescribeImageQuery, error) {
	// Aliases are mutually exclusive, both on the term level and field level within a term.
	// This is enforced by a CEL validation, we will treat this as an invariant.
	if term, ok := lo.Find(nodeClass.Spec.ImageSelectorTerms, func(term v1alpha1.ImageSelectorTerm) bool {
		return term.Alias != ""
	}); ok {
		kubernetesVersion, err := p.versionProvider.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting kubernetes version, %w", err)
		}
		imageFamily := GetImageFamily(v1alpha1.ImageFamilyFromAlias(term.Alias), nil)
		query, err := imageFamily.DescribeImageQuery(ctx, kubernetesVersion, v1alpha1.ImageVersionFromAlias(term.Alias))
		if err != nil {
			return nil, err
		}
		return query, nil
	}

	var queries []DescribeImageQuery
	for _, term := range nodeClass.Spec.ImageSelectorTerms {
		describeImagesRequest := &ecs.DescribeImagesRequest{
			ImageOwnerAlias: lo.Ternary(term.Owner != "", tea.String(term.Owner), nil),
			IsPublic:        tea.Bool(true),
		}
		if term.Owner == "share" {
			describeImagesRequest.IsPublic = tea.Bool(false)
		}
		if term.ID != "" {
			describeImagesRequest.ImageId = tea.String(term.ID)
		}
		if term.Name != "" {
			describeImagesRequest.ImageName = tea.String(term.Name)
		}
		var tags []*ecs.DescribeImagesRequestTag
		for k, v := range term.Tags {
			tag := &ecs.DescribeImagesRequestTag{Key: tea.String(k)}
			if v != "*" {
				tag.Value = tea.String(v)
			}
			tags = append(tags, tag)
		}
		if len(tags) > 0 {
			describeImagesRequest.Tag = tags
		}
		queries = append(queries, DescribeImageQuery{DescribeImagesRequest: describeImagesRequest})
	}
	return queries, nil
}

//nolint:gocyclo
func (p *DefaultProvider) GetImages(queries []DescribeImageQuery) (Images, error) {
	hash, err := hashstructure.Hash(queries, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	if err != nil {
		return nil, err
	}
	if images, ok := p.cache.Get(fmt.Sprintf("%d", hash)); ok {
		// Ensure what's returned from this function is a deep-copy of Images so alterations
		// to the data don't affect the original
		return append(Images{}, images.(Images)...), nil
	}

	images := map[uint64]Image{}
	for _, query := range queries {
		if err := p.describeImages(query.DescribeImagesRequest, func(image *ecs.DescribeImagesResponseBodyImagesImage) {
			// not support i386
			arch, ok := v1alpha1.AlibabaCloudToKubeArchitectures[lo.FromPtr(image.Architecture)]
			if !ok {
				return
			}

			// Each image may have multiple associated sets of requirements. For example, an image may be compatible with Neuron instances
			// and GPU instances. In that case, we'll have a set of requirements for each, and will create one "image" for each.
			for _, reqs := range query.RequirementsForImageWithArchitecture(arch) {
				// If we already have an image with the same set of requirements, but this image is newer, replace the previous image.
				reqsHash := lo.Must(hashstructure.Hash(reqs.NodeSelectorRequirements(), hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true}))
				if v, ok := images[reqsHash]; ok {
					candidateCreationTime, _ := time.Parse(time.RFC3339, lo.FromPtr(image.CreationTime))
					existingCreationTime, _ := time.Parse(time.RFC3339, v.CreationTime)
					if existingCreationTime == candidateCreationTime && lo.FromPtr(image.ImageName) < v.Name {
						continue
					}
					if candidateCreationTime.Unix() < existingCreationTime.Unix() {
						continue
					}
				}
				images[reqsHash] = Image{
					Name:         lo.FromPtr(image.ImageName),
					ImageID:      lo.FromPtr(image.ImageId),
					CreationTime: lo.FromPtr(image.CreationTime),
					Requirements: reqs,
				}
			}
		}); err != nil {
			return nil, fmt.Errorf("describing images, %w", err)
		}

	}
	p.cache.SetDefault(fmt.Sprintf("%d", hash), Images(lo.Values(images)))
	return lo.Values(images), nil
}

func (p *DefaultProvider) describeImages(request *ecs.DescribeImagesRequest, process func(*ecs.DescribeImagesResponseBodyImagesImage)) error {
	request.RegionId = tea.String(p.region)
	request.PageSize = tea.Int32(100)
	runtime := &util.RuntimeOptions{}
	for pageNumber := int32(1); pageNumber < 500; pageNumber++ {
		request.PageNumber = tea.Int32(pageNumber)
		output, err := p.ecsapi.DescribeImagesWithOptions(request, runtime)
		if err != nil {
			return err
		} else if output == nil || output.Body == nil || output.Body.TotalCount == nil || output.Body.Images == nil {
			return fmt.Errorf("unexpected null value was returned")
		}

		for i := range output.Body.Images.Image {
			process(output.Body.Images.Image[i])
		}

		if *output.Body.TotalCount < pageNumber*100 || len(output.Body.Images.Image) < 100 {
			break
		}
	}
	return nil
}
