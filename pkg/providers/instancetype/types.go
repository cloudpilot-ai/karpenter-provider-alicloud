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

package instancetype

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/operator/options"
)

var (
	instanceTypeScheme = regexp.MustCompile(`^ecs\.([a-z]+)(\-[0-9]+tb)?([0-9]+).*`)
)

const (
	MemoryAvailable = "memory.available"
	NodeFSAvailable = "nodefs.available"

	GiBBytesRatio = 1024 * 1024 * 1024
)

type ZoneData struct {
	ID        string
	Available bool
}

func NewInstanceType(ctx context.Context, info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, kc *v1alpha1.KubeletConfiguration, region string, offerings cloudprovider.Offerings) *cloudprovider.InstanceType {

	it := &cloudprovider.InstanceType{
		Name:         *info.InstanceTypeId,
		Requirements: computeRequirements(info, offerings, region),
		Offerings:    offerings,
		Capacity:     computeCapacity(ctx, info, kc.MaxPods, kc.PodsPerCore),
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved:      kubeReservedResources(cpu(info), pods(ctx, info, kc.MaxPods, kc.PodsPerCore), kc.KubeReserved),
			SystemReserved:    systemReservedResources(kc.SystemReserved),
			EvictionThreshold: evictionThreshold(memory(ctx, info), ephemeralStorage(info), kc.EvictionHard, kc.EvictionSoft),
		},
	}
	if it.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Windows)))) == nil {
		it.Capacity[v1alpha1.ResourcePrivateIPv4Address] = *privateIPv4Address(info)
	}
	return it
}

func extractECSArch(unFormatedArch string) string {
	switch unFormatedArch {
	case "X86":
		return "amd64"
	case "ARM":
		return "arm64"
	default:
		return "amd64"
	}
}

//nolint:gocyclo
func computeRequirements(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, offerings cloudprovider.Offerings, region string) scheduling.Requirements {
	requirements := scheduling.NewRequirements(
		// Well Known Upstream
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, *info.InstanceTypeId),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, extractECSArch(*info.CpuArchitecture)),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Linux)),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(corev1.LabelTopologyZone).Any()
		})...),
		scheduling.NewRequirement(corev1.LabelTopologyRegion, corev1.NodeSelectorOpIn, region),
		scheduling.NewRequirement(corev1.LabelWindowsBuild, corev1.NodeSelectorOpDoesNotExist),
		// Well Known to Karpenter
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
		})...),
		// Well Known to AlibabaCloud
		scheduling.NewRequirement(v1alpha1.LabelInstanceCPU, corev1.NodeSelectorOpIn, fmt.Sprint(*info.CpuCoreCount)),
		scheduling.NewRequirement(v1alpha1.LabelInstanceCPUManufacturer, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceMemory, corev1.NodeSelectorOpIn, fmt.Sprint(*info.MemorySize)),
		scheduling.NewRequirement(v1alpha1.LabelInstanceEBSBandwidth, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceNetworkBandwidth, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceCategory, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceFamily, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGeneration, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceLocalNVME, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceSize, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUName, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUManufacturer, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUCount, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUMemory, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceAcceleratorName, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceAcceleratorManufacturer, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceAcceleratorCount, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceEncryptionInTransitSupported, corev1.NodeSelectorOpIn, fmt.Sprint(info.NetworkEncryptionSupport)),
	)
	// Only add zone-id label when available in offerings. It may not be available if a user has upgraded from a
	// previous version of Karpenter w/o zone-id support and the nodeclass vswitch status has not yet updated.
	if zoneIDs := lo.FilterMap(offerings.Available(), func(o cloudprovider.Offering, _ int) (string, bool) {
		zoneID := o.Requirements.Get(v1alpha1.LabelTopologyZoneID).Any()
		return zoneID, zoneID != ""
	}); len(zoneIDs) != 0 {
		requirements.Add(scheduling.NewRequirement(v1alpha1.LabelTopologyZoneID, corev1.NodeSelectorOpIn, zoneIDs...))
	}

	// Instance Type Labels
	instanceFamilyParts := instanceTypeScheme.FindStringSubmatch(*info.InstanceTypeId)
	if len(instanceFamilyParts) == 4 {
		requirements[v1alpha1.LabelInstanceCategory].Insert(instanceFamilyParts[1])
		requirements[v1alpha1.LabelInstanceGeneration].Insert(instanceFamilyParts[3])
	}
	instanceTypeParts := strings.Split(*info.InstanceTypeId, ".")
	if len(instanceTypeParts) == 2 {
		requirements.Get(v1alpha1.LabelInstanceFamily).Insert(instanceTypeParts[1])
		requirements.Get(v1alpha1.LabelInstanceSize).Insert(instanceTypeParts[2])
	}

	if info.NvmeSupport != nil && *info.NvmeSupport != "unsupported" {
		requirements[v1alpha1.LabelInstanceLocalNVME].Insert(fmt.Sprint(info.LocalStorageCapacity))
	}

	// Network bandwidth
	requirements[v1alpha1.LabelInstanceNetworkBandwidth].Insert(fmt.Sprint(getInstanceBandwidth(info)))

	// GPU Labels
	if info.GPUAmount != nil && *info.GPUAmount != 0 {
		requirements.Get(v1alpha1.LabelInstanceGPUName).Insert(lowerKabobCase(*info.GPUSpec))
		requirements.Get(v1alpha1.LabelInstanceGPUManufacturer).Insert(getGPUManufacturer(*info.GPUSpec))
		requirements.Get(v1alpha1.LabelInstanceGPUCount).Insert(fmt.Sprint(*info.GPUAmount))
		requirements.Get(v1alpha1.LabelInstanceGPUMemory).Insert(fmt.Sprint(info.GPUMemorySize))
	}

	// CPU Manufacturer, valid options: intel, amd
	if info.PhysicalProcessorModel != nil {
		requirements.Get(v1alpha1.LabelInstanceCPUManufacturer).Insert(getCPUManufacturer(*info.PhysicalProcessorModel))
	}

	return requirements
}

