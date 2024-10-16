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
	"log"
	"strings"

	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ECSNodeClassSpec is the top level specification for the AliCloud Karpenter Provider.
// This will contain configuration necessary to launch instances in AliCloud.
type ECSNodeClassSpec struct {
	// VSwitchSelectorTerms is a list of or vSwitch selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="vSwitchSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id']",rule="self.all(x, has(x.tags) || has(x.id))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in vSwitchSelectorTerms",rule="!self.all(x, has(x.id) && has(x.tags))"
	// +kubebuilder:validation:MaxItems:=30
	// +required
	VSwitchSelectorTerms []VSwitchSelectorTerm `json:"vSwitchSelectorTerms" hash:"ignore"`
	// SecurityGroupSelectorTerms is a list of or security group selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="securityGroupSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id', 'name']",rule="self.all(x, has(x.tags) || has(x.id) || has(x.name))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.id) && (has(x.tags) || has(x.name)))"
	// +kubebuilder:validation:XValidation:message="'name' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.name) && (has(x.tags) || has(x.id)))"
	// +kubebuilder:validation:MaxItems:=30
	// +required
	SecurityGroupSelectorTerms []SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms" hash:"ignore"`
	// ImageSelectorTerms is a list of or image selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id', 'name', 'alias']",rule="self.all(x, has(x.tags) || has(x.id) || has(x.name) || has(x.alias))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in imageSelectorTerms",rule="!self.exists(x, has(x.id) && (has(x.alias) || has(x.tags) || has(x.name) || has(x.owner)))"
	// +kubebuilder:validation:XValidation:message="'alias' is mutually exclusive, cannot be set with a combination of other fields in imageSelectorTerms",rule="!self.exists(x, has(x.alias) && (has(x.id) || has(x.tags) || has(x.name) || has(x.owner)))"
	// +kubebuilder:validation:XValidation:message="'alias' is mutually exclusive, cannot be set with a combination of other imageSelectorTerms",rule="!(self.exists(x, has(x.alias)) && self.size() != 1)"
	// +kubebuilder:validation:MinItems:=1
	// +kubebuilder:validation:MaxItems:=30
	// +required
	ImageSelectorTerms []ImageSelectorTerm `json:"imageSelectorTerms" hash:"ignore"`
	// UserData to be applied to the provisioned nodes.
	// It must be in the appropriate format based on the ImageFamily in use. Karpenter will merge certain fields into
	// this UserData to ensure nodes are being provisioned with the correct configuration.
	// +optional
	UserData *string `json:"userData,omitempty"`
	// KubeletConfiguration defines args to be used when configuring kubelet on provisioned nodes.
	// They are a subset of the upstream types, recognizing not all options may be supported.
	// Wherever possible, the types and names should reflect the upstream kubelet types.
	// +kubebuilder:validation:XValidation:message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent",rule="has(self.imageGCHighThresholdPercent) && has(self.imageGCLowThresholdPercent) ?  self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent  : true"
	// +kubebuilder:validation:XValidation:message="evictionSoft OwnerKey does not have a matching evictionSoftGracePeriod",rule="has(self.evictionSoft) ? self.evictionSoft.all(e, (e in self.evictionSoftGracePeriod)):true"
	// +kubebuilder:validation:XValidation:message="evictionSoftGracePeriod OwnerKey does not have a matching evictionSoft",rule="has(self.evictionSoftGracePeriod) ? self.evictionSoftGracePeriod.all(e, (e in self.evictionSoft)):true"
	// +optional
	KubeletConfiguration *KubeletConfiguration `json:"kubeletConfiguration,omitempty"`
	// SystemDisk to be applied to provisioned nodes.
	// +optional
	SystemDisk *SystemDisk `json:"systemDisk,omitempty"`
}

// VSwitchSelectorTerm defines selection logic for a vSwitch used by Karpenter to launch nodes.
type VSwitchSelectorTerm struct {
	// Tags is a map of key/value tags used to select vSwitches
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ID is the vSwitch id in ECS
	// +kubebuilder:validation:Pattern="vsw-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`
}

// SecurityGroupSelectorTerm defines selection logic for a security group used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type SecurityGroupSelectorTerm struct {
	// Tags is a map of key/value tags used to select subnets
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ID is the security group id in ECS
	// +kubebuilder:validation:Pattern:="sg-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`
	// Name is the security group name in ECS.
	// This value is the name field, which is different from the name tag.
	Name string `json:"name,omitempty"`
}

