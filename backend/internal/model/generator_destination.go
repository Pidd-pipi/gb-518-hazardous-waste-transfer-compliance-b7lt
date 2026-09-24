package model

import (
	"time"

	"gorm.io/gorm"
)

// GeneratorDestination models one 备案去向 of a 产废单位. Each record names an
// externally licensed disposal/facility plant (处理厂名称) together with its
// hazardous-waste operating licence number (经营许可证号). A generator may
// register several destinations; "revoked" entries are retained as audit
// evidence but can no longer match transfer-manifest destinations.
type GeneratorDestination struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	GeneratorID  uint           `json:"generatorId" gorm:"index;not null"`
	FacilityName string         `json:"facilityName" gorm:"size:200;not null"`
	LicenseNo    string         `json:"licenseNo" gorm:"size:80;not null"`
	Status       string         `json:"status" gorm:"size:24;not null;default:active"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// Destination statuses mirror 备案状态: active 备案有效, revoked 已撤销.
var (
	GeneratorDestinationStatusActive  = "active"
	GeneratorDestinationStatusRevoked = "revoked"
)

func (item GeneratorDestination) TableName() string { return "generator_destinations" }
