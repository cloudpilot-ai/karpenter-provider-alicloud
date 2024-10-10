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

package main

import (
	"encoding/json"
	"os"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

func main() {
	queryClient, err := tools.NewQueryClient("https://pre-price.cloudpilot.ai", tools.AlibabaCloudProvider, "")
	if err != nil {
		panic(err)
	}

	regions := queryClient.ListRegions()
	regionalPrice := map[string]*apis.RegionalInstancePrice{}
	for _, region := range regions {
		price := queryClient.ListInstancesDetails(region)
		regionalPrice[region] = price
	}

	allPrice := map[string]map[string]float64{}
	for region, prices := range regionalPrice {
		allPrice[region] = map[string]float64{}
		for instanceType, priceInfo := range prices.InstanceTypePrices {
			allPrice[region][instanceType] = priceInfo.OnDemandPricePerHour
		}
	}

	// pretty print JSON, referring to https://stackoverflow.com/questions/19038598/how-can-i-pretty-print-json-using-go
	data, err := json.MarshalIndent(allPrice, "", "    ")
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile("pkg/providers/pricing/initial-on-demand-prices.json", data, 0644); err != nil {
		panic(err)
	}
}
