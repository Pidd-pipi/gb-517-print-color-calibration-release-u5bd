package repository

import (
	"context"

	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

// UnitOfWork makes cross-aggregate release rules atomic. Reviewing a decision
// and changing its run/proof evidence therefore cannot leave one side written
// while the other side is rolled back.
type UnitOfWork interface {
	Within(context.Context, func(Dependencies) error) error
}

type Dependencies struct {
	DB                             *gorm.DB
	PrintRuns                      PrintRunRepository
	CreatePrintRunVersioned        func(context.Context, *model.PrintRun, string, string, string) error
	UpdatePrintRunVersioned        func(context.Context, uint, uint, *model.PrintRun, string, string, string) error
	ColorProofs                    ColorProofRepository
	UpdateColorProof               func(context.Context, uint, uint, *model.ColorProof) error
	ReleaseDecisions               ReleaseDecisionRepository
	CreateReleaseDecisionVersioned func(context.Context, *model.ReleaseDecision, string, string, string) error
	UpdateReleaseDecisionVersioned func(context.Context, uint, uint, *model.ReleaseDecision, string, string, string) error
	Security                       SecurityRepository
}

type unitOfWork struct {
	db *gorm.DB
}

func NewUnitOfWork(db *gorm.DB) UnitOfWork { return &unitOfWork{db: db} }

func (u *unitOfWork) Within(ctx context.Context, fn func(Dependencies) error) error {
	return u.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(Dependencies{
			DB:        tx,
			PrintRuns: NewPrintRunRepository(tx),
			CreatePrintRunVersioned: func(ctx context.Context, item *model.PrintRun, actor, requestID, reason string) error {
				return createPrintRunVersioned(ctx, tx, item, actor, requestID, reason)
			},
			UpdatePrintRunVersioned: func(ctx context.Context, id, version uint, item *model.PrintRun, actor, requestID, reason string) error {
				return updatePrintRunVersioned(ctx, tx, id, version, item, actor, requestID, reason)
			},
			ColorProofs: NewColorProofRepository(tx),
			UpdateColorProof: func(ctx context.Context, id, version uint, item *model.ColorProof) error {
				return updateColorProof(ctx, tx, id, version, item)
			},
			ReleaseDecisions: NewReleaseDecisionRepository(tx),
			CreateReleaseDecisionVersioned: func(ctx context.Context, item *model.ReleaseDecision, actor, requestID, reason string) error {
				return createReleaseDecisionVersioned(ctx, tx, item, actor, requestID, reason)
			},
			UpdateReleaseDecisionVersioned: func(ctx context.Context, id, version uint, item *model.ReleaseDecision, actor, requestID, reason string) error {
				return updateReleaseDecisionVersioned(ctx, tx, id, version, item, actor, requestID, reason)
			},
			Security: NewSecurityRepository(tx),
		})
	})
}
