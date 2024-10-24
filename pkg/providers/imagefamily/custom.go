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
	"encoding/base64"

	"github.com/alibabacloud-go/tea/tea"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

type Custom struct {
	*Options
}

// UserData returns the default userdata script for the Image Family
func (c Custom) UserData(kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, instanceTypes []*cloudprovider.InstanceType, customUserData *string) string {
	return base64.StdEncoding.EncodeToString([]byte(tea.StringValue(customUserData)))
}

func (c Custom) DescribeImageQuery(_ context.Context) (DescribeImageQuery, error) {
	return DescribeImageQuery{}, nil
}

func (c Custom) DefaultSystemDisk() *v1alpha1.SystemDisk {
	return &DefaultSystemDisk
}
