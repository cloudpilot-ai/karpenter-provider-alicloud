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

package pricing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDefaultProvider(t *testing.T) {
	type args struct {
		region       string
		instanceType string
	}

	tests := []struct {
		name string
		args args
	}{
		{
			name: "test cn-beijing region with od and spot price",
			args: args{
				region:       "cn-beijing",
				instanceType: "ecs.i2.xlarge",
			},
		},
		{
			name: "test us-east-1 region with od and spot price",
			args: args{
				region:       "us-east-1",
				instanceType: "ecs.i2.xlarge",
			},
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewDefaultProvider(ctx, tt.args.region)
			assert.NoError(t, err)
			assert.NoError(t, provider.UpdateSpotPricing(ctx))
			assert.NoError(t, provider.UpdateOnDemandPricing(ctx))

			assert.NotZero(t, provider.onDemandPrices[tt.args.instanceType])
			for _, info := range provider.spotPrices {
				for _, v := range info.prices {
					assert.NotZero(t, v)
				}
			}
		})
	}
}
