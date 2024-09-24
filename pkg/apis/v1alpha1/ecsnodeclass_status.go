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

// VSwitch contains resolved VSwitch selector values utilized for node launch
type VSwitch struct {
	// ID of the vSwitch
	// +required
	ID string `json:"id"`
	// The associated availability zone ID
	// +required
	ZoneID string `json:"zoneID,omitempty"`
}

// ECSNodeClassStatus contains the resolved state of the ECSNodeClass
type ECSNodeClassStatus struct {
	// VSwitches contains the current VSwitch values that are available to the
	// cluster under the vSwitch selectors.
	// +optional
	VSwitches []VSwitch `json:"vSwitches,omitempty"`
}
