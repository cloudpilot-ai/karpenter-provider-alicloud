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

package controllers

import (
	"context"

	"github.com/awslabs/operatorpkg/controller"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"

	nodeclaimgarbagecollection "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/controllers/nodeclaim/garbagecollection"
	nodeclaimtagging "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/controllers/nodeclaim/tagging"
	controllerspricing "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/controllers/providers/pricing"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/instance"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/pricing"
)

func NewControllers(ctx context.Context, mgr manager.Manager, clk clock.Clock, kubeClient client.Client, recorder events.Recorder,
	cloudProvider cloudprovider.CloudProvider, instanceProvider instance.Provider, pricingProvider pricing.Provider) []controller.Controller {

	controllers := []controller.Controller{
		controllerspricing.NewController(pricingProvider),
		nodeclaimgarbagecollection.NewController(kubeClient, cloudProvider),
		nodeclaimtagging.NewController(kubeClient, instanceProvider),
	}
	return controllers
}
