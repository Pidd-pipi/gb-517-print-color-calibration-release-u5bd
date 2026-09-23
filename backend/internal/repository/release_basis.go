package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Basis invalidation reasons surfaced verbatim to the reviewer UI.
const (
	BasisReasonRunMissing      = "关联印刷批次已不存在"
	BasisReasonProofMissing    = "关联校样已不存在"
	BasisReasonProofRejected   = "关联校样已被拒绝，不能作为放行依据"
	BasisReasonRunChanged      = "批次配置已换版本，放行依据不再对应原批次配置"
	BasisReasonProofChanged    = "校样出现更新读数，放行依据与当前读数不一致"
	BasisReasonRunNotProofing  = "批次已不在校样阶段"
	BasisReasonProofNotAccept  = "校样当前不是已接收状态"
	BasisReasonReadingTooLarge = "当前读数超出批次允许范围"
)

// ReleaseBasisInput carries a reviewer's 放行 attempt. The batch and proof are
// always re-read inside the same transaction as the decision mutation.
type ReleaseBasisInput struct {
	DecisionID      uint
	ExpectedVersion uint
	Target          string
	Reason          string
	Actor           string
	RequestID       string
}

// ReleaseWithBasis atomically re-reads the bound print run and color proof
// under row locks, re-validates the decision basis and only then performs the
// release. When the basis has gone stale the decision is persisted as 依据失效
// in the same transaction and the invalidation reason is returned. There is no
// outcome where the review write and a concurrent evidence change each succeed
// by half.
func (r *releaseDecisionRepository) ReleaseWithBasis(ctx context.Context, in ReleaseBasisInput) (string, error) {
	var persistedReason string
	// SQLite (local dev and tests) locks the whole database on write and does
	// not support SELECT ... FOR UPDATE; serializing release transactions in
	// process gives the same all-or-nothing semantics that row locks provide
	// on MySQL/PostgreSQL.
	releaseMu.Lock()
	defer releaseMu.Unlock()
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var decision model.ReleaseDecision
		decisionQuery := tx.Where("id = ? AND version = ?", in.DecisionID, in.ExpectedVersion)
		if r.supportRowLock(tx) {
			decisionQuery = decisionQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		result := decisionQuery.First(&decision)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrVersionConflict
		}
		if result.Error != nil {
			return result.Error
		}

		var run model.PrintRun
		runQuery := tx
		var proof model.ColorProof
		proofQ := tx
		if r.supportRowLock(tx) {
			runQuery = runQuery.Clauses(clause.Locking{Strength: "UPDATE"})
			proofQ = proofQ.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		runErr := runQuery.First(&run, decision.PrintRunID).Error
		proofErr := proofQ.First(&proof, decision.ColorProofID).Error
		if runErr != nil && !errors.Is(runErr, gorm.ErrRecordNotFound) {
			return runErr
		}
		if proofErr != nil && !errors.Is(proofErr, gorm.ErrRecordNotFound) {
			return proofErr
		}
		reason := evaluateBasis(&decision, &run, !errors.Is(runErr, gorm.ErrRecordNotFound),
			&proof, !errors.Is(proofErr, gorm.ErrRecordNotFound))

		now := time.Now().UTC()
		if reason != "" {
			persistedReason = reason
			// Persist the stale basis exactly once; repeated clicks keep the
			// same version instead of appending duplicate revisions.
			if decision.BasisInvalidReason == "" {
				decision.BasisInvalidReason = reason
				decision.Version++
				decision.UpdatedAt = now
				if err := tx.Model(&model.ReleaseDecision{}).
					Where("id = ?", decision.ID).
					Updates(map[string]any{
						"basis_invalid_reason": reason,
						"version":              decision.Version,
						"updated_at":           now,
					}).Error; err != nil {
					return err
				}
				if err := tx.Create(releaseDecisionRevision(&decision, in.Actor, in.RequestID, "依据失效："+reason)).Error; err != nil {
					return err
				}
			}
			return nil
		}

		// Basis still holds: the decision crosses the release gate. The linked
		// batch's -> released transition is driven by the same reviewer action;
		// a batch already released by an earlier decision is simply skipped.
		before := decision.Status
		decision.Status = in.Target
		decision.Version++
		decision.UpdatedAt = now
		updateResult := tx.Model(&model.ReleaseDecision{}).
			Where("id = ? AND version = ?", decision.ID, in.ExpectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(&decision)
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if err := tx.Create(releaseDecisionRevision(&decision, in.Actor, in.RequestID, in.Reason)).Error; err != nil {
			return err
		}
		audits := []model.AuditLog{{
			RequestID: in.RequestID, Actor: in.Actor, Action: "transition", EntityType: "ReleaseDecision",
			EntityID: decision.ID, BeforeState: before, AfterState: in.Target, Detail: in.Reason, CreatedAt: now,
		}}
		if run.Status != "released" {
			runBefore := run.Status
			run.Status = "released"
			run.Version++
			run.UpdatedAt = now
			runResult := tx.Model(&model.PrintRun{}).
				Where("id = ? AND version = ?", run.ID, run.Version-1).
				Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(&run)
			if runResult.Error != nil {
				return runResult.Error
			}
			if runResult.RowsAffected == 0 {
				return ErrVersionConflict
			}
			if err := tx.Create(printRunRevision(&run, in.Actor, in.RequestID, "放行决定 "+decision.Code+" 复核放行")).Error; err != nil {
				return err
			}
			audits = append(audits, model.AuditLog{
				RequestID: in.RequestID, Actor: in.Actor, Action: "transition", EntityType: "PrintRun",
				EntityID: run.ID, BeforeState: runBefore, AfterState: "released",
				Detail: "basis re-read at release: 批次 " + run.Code + " 校样 " + proof.Code, CreatedAt: now,
			})
		}
		return tx.Create(audits).Error
	})
	return persistedReason, err
}