func computeCapacity(ctx context.Context, info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, maxPods *int32, podsPerCore *int32) corev1.ResourceList {

	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:              *cpu(info),
		corev1.ResourceMemory:           *memory(ctx, info),
		corev1.ResourceEphemeralStorage: *ephemeralStorage(info),
		corev1.ResourcePods:             *pods(ctx, info, maxPods, podsPerCore),
		v1alpha1.ResourceNVIDIAGPU:      *nvidiaGPUs(info),
		v1alpha1.ResourceAMDGPU:         *amdGPUs(info),
	}
	return resourceList
}

func kubeReservedResources(cpus, pods *resource.Quantity, kubeReserved map[string]string) corev1.ResourceList {
	resources := corev1.ResourceList{
		corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", (11*pods.Value())+255)),
		corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"), // default kube-reserved ephemeral-storage
	}
	// kube-reserved Computed from
	// https://github.com/bottlerocket-os/bottlerocket/pull/1388/files#diff-bba9e4e3e46203be2b12f22e0d654ebd270f0b478dd34f40c31d7aa695620f2fR611
	for _, cpuRange := range []struct {
		start      int64
		end        int64
		percentage float64
	}{
		{start: 0, end: 1000, percentage: 0.06},
		{start: 1000, end: 2000, percentage: 0.01},
		{start: 2000, end: 4000, percentage: 0.005},
		{start: 4000, end: 1 << 31, percentage: 0.0025},
	} {
		if cpu := cpus.MilliValue(); cpu >= cpuRange.start {
			r := float64(cpuRange.end - cpuRange.start)
			if cpu < cpuRange.end {
				r = float64(cpu - cpuRange.start)
			}
			cpuOverhead := resources.Cpu()
			cpuOverhead.Add(*resource.NewMilliQuantity(int64(r*cpuRange.percentage), resource.DecimalSI))
			resources[corev1.ResourceCPU] = *cpuOverhead
		}
	}
	return lo.Assign(resources, lo.MapEntries(kubeReserved, func(k string, v string) (corev1.ResourceName, resource.Quantity) {
		return corev1.ResourceName(k), resource.MustParse(v)
	}))
}

func systemReservedResources(systemReserved map[string]string) corev1.ResourceList {
	return lo.MapEntries(systemReserved, func(k string, v string) (corev1.ResourceName, resource.Quantity) {
		return corev1.ResourceName(k), resource.MustParse(v)
	})
}

