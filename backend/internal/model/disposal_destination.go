package model

import (
	"strings"
	"time"
)

// NormalizeDestinationName canonicalizes facility names for reconciliation so
// that incidental whitespace and casing differences do not create duplicate
// filings. Manifests must match the generator's current filing item-by-item.
func NormalizeDestinationName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), ""))
}

// DisposalDestination models one 备案去向 belonging to a 产废单位. A generator may
// file multiple destinations; each carries the disposal facility name and its
// hazardous-waste operating license number. Rows are retained when a filing is
// withdrawn and flipped to status "revoked" so historical manifests can still be
// reconciled against the concrete location and license.
type DisposalDestination struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	GeneratorID   uint      `json:"generatorId" gorm:"uniqueIndex:idx_destination_identity,priority:1;index;not null"`
	FacilityName  string    `json:"facilityName" gorm:"size:200;uniqueIndex:idx_destination_identity,priority:2;not null"`
	LicenseNumber string    `json:"licenseNumber" gorm:"size:80;not null"`
	Status        string    `json:"status" gorm:"size:32;index;not null;default:active"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (item DisposalDestination) TableName() string { return "disposal_destinations" }

const (
	DestinationStatusActive  = "active"
	DestinationStatusRevoked = "revoked"
)
