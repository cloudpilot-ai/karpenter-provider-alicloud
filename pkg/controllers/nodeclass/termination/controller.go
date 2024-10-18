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

package termination

import (
	"context"
	"fmt"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/launchtemplate"
)

type Controller struct {
	kubeClient client.Client
	recorder   events.Recorder

	launchTemplateProvider launchtemplate.Provider
}

func NewController(kubeClient client.Client, recorder events.Recorder, launchTemplateProvider launchtemplate.Provider) *Controller {
	return &Controller{
		kubeClient:             kubeClient,
		recorder:               recorder,
		launchTemplateProvider: launchTemplateProvider,
	}
}

func (c *Controller) Reconcile(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodeclass.termination")

	if !nodeClass.GetDeletionTimestamp().IsZero() {
		return c.finalize(ctx, nodeClass)
	}
	return reconcile.Result{}, nil
}

func (c *Controller) finalize(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (reconcile.Result, error) {
	stored := nodeClass.DeepCopy()
	if !controllerutil.ContainsFinalizer(nodeClass, v1alpha1.TerminationFinalizer) {
		return reconcile.Result{}, nil
	}
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList, client.MatchingFields{"spec.nodeClassRef.name": nodeClass.Name}); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing nodeclaims that are using nodeclass, %w", err)
	}
	if len(nodeClaimList.Items) > 0 {
		c.recorder.Publish(WaitingOnNodeClaimTerminationEvent(nodeClass, lo.Map(nodeClaimList.Items, func(nc karpv1.NodeClaim, _ int) string { return nc.Name })))
		return reconcile.Result{RequeueAfter: time.Minute * 10}, nil // periodically fire the event
	}
	if err := c.launchTemplateProvider.DeleteAll(ctx, nodeClass); err != nil {
		return reconcile.Result{}, fmt.Errorf("deleting launch templates, %w", err)
	}
	controllerutil.RemoveFinalizer(nodeClass, v1alpha1.TerminationFinalizer)
	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			if errors.IsConflict(err) {
				return reconcile.Result{Requeue: true}, nil
			}
			return reconcile.Result{}, client.IgnoreNotFound(fmt.Errorf("removing termination finalizer, %w", err))
		}
	}
	return reconcile.Result{}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclass.termination").
		For(&v1alpha1.ECSNodeClass{}).
		Watches(
			&karpv1.NodeClaim{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
				nc := o.(*karpv1.NodeClaim)
				if nc.Spec.NodeClassRef == nil {
					return nil
				}
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: nc.Spec.NodeClassRef.Name}}}
			}),
			// Watch for NodeClaim deletion events
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool { return false },
				DeleteFunc: func(e event.DeleteEvent) bool { return true },
			}),
		).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