func evictionThreshold(memory *resource.Quantity, storage *resource.Quantity, evictionHard map[string]string, evictionSoft map[string]string) corev1.ResourceList {
	overhead := corev1.ResourceList{
		corev1.ResourceMemory:           resource.MustParse("100Mi"),
		corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprint(math.Ceil(float64(storage.Value()) / 100 * 10))),
	}

	override := corev1.ResourceList{}
	var evictionSignals []map[string]string
	if evictionHard != nil {
		evictionSignals = append(evictionSignals, evictionHard)
	}
	if evictionSoft != nil {
		evictionSignals = append(evictionSignals, evictionSoft)
	}

	for _, m := range evictionSignals {
		temp := corev1.ResourceList{}
		if v, ok := m[MemoryAvailable]; ok {
			temp[corev1.ResourceMemory] = computeEvictionSignal(*memory, v)
		}
		if v, ok := m[NodeFSAvailable]; ok {
			temp[corev1.ResourceEphemeralStorage] = computeEvictionSignal(*storage, v)
		}
		override = resources.MaxResources(override, temp)
	}
	// Assign merges maps from left to right so overrides will always be taken last
	return lo.Assign(overhead, override)
}

// computeEvictionSignal computes the resource quantity value for an eviction signal value, computed off the
// base capacity value if the signal value is a percentage or as a resource quantity if the signal value isn't a percentage
func computeEvictionSignal(capacity resource.Quantity, signalValue string) resource.Quantity {
	if strings.HasSuffix(signalValue, "%") {
		p := mustParsePercentage(signalValue)

		// Calculation is node.capacity * signalValue if percentage
		// From https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/#eviction-signals
		return resource.MustParse(fmt.Sprint(math.Ceil(capacity.AsApproximateFloat64() / 100 * p)))
	}
	return resource.MustParse(signalValue)
}

func mustParsePercentage(v string) float64 {
	p, err := strconv.ParseFloat(strings.Trim(v, "%"), 64)
	if err != nil {
		panic(fmt.Sprintf("expected percentage value to be a float but got %s, %v", v, err))
	}
	// Setting percentage value to 100% is considered disabling the threshold according to
	// https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/
	if p == 100 {
		p = 0
	}
	return p
}

func cpu(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	return resources.Quantity(fmt.Sprint(*info.CpuCoreCount))
}

func memory(ctx context.Context, info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	sizeInGib := tea.Float32Value(info.MemorySize)
	mem := resources.Quantity(fmt.Sprintf("%fGi", sizeInGib))
	if mem.IsZero() {
		return mem
	}
	// Account for VM overhead in calculation
	mem.Sub(resource.MustParse(fmt.Sprintf("%dGi", int64(math.Ceil(float64(mem.Value())*options.FromContext(ctx).VMMemoryOverheadPercent/GiBBytesRatio)))))
	return mem
}

func pods(_ context.Context, info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, maxPods *int32, podsPerCore *int32) *resource.Quantity {
	var count int64
	switch {
	case maxPods != nil:
		count = int64(lo.FromPtr(maxPods))
	default:
		count = 110

	}
	if lo.FromPtr(podsPerCore) > 0 {
		count = lo.Min([]int64{int64(lo.FromPtr(podsPerCore) * lo.FromPtr(info.CpuCoreCount)), count})
	}
	return resources.Quantity(fmt.Sprint(count))
}

func nvidiaGPUs(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	if strings.ToLower(getGPUManufacturer(*info.GPUSpec)) == "nvidia" {
		return resources.Quantity(fmt.Sprint(*info.GPUAmount))
	}

	return resources.Quantity("0")
}

func amdGPUs(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	if strings.ToLower(getGPUManufacturer(*info.GPUSpec)) == "amd" {
		return resources.Quantity(fmt.Sprint(*info.GPUAmount))
	}

	return resources.Quantity("0")
}

func lowerKabobCase(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
}

func getGPUManufacturer(gpuName string) string {
	return strings.Split(gpuName, " ")[0]
}

func getCPUManufacturer(cpuName string) string {
	return strings.Split(cpuName, " ")[0]
}

func ephemeralStorage(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	return resources.Quantity(fmt.Sprintf("%dG", tea.Int64Value(info.LocalStorageCapacity)))
}

func privateIPv4Address(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	return resources.Quantity(fmt.Sprint(*info.EniPrivateIpAddressQuantity * (*info.EniQuantity)))
}

func getInstanceBandwidth(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) int64 {
	bandwidthRx := int32(0)
	bandwidthTx := int32(0)
	if info.InstanceBandwidthRx != nil {
		bandwidthRx = *info.InstanceBandwidthRx
	}
	if info.InstanceBandwidthTx != nil {
		bandwidthTx = *info.InstanceBandwidthTx
	}

	if bandwidthRx > bandwidthTx {
		return int64(bandwidthRx)
	}

	return int64(bandwidthTx)
}
