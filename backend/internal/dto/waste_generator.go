package dto

import "time"

// GeneratorDestinationInput is one 备案去向 entry maintained together with a
// 产废单位. Multiple entries are allowed; status may be omitted on create
// (defaults to active).
type GeneratorDestinationInput struct {
	FacilityName string `json:"facilityName" binding:"required,min=2,max=200"`
	LicenseNo    string `json:"licenseNo" binding:"required,min=3,max=80"`
	Status       string `json:"status" binding:"omitempty,oneof=active revoked"`
}

// CreateWasteGenerator is the public write contract for 产废单位. Status is deliberately
// omitted so callers cannot bypass the service state machine.
type CreateWasteGenerator struct {
	Code                   string                      `json:"code" binding:"required,min=2,max=64"`
	Name                   string                      `json:"name" binding:"required,min=2,max=160"`
	PermitNumber           string                      `json:"permitNumber" binding:"required,min=3,max=80"`
	PermitExpiresAt        time.Time                   `json:"permitExpiresAt" binding:"required"`
	WasteCategories        string                      `json:"wasteCategories" binding:"required,max=500"`
	Description            string                      `json:"description" binding:"max=1000"`
	Facility               string                      `json:"facility" binding:"required,max=120"`
	Owner                  string                      `json:"owner" binding:"required,max=120"`
	Category               string                      `json:"category" binding:"required,max=80"`
	RiskLevel              string                      `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue            float64                     `json:"metricValue"`
	MetricUnit             string                      `json:"metricUnit" binding:"max=24"`
	EffectiveAt            time.Time                   `json:"effectiveAt" binding:"required"`
	Evidence               string                      `json:"evidence" binding:"max=2000"`
	RelatedCode            string                      `json:"relatedCode" binding:"max=64"`
	RegisteredDestinations []GeneratorDestinationInput `json:"registeredDestinations" binding:"dive"`
}

type UpdateWasteGenerator struct {
	ExpectedVersion        uint                        `json:"expectedVersion" binding:"required"`
	Name                   string                      `json:"name" binding:"required,min=2,max=160"`
	PermitNumber           string                      `json:"permitNumber" binding:"required,min=3,max=80"`
	PermitExpiresAt        time.Time                   `json:"permitExpiresAt" binding:"required"`
	WasteCategories        string                      `json:"wasteCategories" binding:"required,max=500"`
	Description            string                      `json:"description" binding:"max=1000"`
	Facility               string                      `json:"facility" binding:"required,max=120"`
	Owner                  string                      `json:"owner" binding:"required,max=120"`
	Category               string                      `json:"category" binding:"required,max=80"`
	RiskLevel              string                      `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue            float64                     `json:"metricValue"`
	MetricUnit             string                      `json:"metricUnit" binding:"max=24"`
	EffectiveAt            time.Time                   `json:"effectiveAt" binding:"required"`
	Evidence               string                      `json:"evidence" binding:"max=2000"`
	RelatedCode            string                      `json:"relatedCode" binding:"max=64"`
	RegisteredDestinations []GeneratorDestinationInput `json:"registeredDestinations" binding:"dive"`
}
