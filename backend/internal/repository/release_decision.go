package repository

import (
	"context"
	"strings"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReleaseDecisionRepository owns all persistence operations for 放行决定.
type ReleaseDecisionRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ReleaseDecision], error)
	Get(context.Context, uint) (model.ReleaseDecision, error)
	GetForUpdate(context.Context, uint) (model.ReleaseDecision, error)
	CreateVersioned(context.Context, *model.ReleaseDecision, string, string, string) error
	UpdateVersioned(context.Context, uint, uint, *model.ReleaseDecision, string, string, string) error
	MarkDraftBasisInvalidByRun(context.Context, uint, string, string, string, string) (int64, error)
	MarkDraftBasisInvalidByProof(context.Context, uint, string, string, string, string) (int64, error)
	LockDraftBasisByRun(context.Context, uint) error
	LockDraftBasisByProof(context.Context, uint) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type releaseDecisionRepository struct {
	store *Store[model.ReleaseDecision]
}

func NewReleaseDecisionRepository(db *gorm.DB) ReleaseDecisionRepository {
	return &releaseDecisionRepository{store: NewStore[model.ReleaseDecision](db)}
}

func (r *releaseDecisionRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ReleaseDecision], error) {
	return r.store.List(ctx, q)
}
func (r *releaseDecisionRepository) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	var item model.ReleaseDecision
	err := r.store.db.WithContext(ctx).
		Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("version DESC") }).
		First(&item, id).Error
	return item, err
}
func (r *releaseDecisionRepository) GetForUpdate(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	var item model.ReleaseDecision
	query := r.store.db.WithContext(ctx)
	if query.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.First(&item, id).Error
	return item, err
}
func (r *releaseDecisionRepository) CreateVersioned(ctx context.Context, item *model.ReleaseDecision, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return createReleaseDecisionVersioned(ctx, tx, item, actor, requestID, reason)
	})
}
func (r *releaseDecisionRepository) UpdateVersioned(ctx context.Context, id, version uint, item *model.ReleaseDecision, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return updateReleaseDecisionVersioned(ctx, tx, id, version, item, actor, requestID, reason)
	})
}

func createReleaseDecisionVersioned(ctx context.Context, db *gorm.DB, item *model.ReleaseDecision, actor, requestID, reason string) error {
	if err := db.WithContext(ctx).Omit("Revisions").Create(item).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Create(releaseDecisionRevision(item, actor, requestID, reason)).Error
}

func updateReleaseDecisionVersioned(ctx context.Context, db *gorm.DB, id, version uint, item *model.ReleaseDecision, actor, requestID, reason string) error {
	result := db.WithContext(ctx).Model(&model.ReleaseDecision{}).Where("id = ? AND version = ?", id, version).
		Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return db.WithContext(ctx).Create(releaseDecisionRevision(item, actor, requestID, reason)).Error
}

func (r *releaseDecisionRepository) MarkDraftBasisInvalidByRun(ctx context.Context, runID uint, runCode, reason, actor, requestID string) (int64, error) {
	return r.markDraftBasisInvalid(ctx, "print_run_id", runID, runCode, reason, actor, requestID)
}

func (r *releaseDecisionRepository) MarkDraftBasisInvalidByProof(ctx context.Context, proofID uint, proofCode, reason, actor, requestID string) (int64, error) {
	return r.markDraftBasisInvalid(ctx, "color_proof_id", proofID, proofCode, reason, actor, requestID)
}

func (r *releaseDecisionRepository) LockDraftBasisByRun(ctx context.Context, runID uint) error {
	return lockDraftBasis(ctx, r.store.db, "print_run_id", runID)
}

func (r *releaseDecisionRepository) LockDraftBasisByProof(ctx context.Context, proofID uint) error {
	return lockDraftBasis(ctx, r.store.db, "color_proof_id", proofID)
}

func lockDraftBasis(ctx context.Context, db *gorm.DB, linkColumn string, linkID uint) error {
	query := db.WithContext(ctx).Model(&model.ReleaseDecision{})
	if query.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var ids []uint
	return query.Where(linkColumn+" = ? AND status = ?", linkID, "draft").Pluck("id", &ids).Error
}

func (r *releaseDecisionRepository) markDraftBasisInvalid(ctx context.Context, linkColumn string, linkID uint, linkedCode, reason, actor, requestID string) (int64, error) {
	db := r.store.db.WithContext(ctx)
	var decisions []model.ReleaseDecision
	query := db
	if query.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	query = query.Where(linkColumn+" = ? AND status = ?", linkID, "draft")
	if err := query.Find(&decisions).Error; err != nil {
		return 0, err
	}
	for i := range decisions {
		decision := &decisions[i]
		combinedReason := CombineInvalidReasons(decision.InvalidReason, reason)
		if decision.BasisValid == false && combinedReason == decision.InvalidReason {
			continue
		}
		wasValid := decision.BasisValid
		beforeState := "draft:invalid"
		if wasValid {
			beforeState = "draft:valid"
		}
		decision.Version++
		decision.BasisValid = false
		decision.InvalidReason = combinedReason
		decision.UpdatedAt = time.Now().UTC()
		if err := db.Model(&model.ReleaseDecision{}).Where("id = ?", decision.ID).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(decision).Error; err != nil {
			return 0, err
		}
		if err := db.Create(releaseDecisionRevision(decision, actor, requestID, reason)).Error; err != nil {
			return 0, err
		}
		if err := db.Create(&model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "basis_invalidated", EntityType: "ReleaseDecision",
			EntityID: decision.ID, BeforeState: beforeState, AfterState: "draft:invalid",
			Detail: linkedCode + ": " + combinedReason, CreatedAt: time.Now().UTC(),
		}).Error; err != nil {
			return 0, err
		}
	}
	return int64(len(decisions)), nil
}

func CombineInvalidReasons(existing, added string) string {
	existing = strings.TrimSpace(existing)
	added = strings.TrimSpace(added)
	if added == "" {
		return existing
	}
	if existing == "" {
		return added
	}
	for _, reason := range strings.Split(existing, "；") {
		if reason == added {
			return existing
		}
	}
	return existing + "；" + added
}

func releaseDecisionRevision(item *model.ReleaseDecision, actor, requestID, reason string) *model.ReleaseDecisionRevision {
	return &model.ReleaseDecisionRevision{
		ReleaseDecisionID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
		RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
		PrintRunID: item.PrintRunID, PrintRunCode: item.PrintRunCode, PrintRunVersion: item.PrintRunVersion,
		ColorProofID: item.ColorProofID, ColorProofCode: item.ColorProofCode, ColorProofVersion: item.ColorProofVersion,
		BasisValid: item.BasisValid, InvalidReason: item.InvalidReason,
		Actor: actor, RequestID: requestID, Reason: reason,
	}
}
func (r *releaseDecisionRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *releaseDecisionRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
