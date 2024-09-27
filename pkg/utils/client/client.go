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

package client

import (
	"errors"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	aliyunconfig "github.com/aliyun/aliyun-cli/config"
)

func NewECSClient() (*ecsclient.Client, error) {
	profile, err := aliyunconfig.LoadCurrentProfile()
	if err != nil {
		return nil, err
	}

	if profile.RegionId == "" {
		return nil, errors.New("regionId must be set in the config file")
	}

	credentialClient, err := profile.GetCredential(nil, nil)
	if err != nil {
		return nil, err
	}

	ecsConfig := &openapi.Config{
		RegionId:   tea.String(profile.RegionId),
		Credential: credentialClient,
	}

	return ecsclient.NewClient(ecsConfig)
}
