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
	ConditionTypeVSwitchesReady      = "VSwitchesReady"
	ConditionTypeSecurityGroupsReady = "SecurityGroupsReady"
	ConditionTypeInstanceRAMReady    = "InstanceRAMReady"
)

// VSwitch contains resolved VSwitch selector values utilized for node launch
type VSwitch struct {
	// ID of the vSwitch
	// +required
	ID string `json:"id"`
	// The associated availability zone ID
	// +required
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

// Image contains resolved image selector values utilized for node launch
type Image struct {
	// ID of the Image
	// +required
	ID string `json:"id"`
	// Name of the Image
	// +optional
	Name string `json:"name,omitempty"`
	// Requirements of the Image to be utilized on an instance type
	// +required
	Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
}

// ECSNodeClassStatus contains the resolved state of the ECSNodeClass
type ECSNodeClassStatus struct {
	// VSwitches contains the current VSwitch values that are available to the
	// cluster under the vSwitch selectors.
	// +optional
	VSwitches []VSwitch `json:"vSwitches,omitempty"`
	// SecurityGroups contains the current Security Groups values that are available to the
	// cluster under the SecurityGroups selectors.
	// +optional
	SecurityGroups []SecurityGroup `json:"securityGroups,omitempty"`
	// Image contains the current image that are available to the
	// cluster under the Image selectors.
	// +optional
	Images []Image `json:"images,omitempty"`
	// Conditions contains signals for health and readiness
	// +optional
	Conditions []status.Condition `json:"conditions,omitempty"`
}

func (in *ECSNodeClass) StatusConditions() status.ConditionSet {
	return status.NewReadyConditions(
		ConditionTypeVSwitchesReady,
		ConditionTypeSecurityGroupsReady,
		ConditionTypeInstanceRAMReady,
	).For(in)
}

func (in *ECSNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

func (in *ECSNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}
