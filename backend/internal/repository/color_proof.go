package repository

import (
	"context"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ColorProofRepository owns all persistence operations for 色彩校样.
type ColorProofRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ColorProof], error)
	Get(context.Context, uint) (model.ColorProof, error)
	GetForUpdate(context.Context, uint) (model.ColorProof, error)
	Create(context.Context, *model.ColorProof) error
	Update(context.Context, uint, uint, *model.ColorProof) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type colorProofRepository struct {
	store *Store[model.ColorProof]
}

func NewColorProofRepository(db *gorm.DB) ColorProofRepository {
	return &colorProofRepository{store: NewStore[model.ColorProof](db)}
}

func (r *colorProofRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ColorProof], error) {
	return r.store.List(ctx, q)
}
func (r *colorProofRepository) Get(ctx context.Context, id uint) (model.ColorProof, error) {
	return r.store.Get(ctx, id)
}
func (r *colorProofRepository) GetForUpdate(ctx context.Context, id uint) (model.ColorProof, error) {
	var item model.ColorProof
	query := r.store.db.WithContext(ctx)
	if query.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.First(&item, id).Error
	return item, err
}
func (r *colorProofRepository) Create(ctx context.Context, item *model.ColorProof) error {
	return r.store.Create(ctx, item)
}
func (r *colorProofRepository) Update(ctx context.Context, id, version uint, item *model.ColorProof) error {
	return r.store.Update(ctx, id, version, item)
}
func updateColorProof(ctx context.Context, db *gorm.DB, id, version uint, item *model.ColorProof) error {
	result := db.WithContext(ctx).Model(&model.ColorProof{}).Where("id = ? AND version = ?", id, version).
		Select("*").Omit("id", "code", "created_at", "deleted_at").Updates(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}
func (r *colorProofRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *colorProofRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
