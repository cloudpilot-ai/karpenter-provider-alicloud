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

package cloudprovider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/config"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/instance"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/instancetype"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils"
)

var _ cloudprovider.CloudProvider = (*CloudProvider)(nil)

type CloudProvider struct {
	kubeClient client.Client
	recorder   events.Recorder

	instanceTypeProvider instancetype.Provider
	instanceProvider     instance.Provider
}

func New(kubeClient client.Client, recorder events.Recorder) *CloudProvider {
	return &CloudProvider{
		kubeClient: kubeClient,
	}
}

// Create a NodeClaim given the constraints.
func (c *CloudProvider) Create(ctx context.Context, nodeClaim *karpv1.NodeClaim) (*karpv1.NodeClaim, error) {
	// TODO: Implement this
	return nil, nil
}

func (c *CloudProvider) List(ctx context.Context) ([]*karpv1.NodeClaim, error) {
	instances, err := c.instanceProvider.List(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, "listing instances")
		return nil, fmt.Errorf("listing instances, %w", err)
	}
	var nodeClaims []*karpv1.NodeClaim
	for i := range instances {
		instanceType, err := c.resolveInstanceTypeFromInstance(ctx, instances[i])
		if err != nil {
			log.FromContext(ctx).Error(err, "resolving instance type")
			return nil, fmt.Errorf("resolving instance type, %w", err)
		}
		nc, err := c.resolveNodeClassFromInstance(ctx, instances[i])
		if client.IgnoreNotFound(err) != nil {
			log.FromContext(ctx).Error(err, "resolving nodeclass")
			return nil, fmt.Errorf("resolving nodeclass, %w", err)
		}
		nodeClaims = append(nodeClaims, c.instanceToNodeClaim(instances[i], instanceType, nc))
	}
	return nodeClaims, nil
}

func (c *CloudProvider) Get(ctx context.Context, providerID string) (*karpv1.NodeClaim, error) {
	id, err := utils.ParseInstanceID(providerID)
	if err != nil {
		log.FromContext(ctx).Error(err, "parsing instance ID")
		return nil, fmt.Errorf("getting instance ID, %w", err)
	}

	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("id", id))

	instance, err := c.instanceProvider.Get(ctx, id)
	if err != nil {
		log.FromContext(ctx).Error(err, "getting instance")
		return nil, fmt.Errorf("getting instance, %w", err)
	}

	instanceType, err := c.resolveInstanceTypeFromInstance(ctx, instance)
	if err != nil {
		log.FromContext(ctx).Error(err, "resolving instance type")
		return nil, fmt.Errorf("resolving instance type, %w", err)
	}

	nc, err := c.resolveNodeClassFromInstance(ctx, instance)
	if client.IgnoreNotFound(err) != nil {
		log.FromContext(ctx).Error(err, "resolving nodeclass")
		return nil, fmt.Errorf("resolving nodeclass, %w", err)
	}

	return c.instanceToNodeClaim(instance, instanceType, nc), nil
}

func (c *CloudProvider) LivenessProbe(req *http.Request) error {
	return c.instanceTypeProvider.LivenessProbe(req)
}

// GetInstanceTypes returns all available InstanceTypes
func (c *CloudProvider) GetInstanceTypes(ctx context.Context, nodePool *karpv1.NodePool) ([]*cloudprovider.InstanceType, error) {
	// TODO: Implement this
	return nil, nil
}

func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *karpv1.NodeClaim) error {
	// TODO: Implement this
	return nil
}

func (c *CloudProvider) IsDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim) (cloudprovider.DriftReason, error) {
	// TODO: Implement this
	return cloudprovider.DriftReason(""), nil
}

// Name returns the CloudProvider implementation name.
func (c *CloudProvider) Name() string {
	return config.CloudName
}

func (c *CloudProvider) GetSupportedNodeClasses() []status.Object {
	// TODO: Implement this
	return nil
}

