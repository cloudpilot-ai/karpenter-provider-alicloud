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

package instance

import "time"

// Instance is an internal data representation of either an ecs.Instance or an ecs.FleetInstance
// It contains all the common data that is needed to inject into the Machine from either of these responses
type Instance struct {
	LaunchTime       time.Time
	State            string
	ID               string
	ImageID          string
	Type             string
	Zone             string
	CapacityType     string
	SecurityGroupIDs []string
	SubnetID         string
	Tags             map[string]string
	EFAEnabled       bool
}