// ImageSelectorTerm defines selection logic for an image used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type ImageSelectorTerm struct {
	// Alias specifies which ACK image to select.
	// Each alias consists of a family and an image version, specified as "family@version".
	// Valid families include: aliyun3.
	// Currently only supports version pinning to the latest image release, with that images version format (ex: "aliyun3@latest").
	// Setting the version to latest will result in drift when a new Image is released. This is **not** recommended for production environments.
	// +kubebuilder:validation:XValidation:message="'alias' is improperly formatted, must match the format 'family@version'",rule="self.matches('^[a-zA-Z0-9]*@.*$')"
	// +kubebuilder:validation:XValidation:message="family is not supported, must be one of the following: 'aliyun3'",rule="self.find('^[^@]+') in ['aliyun3']"
	// +kubebuilder:validation:MaxLength=30
	// +optional
	Alias string `json:"alias,omitempty"`
	// Tags is a map of key/value tags used to select subsets
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ID is the image id in ECS
	// +optional
	ID string `json:"id,omitempty"`
	// Name is the image name in ECS.
	// This value is the name field, which is different from the name tag.
	// +optional
	Name string `json:"name,omitempty"`
	// Owner is the image source.
	// Default is system | self | public. If specified, only one of the following: "self", "system", "share", "public", and "marketplace"
	// +optional
	Owner string `json:"owner,omitempty"`
}

// KubeletConfiguration defines args to be used when configuring kubelet on provisioned nodes.
// They are a subset of the upstream types, recognizing not all options may be supported.
// Wherever possible, the types and names should reflect the upstream kubelet types.
// https://pkg.go.dev/k8s.io/kubelet/config/v1beta1#KubeletConfiguration
// https://github.com/kubernetes/kubernetes/blob/9f82d81e55cafdedab619ea25cabf5d42736dacf/cmd/kubelet/app/options/options.go#L53
type KubeletConfiguration struct {
	// clusterDNS is a list of IP addresses for the cluster DNS server.
	// Note that not all providers may use all addresses.
	//+optional
	ClusterDNS []string `json:"clusterDNS,omitempty"`
	// MaxPods is an override for the maximum number of pods that can run on
	// a worker node instance.
	// +kubebuilder:validation:Minimum:=0
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`
	// PodsPerCore is an override for the number of pods that can run on a worker node
	// instance based on the number of cpu cores. This value cannot exceed MaxPods, so, if
	// MaxPods is a lower value, that value will be used.
	// +kubebuilder:validation:Minimum:=0
	// +optional
	PodsPerCore *int32 `json:"podsPerCore,omitempty"`
	// SystemReserved contains resources reserved for OS system daemons and kernel memory.
	// +kubebuilder:validation:XValidation:message="valid keys for systemReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="systemReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +optional
	SystemReserved map[string]string `json:"systemReserved,omitempty"`
	// KubeReserved contains resources reserved for Kubernetes system components.
	// +kubebuilder:validation:XValidation:message="valid keys for kubeReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="kubeReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +optional
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`
	// EvictionHard is the map of signal names to quantities that define hard eviction thresholds
	// +kubebuilder:validation:XValidation:message="valid keys for evictionHard are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionHard map[string]string `json:"evictionHard,omitempty"`
	// EvictionSoft is the map of signal names to quantities that define soft eviction thresholds
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoft are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`
	// EvictionSoftGracePeriod is the map of signal names to quantities that define grace periods for each eviction signal
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoftGracePeriod are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionSoftGracePeriod map[string]metav1.Duration `json:"evictionSoftGracePeriod,omitempty"`
	// EvictionMaxPodGracePeriod is the maximum allowed grace period (in seconds) to use when terminating pods in
	// response to soft eviction thresholds being met.
	// +optional
	EvictionMaxPodGracePeriod *int32 `json:"evictionMaxPodGracePeriod,omitempty"`
	// ImageGCHighThresholdPercent is the percent of disk usage after which image
	// garbage collection is always run. The percent is calculated by dividing this
	// field value by 100, so this field must be between 0 and 100, inclusive.
	// When specified, the value must be greater than ImageGCLowThresholdPercent.
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`
	// ImageGCLowThresholdPercent is the percent of disk usage before which image
	// garbage collection is never run. Lowest disk usage to garbage collect to.
	// The percent is calculated by dividing this field value by 100,
	// so the field value must be between 0 and 100, inclusive.
	// When specified, the value must be less than imageGCHighThresholdPercent
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`
	// CPUCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits.
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`
}

