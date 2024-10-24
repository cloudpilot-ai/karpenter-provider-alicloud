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
	"regexp"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
)

var (
	// The format should look like aliyun_2_arm64_20G_alibase_20240819.vhd
	alibabaCloudLinux2ImageIDRegex = regexp.MustCompile("aliyun_2_.*G_alibase_.*vhd")
)

type AlibabaCloudLinux2 struct {
	*Options
}

func (a AlibabaCloudLinux2) UserData(kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, instanceTypes []*cloudprovider.InstanceType, customUserData *string) string {
	// in-cluster user data
	// curl http://aliacs-k8s-{region}.oss-{region}-internal.aliyuncs.com/public/pkg/run/attach/{clusterversion}/attach_node.sh | bash -s -- --token **** --endpoint 1.x.x.x --cluster-dns 1.x.x.x --cluster-id ...
	//
	// token: secret/bootstrap-token

	// TODO: implement me
	return base64.StdEncoding.EncodeToString([]byte("sleep 300"))
}

func alibabaCloudLinux2ImageFilterFunc(imageID string) bool {
	return alibabaCloudLinux2ImageIDRegex.Match([]byte(imageID))
}

func (a AlibabaCloudLinux2) DescribeImageQuery(_ context.Context) (DescribeImageQuery, error) {
	return DescribeImageQuery{
		BaseQuery: DescribeImageQueryBase{
			DescribeImagesRequest: &ecs.DescribeImagesRequest{
				ImageOwnerAlias: tea.String("system"),
				OSType:          tea.String("linux"),
				ActionType:      tea.String("CreateEcs"),
			},
		},
		FilterFunc: alibabaCloudLinux2ImageFilterFunc,
	}, nil
}

func (a AlibabaCloudLinux2) DefaultSystemDisk() *v1alpha1.SystemDisk {
	return &DefaultSystemDisk
}
