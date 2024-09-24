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

package v1alpha1

import (
	"github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
)

const (
	ConditionTypeSubnetsReady         = "SubnetsReady"
	ConditionTypeSecurityGroupsReady  = "SecurityGroupsReady"
	ConditionTypeAMIsReady            = "AMIsReady"
	ConditionTypeInstanceProfileReady = "InstanceProfileReady"
)

// Subnet contains resolved Subnet selector values utilized for node launch
type Subnet struct {
	// ID of the subnet
	// +required
	ID string `json:"id"`
	// The associated availability zone
	// +required
	Zone string `json:"zone"`
	// The associated availability zone ID
	// +optional
	ZoneID string `json:"zoneID,omitempty"`
}

// SecurityGroup contains resolved SecurityGroup selector values utilized for node launch
type SecurityGroup struct {
	// ID of the security group
	// +required
	ID string `json:"id"`
	// Name of the security group
	// +optional
	Name string `json:"name,omitempty"`
}

// AMI contains resolved AMI selector values utilized for node launch
type AMI struct {
	// ID of the AMI
	// +required
	ID string `json:"id"`
	// Name of the AMI
	// +optional
	Name string `json:"name,omitempty"`
	// Requirements of the AMI to be utilized on an instance type
	// +required
	Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
}

// ECSNodeClassStatus contains the resolved state of the ECSNodeClass
type ECSNodeClassStatus struct {
	// Subnets contains the current Subnet values that are available to the
	// cluster under the subnet selectors.
	// +optional
	Subnets []Subnet `json:"subnets,omitempty"`
	// SecurityGroups contains the current Security Groups values that are available to the
	// cluster under the SecurityGroups selectors.
	// +optional
	SecurityGroups []SecurityGroup `json:"securityGroups,omitempty"`
	// AMI contains the current AMI values that are available to the
	// cluster under the AMI selectors.
	// +optional
	AMIs []AMI `json:"amis,omitempty"`
	// InstanceProfile contains the resolved instance profile for the role
	// +optional
	InstanceProfile string `json:"instanceProfile,omitempty"`
	// Conditions contains signals for health and readiness
	// +optional
	Conditions []status.Condition `json:"conditions,omitempty"`
}

func (in *ECSNodeClass) StatusConditions() status.ConditionSet {
	return status.NewReadyConditions(
		ConditionTypeAMIsReady,
		ConditionTypeSubnetsReady,
		ConditionTypeSecurityGroupsReady,
		ConditionTypeInstanceProfileReady,
	).For(in)
}

func (in *ECSNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

func (in *ECSNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}
