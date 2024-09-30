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
	"fmt"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"go.uber.org/multierr"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils/alierrors"
)

type Provider interface {
	Create(context.Context, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim, []*cloudprovider.InstanceType) (*Instance, error)
	Get(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Delete(context.Context, string) error
	CreateTags(context.Context, string, map[string]string) error
}

type DefaultProvider struct {
	ecsClient *ecsclient.Client
	region    string
}

func NewDefaultProvider(ctx context.Context, region string, ecsClient *ecsclient.Client) *DefaultProvider {
	return &DefaultProvider{
		ecsClient: ecsClient,
		region:    region,
	}
}

func (p *DefaultProvider) Create(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType,
) (*Instance, error) {
	// TODO: implement me
	return nil, nil
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
