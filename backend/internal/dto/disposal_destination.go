package dto

// DisposalDestinationInput is one filed 备案去向 entry on a 产废单位. The full list
// is replaced on every create/update; entries no longer present are revoked.
// Status is owned by the service so callers cannot self-restore a filing.
type DisposalDestinationInput struct {
	FacilityName  string `json:"facilityName" binding:"required,min=2,max=200"`
	LicenseNumber string `json:"licenseNumber" binding:"required,min=3,max=80"`
}
