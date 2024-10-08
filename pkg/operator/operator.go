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

package operator

import (
	"context"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/operator"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/instance"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/providers/pricing"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils/client"
)

// Operator is injected into the AliCloud CloudProvider's factories
type Operator struct {
	*operator.Operator

	InstanceProvider instance.Provider
	PricingProvider  pricing.Provider
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	ecsClient, err := client.NewECSClient()
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create ECS client")
		os.Exit(1)
	}

	pricingProvider := pricing.NewDefaultProvider(
		ctx,
		ecsClient,
		*ecsClient.RegionId,
	)

	instanceProvider := instance.NewDefaultProvider(
		ctx,
		*ecsClient.RegionId,
		ecsClient,
	)

	return ctx, &Operator{
		Operator: operator,

		InstanceProvider: instanceProvider,
		PricingProvider:  pricingProvider,
	}
}