func (c *CloudProvider) resolveInstanceTypeFromInstance(ctx context.Context, instance *instance.Instance) (*cloudprovider.InstanceType, error) {
	nodePool, err := c.resolveNodePoolFromInstance(ctx, instance)
	if err != nil {
		// If we can't resolve the NodePool, we fall back to not getting instance type info
		return nil, client.IgnoreNotFound(fmt.Errorf("resolving nodepool, %w", err))
	}
	instanceTypes, err := c.GetInstanceTypes(ctx, nodePool)
	if err != nil {
		// If we can't resolve the NodePool, we fall back to not getting instance type info
		return nil, client.IgnoreNotFound(fmt.Errorf("resolving nodeclass, %w", err))
	}
	instanceType, _ := lo.Find(instanceTypes, func(i *cloudprovider.InstanceType) bool {
		return i.Name == instance.Type
	})
	return instanceType, nil
}

func (c *CloudProvider) resolveNodePoolFromInstance(ctx context.Context, instance *instance.Instance) (*karpv1.NodePool, error) {
	if nodePoolName, ok := instance.Tags[karpv1.NodePoolLabelKey]; ok {
		nodePool := &karpv1.NodePool{}
		if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodePoolName}, nodePool); err != nil {
			return nil, err
		}
		return nodePool, nil
	}
	return nil, errors.NewNotFound(schema.GroupResource{Group: coreapis.Group, Resource: "nodepools"}, "")
}

func (c *CloudProvider) resolveNodeClassFromInstance(ctx context.Context, instance *instance.Instance) (*v1alpha1.ECSNodeClass, error) {
	np, err := c.resolveNodePoolFromInstance(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("resolving nodepool, %w", err)
	}
	return c.resolveNodeClassFromNodePool(ctx, np)
}

func (c *CloudProvider) resolveNodeClassFromNodePool(ctx context.Context, nodePool *karpv1.NodePool) (*v1alpha1.ECSNodeClass, error) {
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodePool.Spec.Template.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, err
	}
	if !nodeClass.DeletionTimestamp.IsZero() {
		// For the purposes of NodeClass CloudProvider resolution, we treat deleting NodeClasses as NotFound,
		// but we return a different error message to be clearer to users
		return nil, newTerminatingNodeClassError(nodeClass.Name)
	}
	return nodeClass, nil
}

// newTerminatingNodeClassError returns a NotFound error for handling by
func newTerminatingNodeClassError(name string) *errors.StatusError {
	qualifiedResource := schema.GroupResource{Group: apis.Group, Resource: "ecsnodeclasses"}
	err := errors.NewNotFound(qualifiedResource, name)
	err.ErrStatus.Message = fmt.Sprintf("%s %q is terminating, treating as not found", qualifiedResource.String(), name)
	return err
}

func (c *CloudProvider) instanceToNodeClaim(i *instance.Instance, instanceType *cloudprovider.InstanceType, _ *v1alpha1.ECSNodeClass) *karpv1.NodeClaim {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	if instanceType != nil {
		labels = utils.GetAllSingleValuedRequirementLabels(instanceType)
		resourceFilter := func(n corev1.ResourceName, v resource.Quantity) bool {
			return !resources.IsZero(v)
		}
		nodeClaim.Status.Capacity = lo.PickBy(instanceType.Capacity, resourceFilter)
		nodeClaim.Status.Allocatable = lo.PickBy(instanceType.Allocatable(), resourceFilter)
	}
	labels[corev1.LabelTopologyZone] = i.Zone
	labels[karpv1.CapacityTypeLabelKey] = i.CapacityType
	if v, ok := i.Tags[karpv1.NodePoolLabelKey]; ok {
		labels[karpv1.NodePoolLabelKey] = v
	}
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations

	nodeClaim.CreationTimestamp = metav1.Time{Time: i.CreationTime}

	// If the instance is stopping or stopped, we set the deletion timestamp to now
	if i.Status == instance.InstanceStatusStopping || i.Status == instance.InstanceStatusStopped {
		nodeClaim.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}

	nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", i.Region, i.ID)
	nodeClaim.Status.ImageID = i.ImageID
	return nodeClaim
}
