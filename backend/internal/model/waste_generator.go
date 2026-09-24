package model

import "time"

// WasteGenerator models 产废单位 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type WasteGenerator struct {
	BaseModel
	PermitNumber    string    `json:"permitNumber" gorm:"size:80;uniqueIndex;not null"`
	PermitExpiresAt time.Time `json:"permitExpiresAt" gorm:"index;not null"`
	WasteCategories string    `json:"wasteCategories" gorm:"size:500;not null"`
	Facility        string    `json:"facility" gorm:"size:120;index"`
	Owner           string    `json:"owner" gorm:"size:120;index"`
	Category        string    `json:"category" gorm:"size:80;index"`
	RiskLevel       string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt     time.Time `json:"effectiveAt"`
	Evidence        string    `json:"evidence" gorm:"size:2000"`
	RelatedCode     string    `json:"relatedCode" gorm:"size:64;index"`
	// RegisteredDestinations holds the optional 备案去向 list. Matching is done
	// against the current rows, so registering destinations is deliberately
	// optional, but once a generator registers one every manifest destination
	// must match an active row.
	RegisteredDestinations []GeneratorDestination `json:"registeredDestinations,omitempty" gorm:"foreignKey:GeneratorID;constraint:OnDelete:CASCADE"`
}

func (item *WasteGenerator) GetBase() *BaseModel { return &item.BaseModel }

func (item WasteGenerator) TableName() string { return "waste_generators" }

var WasteGeneratorInitialStatus = "active"
