package repository

import (
	"context"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"gorm.io/gorm"
)

// WasteGeneratorRepository owns all persistence operations for 产废单位, including
// its filed 备案去向 child rows.
type WasteGeneratorRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.WasteGenerator], error)
	Get(context.Context, uint) (model.WasteGenerator, error)
	FindByCode(context.Context, string) (model.WasteGenerator, error)
	Create(context.Context, *model.WasteGenerator) error
	CreateAudited(ctx context.Context, item *model.WasteGenerator, destinations []model.DisposalDestination, audit *model.AuditLog) error
	Update(context.Context, uint, uint, *model.WasteGenerator) error
	UpdateAudited(ctx context.Context, id, version uint, item *model.WasteGenerator, destinations []model.DisposalDestination, audit *model.AuditLog) error
	// TransitionAudited persists a status move without touching the filed
	// destinations: filing changes and workflow moves are independent concerns.
	TransitionAudited(ctx context.Context, id, version uint, item *model.WasteGenerator, audit *model.AuditLog) error
	Delete(context.Context, uint) error
	DeleteAudited(context.Context, uint, *model.AuditLog) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type wasteGeneratorRepository struct {
	db    *gorm.DB
	store *Store[model.WasteGenerator]
}

func NewWasteGeneratorRepository(db *gorm.DB) WasteGeneratorRepository {
	return &wasteGeneratorRepository{db: db, store: NewStore[model.WasteGenerator](db)}
}

func (r *wasteGeneratorRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.WasteGenerator], error) {
	page, err := r.store.List(ctx, q)
	if err != nil {
		return page, err
	}
	if err := r.attachDestinations(ctx, page.Items); err != nil {
		return page, err
	}
	return page, nil
}

func (r *wasteGeneratorRepository) Get(ctx context.Context, id uint) (model.WasteGenerator, error) {
	item, err := r.store.Get(ctx, id)
	if err != nil {
		return item, err
	}
	destinations, err := r.listDestinations(ctx, []uint{item.ID})
	if err != nil {
		return item, err
	}
	item.Destinations = destinations[item.ID]
	return item, nil
}

func (r *wasteGeneratorRepository) FindByCode(ctx context.Context, code string) (model.WasteGenerator, error) {
	item, err := r.store.FindByCode(ctx, code)
	if err != nil {
		return item, err
	}
	destinations, err := r.listDestinations(ctx, []uint{item.ID})
	if err != nil {
		return item, err
	}
	item.Destinations = destinations[item.ID]
	return item, nil
}

func (r *wasteGeneratorRepository) Create(ctx context.Context, item *model.WasteGenerator) error {
	return r.store.Create(ctx, item)
}

func (r *wasteGeneratorRepository) CreateAudited(ctx context.Context, item *model.WasteGenerator, destinations []model.DisposalDestination, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		if err := reconcileDestinations(tx, item.ID, destinations); err != nil {
			return err
		}
		audit.EntityID = item.ID
		return tx.Create(audit).Error
	})
}

func (r *wasteGeneratorRepository) Update(ctx context.Context, id, version uint, item *model.WasteGenerator) error {
	return r.store.Update(ctx, id, version, item)
}

func (r *wasteGeneratorRepository) TransitionAudited(ctx context.Context, id, expectedVersion uint, item *model.WasteGenerator, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.WasteGenerator{}).
			Where("id = ? AND version = ?", id, expectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "destinations").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		audit.EntityID = id
		return tx.Create(audit).Error
	})
}

func (r *wasteGeneratorRepository) UpdateAudited(ctx context.Context, id, expectedVersion uint, item *model.WasteGenerator, destinations []model.DisposalDestination, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.WasteGenerator{}).
			Where("id = ? AND version = ?", id, expectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "destinations").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if err := reconcileDestinations(tx, id, destinations); err != nil {
			return err
		}
		audit.EntityID = id
		return tx.Create(audit).Error
	})
}

func (r *wasteGeneratorRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *wasteGeneratorRepository) DeleteAudited(ctx context.Context, id uint, audit *model.AuditLog) error {
	return r.store.DeleteAudited(ctx, id, audit)
}
func (r *wasteGeneratorRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

// listDestinations loads every filed destination (active and revoked) for the
// given generators so callers can distinguish "never filed" from "filing revoked".
func (r *wasteGeneratorRepository) listDestinations(ctx context.Context, ids []uint) (map[uint][]model.DisposalDestination, error) {
	grouped := make(map[uint][]model.DisposalDestination)
	if len(ids) == 0 {
		return grouped, nil
	}
	var rows []model.DisposalDestination
	err := r.db.WithContext(ctx).
		Where("generator_id IN ?", ids).
		Order("generator_id ASC, status ASC, facility_name ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		grouped[row.GeneratorID] = append(grouped[row.GeneratorID], row)
	}
	return grouped, nil
}

func (r *wasteGeneratorRepository) attachDestinations(ctx context.Context, items []model.WasteGenerator) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	grouped, err := r.listDestinations(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Destinations = grouped[items[i].ID]
	}
	return nil
}

// reconcileDestinations replaces the active filing set inside an existing
// transaction: present rows are inserted/re-activated, missing rows are revoked
// rather than deleted so historical references survive.
func reconcileDestinations(tx *gorm.DB, generatorID uint, wanted []model.DisposalDestination) error {
	var existing []model.DisposalDestination
	if err := tx.Where("generator_id = ?", generatorID).Find(&existing).Error; err != nil {
		return err
	}
	byName := make(map[string]model.DisposalDestination, len(existing))
	for _, row := range existing {
		byName[model.NormalizeDestinationName(row.FacilityName)] = row
	}
	wantedByName := make(map[string]model.DisposalDestination, len(wanted))
	for _, row := range wanted {
		wantedByName[model.NormalizeDestinationName(row.FacilityName)] = row
	}
	now := time.Now().UTC()
	for _, row := range existing {
		key := model.NormalizeDestinationName(row.FacilityName)
		if incoming, ok := wantedByName[key]; ok {
			if err := tx.Model(&model.DisposalDestination{}).Where("id = ?", row.ID).
				Updates(map[string]any{"license_number": incoming.LicenseNumber, "status": model.DestinationStatusActive, "updated_at": now}).Error; err != nil {
				return err
			}
			continue
		}
		if row.Status == model.DestinationStatusActive {
			if err := tx.Model(&model.DisposalDestination{}).Where("id = ?", row.ID).
				Updates(map[string]any{"status": model.DestinationStatusRevoked, "updated_at": now}).Error; err != nil {
				return err
			}
		}
	}
	for key, incoming := range wantedByName {
		if _, found := byName[key]; found {
			continue
		}
		incoming.GeneratorID = generatorID
		incoming.Status = model.DestinationStatusActive
		if err := tx.Create(&incoming).Error; err != nil {
			return err
		}
	}
	return nil
}
