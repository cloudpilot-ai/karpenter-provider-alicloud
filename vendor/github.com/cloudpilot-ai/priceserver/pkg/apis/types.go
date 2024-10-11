package apis

type RegionTypeKey struct {
	Region       string
	InstanceType string
}

type RegionalInstancePrice struct {
	InstanceTypePrices map[string]*InstanceTypePrice `json:"instanceTypePrices"`
}

type InstanceTypePrice struct {
	Arch                 string   `json:"arch"`
	VCPU                 float64  `json:"vcpu"`
	Memory               float64  `json:"memory"`
	GPU                  float64  `json:"gpu"`
	Zones                []string `json:"zones"`
	OnDemandPricePerHour float64  `json:"onDemandPricePerHour"`
	// AWSEC2Billing represents the cost of saving plan billing
	// key is {savings plan type}/{term length}/{payment option}
	AWSEC2Billing map[string]AWSEC2Billing `json:"awsEC2Billing,omitempty"`
	// SpotPricePerHour represents the smallest spot price per hour in different zones
	SpotPricePerHour map[string]float64 `json:"spotPricePerHour,omitempty"`
}

type AWSEC2Billing struct {
	Rate float64 `json:"rate"`
}

type AWSEC2SPPaymentOption string

const (
	AWSEC2SPPaymentOptionAllUpfront     AWSEC2SPPaymentOption = "all"
	AWSEC2SPPaymentOptionPartialUpfront AWSEC2SPPaymentOption = "partial"
	AWSEC2SPPaymentOptionNoUpfront      AWSEC2SPPaymentOption = "no"
)

func (r *RegionalInstancePrice) DeepCopy() *RegionalInstancePrice {
	d := &RegionalInstancePrice{
		InstanceTypePrices: make(map[string]*InstanceTypePrice),
	}
	for k, v := range r.InstanceTypePrices {
		vCopy := v.DeepCopy()
		d.InstanceTypePrices[k] = vCopy
	}
	return d
}

func (i *InstanceTypePrice) DeepCopy() *InstanceTypePrice {
	d := &InstanceTypePrice{
		Arch:                 i.Arch,
		VCPU:                 i.VCPU,
		Memory:               i.Memory,
		GPU:                  i.GPU,
		Zones:                make([]string, len(i.Zones)),
		OnDemandPricePerHour: i.OnDemandPricePerHour,
		AWSEC2Billing:        make(map[string]AWSEC2Billing),
		SpotPricePerHour:     make(map[string]float64),
	}
	copy(d.Zones, i.Zones)
	for k, v := range i.AWSEC2Billing {
		d.AWSEC2Billing[k] = v
	}
	for k, v := range i.SpotPricePerHour {
		d.SpotPricePerHour[k] = v
	}
	return d
}