type SystemDisk struct {
	// The category of the system disk (for example, cloud or cloud_ssd).
	// Only one of the following: "cloud", "cloud_efficiency", "cloud_ssd", "cloud_essd", "cloud_auto", and "cloud_essd_entry"
	// +kubebuilder:validation:Enum:={cloud,cloud_efficiency,cloud_ssd,cloud_essd,cloud_auto,cloud_essd_entry}
	// +optional
	Category *string `json:"category,omitempty"`
	// The size of the system disk. Unit: GiB.
	// Valid values:
	//   * If you set Category to cloud: 20 to 500.
	//   * If you set Category to other disk categories: 20 to 2048.
	//
	// +kubebuilder:validation:XValidation:message="size invalid",rule="self >= 20"
	// +optional
	Size *int32 `json:"size,omitempty"`
	// The name of the system disk.
	// The name must be 2 to 128 characters in length.
	// The name must start with a letter and cannot start with http:// or https://.
	// The name can contain letters, digits, colons (:), underscores (_), and hyphens (-).
	// +kubebuilder:validation:XValidation:message="format invalid",rule="!self.startsWith('http') && self.size() >= 2 && self.size() <= 128"
	// +kubebuilder:validation:Pattern="^[A-Za-z][A-Za-z0-9:_-]*$"
	// +optional
	DiskName *string `json:"diskName,omitempty"`
	// The performance level of the ESSD to use as the system disk. Default value: PL0.
	// Valid values:
	//   * PL0: A single ESSD can deliver up to 10,000 random read/write IOPS.
	//   * PL1: A single ESSD can deliver up to 50,000 random read/write IOPS.
	//   * PL2: A single ESSD can deliver up to 100,000 random read/write IOPS.
	//   * PL3: A single ESSD can deliver up to 1,000,000 random read/write IOPS.
	// +kubebuilder:validation:Enum:={PL0,PL1,PL2,PL3}
	PerformanceLevel *string `json:"performanceLevel,omitempty"`
	// The ID of the automatic snapshot policy to apply to the system disk.
	AutoSnapshotPolicyID *string `json:"autoSnapshotPolicyId,omitempty"`
	// Specifies whether to enable the performance burst feature for the system disk
	BurstingEnabled *bool `json:"burstingEnabled,omitempty"`
	// TODO: add ProvisionedIops or Iops
}

// ECSNodeClass is the Schema for the ECSNodeClass API
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=ecsnodeclasses,scope=Cluster,categories=karpenter,shortName={ecsnc,ecsncs}
// +kubebuilder:subresource:status
type ECSNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ECSNodeClassSpec   `json:"spec,omitempty"`
	Status ECSNodeClassStatus `json:"status,omitempty"`
}

// ImageFamily If an alias is specified, return alias, or be 'Custom' (enforced via validation).
func (in *ECSNodeClass) ImageFamily() string {
	if term, ok := lo.Find(in.Spec.ImageSelectorTerms, func(t ImageSelectorTerm) bool {
		return t.Alias != ""
	}); ok {
		return ImageFamilyFromAlias(term.Alias)
	}
	// Unreachable: validation enforces that one of the above conditions must be met
	return ImageFamilyCustom
}

// ECSNodeClassList contains a list of ECSNodeClass
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ECSNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ECSNodeClass `json:"items"`
}

func ImageFamilyFromAlias(alias string) string {
	components := strings.Split(alias, "@")
	if len(components) != 2 {
		log.Fatalf("failed to parse image alias %q, invalid format", alias)
	}
	family, ok := lo.Find([]string{
		ImageFamilyAliyun3,
	}, func(family string) bool {
		return strings.ToLower(family) == components[0]
	})
	if !ok {
		log.Fatalf("%q is an invalid alias family", components[0])
	}
	return family
}

func ImageVersionFromAlias(alias string) string {
	components := strings.Split(alias, "@")
	if len(components) != 2 {
		log.Fatalf("failed to parse image alias %q, invalid format", alias)
	}
	return components[1]
}
