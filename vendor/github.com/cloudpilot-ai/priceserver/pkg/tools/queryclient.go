package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
)

type QueryClientInterface interface {
	// Run is used to sync the data periodically
	Run(ctx context.Context)
	// Sync is used to sync the data manually if want to work with your own scheduling mechanism
	Sync() error
	// ListRegions returns a list of the supported regions
	ListRegions() []string
	// ListInstancesDetails returns the details of the supported instances in one region
	ListInstancesDetails(region string) *apis.RegionalInstancePrice
	// GetInstanceDetails returns the details of the specified instance
	GetInstanceDetails(region, instanceType string) *apis.InstanceTypePrice
}

type QueryClientImpl struct {
	region       string
	queryBaseUrl string

	awsMutex  sync.Mutex
	priceData map[string]*apis.RegionalInstancePrice
}

const (
	AlibabaCloudProvider = "alibabacloud"
	AWSCloudProvider     = "aws"
)

func NewQueryClient(endpoint, cloudProvider, region string) (QueryClientInterface, error) {
	var (
		err          error
		queryBaseUrl string
	)
	switch cloudProvider {
	case AWSCloudProvider:
		queryBaseUrl, err = url.JoinPath(endpoint, "/api/v1/aws/ec2")
		if err != nil {
			return nil, err
		}
	case AlibabaCloudProvider:
		queryBaseUrl, err = url.JoinPath(endpoint, "/api/v1/alibabacloud/ecs")
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", cloudProvider)
	}

	ret := &QueryClientImpl{
		region:       region,
		queryBaseUrl: queryBaseUrl,
		priceData:    map[string]*apis.RegionalInstancePrice{},
	}
	if err := ret.Sync(); err != nil {
		return nil, err
	}

	return ret, nil
}

func (q *QueryClientImpl) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute * 30)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = q.Sync()
		}
	}
}

func (q *QueryClientImpl) refreshSpecificInstanceTypeData(region, instanceType string) *apis.InstanceTypePrice {
	url := fmt.Sprintf("%s/regions/%s/types/%s/price", q.queryBaseUrl, region, instanceType)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		klog.Errorf("Failed to create request: %v", err)
		return nil
	}
	req.Header.Add("Accept", "Accept-Encoding: gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		klog.Errorf("Failed to get price data: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("Failed to get price data: %s", resp.Status)
		return nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("Failed to read price data: %v", err)
		return nil
	}

	var price apis.InstanceTypePrice
	err = json.Unmarshal(data, &price)
	if err != nil {
		klog.Errorf("Failed to unmarshal price data: %v", err)
		return nil
	}
	q.priceData[region].InstanceTypePrices[instanceType] = &price

	return &price
}

func (q *QueryClientImpl) Sync() error {
	url := fmt.Sprintf("%s/price", q.queryBaseUrl)
	if q.region != "" {
		url = fmt.Sprintf("%s/regions/%s/price", q.queryBaseUrl, q.region)
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		klog.Errorf("Failed to create request: %v", err)
		return err
	}
	// Use gzip to compress the response
	req.Header.Add("Accept", "Accept-Encoding: gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		klog.Errorf("Failed to get price data: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("Failed to get price data: %s", resp.Status)
		return err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("Failed to read price data: %v", err)
		return err
	}

	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()
	err = json.Unmarshal(data, &q.priceData)
	if err != nil {
		klog.Errorf("Failed to unmarshal price data: %v", err)
		return err
	}

	return nil
}

func (q *QueryClientImpl) ListRegions() []string {
	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()

	var ret []string
	for region := range q.priceData {
		ret = append(ret, region)
	}
	return ret
}

func (q *QueryClientImpl) ListInstancesDetails(region string) *apis.RegionalInstancePrice {
	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()

	if _, ok := q.priceData[region]; !ok {
		return nil
	}
	return q.priceData[region].DeepCopy()
}

func (q *QueryClientImpl) GetInstanceDetails(region, instanceType string) *apis.InstanceTypePrice {
	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()

	if _, ok := q.priceData[region]; !ok {
		return nil
	}
	ret, ok := q.priceData[region].InstanceTypePrices[instanceType]
	if !ok {
		return q.refreshSpecificInstanceTypeData(region, instanceType)
	}
	return ret
}
