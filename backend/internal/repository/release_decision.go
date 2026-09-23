package repository

import (
	"context"
	"strings"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

// ReleaseDecisionRepository owns all persistence operations for 放行决定.
type ReleaseDecisionRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ReleaseDecision], error)
	Get(context.Context, uint) (model.ReleaseDecision, error)
	CreateVersioned(context.Context, *model.ReleaseDecision, string, string, string) error
	UpdateVersioned(context.Context, uint, uint, *model.ReleaseDecision, string, string, string) error
	ReleaseWithBasis(context.Context, ReleaseBasisInput) (string, error)
	EvaluateBasis(context.Context, *model.ReleaseDecision) string
	MarkBasisInvalid(context.Context, uint, string) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type releaseDecisionRepository struct {
	store *Store[model.ReleaseDecision]
}

func NewReleaseDecisionRepository(db *gorm.DB) ReleaseDecisionRepository {
	return &releaseDecisionRepository{store: NewStore[model.ReleaseDecision](db)}
}

// List pages decisions and extends the generic search with the linked batch
// and proof codes, so reviewers can find a decision by any related number.
func (r *releaseDecisionRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ReleaseDecision], error) {
	page, pageSize := normalizePage(q.Page, q.PageSize)
	db := r.store.db.WithContext(ctx).Model(&model.ReleaseDecision{})
	if search := strings.TrimSpace(strings.ToLower(q.Search)); search != "" {
		wildcard := "%" + search + "%"
		db = db.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ? OR LOWER(print_run_code) LIKE ? OR LOWER(color_proof_no) LIKE ?",
			wildcard, wildcard, wildcard, wildcard)
	}
	if status := strings.TrimSpace(q.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.ReleaseDecision]{}, err
	}
	items := make([]model.ReleaseDecision, 0)
	err := db.Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.ReleaseDecision]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *releaseDecisionRepository) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	var item model.ReleaseDecision
	err := r.store.db.WithContext(ctx).
		Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("version DESC") }).
		First(&item, id).Error
	return item, err
}
func (r *releaseDecisionRepository) CreateVersioned(ctx context.Context, item *model.ReleaseDecision, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(item).Error; err != nil {
			return err
		}
		return tx.Create(releaseDecisionRevision(item, actor, requestID, reason)).Error
	})
}
func (r *releaseDecisionRepository) UpdateVersioned(ctx context.Context, id, version uint, item *model.ReleaseDecision, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReleaseDecision{}).Where("id = ? AND version = ?", id, version).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return tx.Create(releaseDecisionRevision(item, actor, requestID, reason)).Error
	})
}

func releaseDecisionRevision(item *model.ReleaseDecision, actor, requestID, reason string) *model.ReleaseDecisionRevision {
	return &model.ReleaseDecisionRevision{
		ReleaseDecisionID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
		RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
		PrintRunID: item.PrintRunID, ColorProofID: item.ColorProofID,
		PrintRunCode: item.PrintRunCode, ColorProofNo: item.ColorProofNo,
		BasisRunVersion: item.BasisRunVersion, BasisProofVersion: item.BasisProofVersion,
		BasisProofReading:   item.BasisProofReading,
		BasisToleranceLimit: item.BasisToleranceLimit,
		BasisInvalidReason:  item.BasisInvalidReason,
		Actor:               actor, RequestID: requestID, Reason: reason,
	}
}
func (r *releaseDecisionRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *releaseDecisionRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
