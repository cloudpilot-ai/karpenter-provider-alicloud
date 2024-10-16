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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

type Aliyun3 struct {
	*Options
}

func (a Aliyun3) UserData(kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, instanceTypes []*cloudprovider.InstanceType, customUserData *string) string {
	// in-cluster user data
	// curl http://aliacs-k8s-{region}.oss-{region}-internal.aliyuncs.com/public/pkg/run/attach/{clusterversion}/attach_node.sh | bash -s -- --token **** --endpoint 1.x.x.x --cluster-dns 1.x.x.x --cluster-id ...
	//
	// token: secret/bootstrap-token

	//TODO implement me
	panic("implement me")
}

func (a Aliyun3) DescribeImageQuery(ctx context.Context, k8sVersion string, imageVersion string) ([]DescribeImageQuery, error) {
	//TODO implement me
	panic("implement me")
}

func (a Aliyun3) DefaultSystemDisk() *v1alpha1.SystemDisk {
	//TODO implement me
	panic("implement me")
}
