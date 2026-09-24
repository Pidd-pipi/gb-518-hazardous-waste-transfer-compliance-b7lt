package repository

import (
	"context"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"gorm.io/gorm"
)

// WasteGeneratorRepository owns all persistence operations for 产废单位,
// including the 备案去向 child aggregate maintained inside generator writes.
type WasteGeneratorRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.WasteGenerator], error)
	Get(context.Context, uint) (model.WasteGenerator, error)
	FindByCode(context.Context, string) (model.WasteGenerator, error)
	Create(context.Context, *model.WasteGenerator) error
	CreateAudited(context.Context, *model.WasteGenerator, *model.AuditLog) error
	Update(context.Context, uint, uint, *model.WasteGenerator) error
	UpdateAudited(context.Context, uint, uint, *model.WasteGenerator, *model.AuditLog) error
	Delete(context.Context, uint) error
	DeleteAudited(context.Context, uint, *model.AuditLog) error
	CountByStatus(context.Context) (map[string]int64, error)
	// Destinations returns all current 备案去向 rows including revoked ones so
	// validation can report the concrete licence problem for a revoked match.
	Destinations(context.Context, uint) ([]model.GeneratorDestination, error)
}

type wasteGeneratorRepository struct {
	db *gorm.DB
}

func NewWasteGeneratorRepository(db *gorm.DB) WasteGeneratorRepository {
	return &wasteGeneratorRepository{db: db}
}

func destinations(db *gorm.DB) *gorm.DB {
	return db.Preload("RegisteredDestinations", func(tx *gorm.DB) *gorm.DB {
		return tx.Order("id ASC")
	})
}

func (r *wasteGeneratorRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.WasteGenerator], error) {
	page, pageSize := normalizePage(q.Page, q.PageSize)
	db := r.db.WithContext(ctx).Model(&model.WasteGenerator{})
	if search := strings.TrimSpace(strings.ToLower(q.Search)); search != "" {
		wildcard := "%" + search + "%"
		db = db.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", wildcard, wildcard)
	}
	if status := strings.TrimSpace(q.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.WasteGenerator]{}, err
	}
	items := make([]model.WasteGenerator, 0)
	err := destinations(db).Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.WasteGenerator]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *wasteGeneratorRepository) Get(ctx context.Context, id uint) (model.WasteGenerator, error) {
	var item model.WasteGenerator
	err := destinations(r.db.WithContext(ctx)).First(&item, id).Error
	return item, err
}

func (r *wasteGeneratorRepository) FindByCode(ctx context.Context, code string) (model.WasteGenerator, error) {
	var item model.WasteGenerator
	err := r.db.WithContext(ctx).Where("UPPER(code) = ?", strings.ToUpper(strings.TrimSpace(code))).First(&item).Error
	return item, err
}

func (r *wasteGeneratorRepository) Create(ctx context.Context, item *model.WasteGenerator) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *wasteGeneratorRepository) CreateAudited(ctx context.Context, item *model.WasteGenerator, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		audit.EntityID = item.ID
		return tx.Create(audit).Error
	})
}

func (r *wasteGeneratorRepository) Update(ctx context.Context, id, version uint, item *model.WasteGenerator) error {
	return updateRecord(r.db.WithContext(ctx).Omit("RegisteredDestinations"), id, version, item)
}

func (r *wasteGeneratorRepository) UpdateAudited(ctx context.Context, id, expectedVersion uint, item *model.WasteGenerator, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.WasteGenerator{}).
			Where("id = ? AND version = ?", id, expectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "RegisteredDestinations").
			Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if err := reconcileDestinations(tx, id, item.RegisteredDestinations); err != nil {
			return err
		}
		audit.EntityID = id
		return tx.Create(audit).Error
	})
}

// reconcileDestinations replaces the generator's 备案去向 set with the desired
// list while preserving rows (and therefore their identity/audit trail) that
// still match by facility name; vanished rows are soft deleted.
func reconcileDestinations(tx *gorm.DB, generatorID uint, desired []model.GeneratorDestination) error {
	var existing []model.GeneratorDestination
	if err := tx.Where("generator_id = ?", generatorID).Find(&existing).Error; err != nil {
		return err
	}
	kept := make(map[string]bool, len(desired))
	now := time.Now().UTC()
	for _, next := range desired {
		name := strings.TrimSpace(next.FacilityName)
		var matched *model.GeneratorDestination
		for i := range existing {
			if strings.TrimSpace(existing[i].FacilityName) == name {
				matched = &existing[i]
				break
			}
		}
		if matched == nil {
			row := model.GeneratorDestination{
				GeneratorID: generatorID, FacilityName: name,
				LicenseNo: strings.TrimSpace(next.LicenseNo), Status: next.Status,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&model.GeneratorDestination{}).Where("id = ?", matched.ID).
				Updates(map[string]any{
					"license_no": strings.TrimSpace(next.LicenseNo),
					"status":     next.Status,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		kept[name] = true
	}
	for i := range existing {
		if !kept[strings.TrimSpace(existing[i].FacilityName)] {
			if err := tx.Delete(&model.GeneratorDestination{}, existing[i].ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *wasteGeneratorRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.WasteGenerator{}, id).Error
}

func (r *wasteGeneratorRepository) DeleteAudited(ctx context.Context, id uint, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&model.WasteGenerator{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("generator_id = ?", id).Delete(&model.GeneratorDestination{}).Error; err != nil {
			return err
		}
		audit.EntityID = id
		return tx.Create(audit).Error
	})
}

func (r *wasteGeneratorRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.WithContext(ctx).Model(&model.WasteGenerator{}).
		Select("status, COUNT(*) AS total").Group("status").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var status string
		var total int64
		if err := rows.Scan(&status, &total); err != nil {
			return nil, err
		}
		counts[status] = total
	}
	return counts, rows.Err()
}

func (r *wasteGeneratorRepository) Destinations(ctx context.Context, generatorID uint) ([]model.GeneratorDestination, error) {
	var rows []model.GeneratorDestination
	err := r.db.WithContext(ctx).Where("generator_id = ?", generatorID).
		Order("id ASC").Find(&rows).Error
	return rows, err
}
