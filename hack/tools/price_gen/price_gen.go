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