// MarkBasisInvalid re-reads the bound records and persists any newly detected
// staleness (e.g. while a reviewer browses the decision list).
func (r *releaseDecisionRepository) MarkBasisInvalid(ctx context.Context, decisionID uint, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var decision model.ReleaseDecision
		if err := tx.First(&decision, decisionID).Error; err != nil {
			return err
		}
		if decision.BasisInvalidReason != "" || reason == "" {
			return nil
		}
		now := time.Now().UTC()
		decision.BasisInvalidReason = reason
		decision.Version++
		decision.UpdatedAt = now
		if err := tx.Model(&model.ReleaseDecision{}).Where("id = ?", decision.ID).
			Updates(map[string]any{"basis_invalid_reason": reason, "version": decision.Version, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(releaseDecisionRevision(&decision, "system", "basis-recheck", "依据失效："+reason)).Error
	})
}

// EvaluateBasis re-reads the bound batch/proof and reports the first staleness
// reason, or "" when the captured basis still supports a release.
func (r *releaseDecisionRepository) EvaluateBasis(ctx context.Context, decision *model.ReleaseDecision) string {
	run, runErr := NewStore[model.PrintRun](r.store.db).Get(ctx, decision.PrintRunID)
	proof, proofErr := NewStore[model.ColorProof](r.store.db).Get(ctx, decision.ColorProofID)
	return evaluateBasis(decision, &run, !errors.Is(runErr, gorm.ErrRecordNotFound),
		&proof, !errors.Is(proofErr, gorm.ErrRecordNotFound))
}

// evaluateBasis decides whether freshly re-read records still match the basis
// snapshot frozen on the decision.
func evaluateBasis(decision *model.ReleaseDecision, run *model.PrintRun, runFound bool, proof *model.ColorProof, proofFound bool) string {
	if !runFound {
		return BasisReasonRunMissing
	}
	if !proofFound {
		return BasisReasonProofMissing
	}
	// A rejected proof is always a hard stop even when its version also
	// changed, so the reviewer sees the decisive reason.
	if proof.Status == "rejected" {
		return BasisReasonProofRejected
	}
	if run.Version != decision.BasisRunVersion {
		return BasisReasonRunChanged
	}
	if proof.Version != decision.BasisProofVersion {
		return BasisReasonProofChanged
	}
	if run.Status != string(constants.RunStateProofing) {
		return BasisReasonRunNotProofing
	}
	if proof.Status != "accepted" {
		return BasisReasonProofNotAccept
	}
	limit := decision.BasisToleranceLimit
	if limit <= 0 {
		limit = 3
	}
	if math.Abs(proof.MetricValue) > limit {
		return fmt.Sprintf("%s（当前读数 %.2f %s，允许范围 ≤ %.2f）", BasisReasonReadingTooLarge, proof.MetricValue, proof.MetricUnit, limit)
	}
	return ""
}

func (r *releaseDecisionRepository) supportRowLock(tx *gorm.DB) bool {
	return tx.Dialector.Name() != "sqlite"
}

// releaseMu serializes basis-gated release transactions on SQLite, where row
// locks are unavailable.
var releaseMu sync.Mutex
