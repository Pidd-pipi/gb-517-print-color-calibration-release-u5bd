package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/blueship581/print-color-calibration-release/backend/internal/repository"
	"gorm.io/gorm"
)

type ReleaseDecisionService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ReleaseDecision], error)
	Get(context.Context, uint) (model.ReleaseDecision, error)
	Create(context.Context, dto.CreateReleaseDecision, string, string) (model.ReleaseDecision, error)
	Update(context.Context, uint, dto.UpdateReleaseDecision, string, string) (model.ReleaseDecision, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ReleaseDecision, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type releaseDecisionService struct {
	repository repository.ReleaseDecisionRepository
	runs       repository.PrintRunRepository
	proofs     repository.ColorProofRepository
	security   SecurityService
}

func NewReleaseDecisionService(repo repository.ReleaseDecisionRepository, runs repository.PrintRunRepository, proofs repository.ColorProofRepository, security SecurityService) ReleaseDecisionService {
	return &releaseDecisionService{repository: repo, runs: runs, proofs: proofs, security: security}
}

func (s *releaseDecisionService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ReleaseDecision], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	for i := range page.Items {
		s.recheckBasis(ctx, &page.Items[i])
	}
	return page, nil
}

func (s *releaseDecisionService) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	s.recheckBasis(ctx, &item)
	return item, nil
}

func (s *releaseDecisionService) Create(ctx context.Context, input dto.CreateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	if input.PrintRunID == 0 || input.ColorProofID == 0 {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 必须关联一条印刷批次和一份校样", ErrInvalidInput)
	}
	run, err := s.runs.Get(ctx, input.PrintRunID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 关联印刷批次不存在", ErrInvalidInput)
	}
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	proof, err := s.proofs.Get(ctx, input.ColorProofID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 关联校样不存在", ErrInvalidInput)
	}
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	// A draft is generated only while the batch is in the proofing stage and
	// the accepted proof reading is inside the batch tolerance.
	if run.Status != string(constants.RunStateProofing) {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 批次 %s 当前为 %s，只有校样阶段才能生成放行草稿", ErrInvalidInput, run.Code, run.Status)
	}
	if proof.Status != "accepted" {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 校样 %s 当前为 %s，只有已接收校样才能作为放行依据", ErrInvalidInput, proof.Code, proof.Status)
	}
	limit := normalizeTolerance(run.ToleranceLimit)
	if absFloat(proof.MetricValue) > limit {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 校样读数 %.2f %s 超出批次允许范围（≤ %.2f），不能生成放行草稿", ErrInvalidInput, proof.MetricValue, proof.MetricUnit, limit)
	}

	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if code == "" {
		code = fmt.Sprintf("RD-%s-%d", run.Code, time.Now().UTC().UnixNano()%1e6)
	}
	now := time.Now().UTC()
	item := model.ReleaseDecision{
		BaseModel: model.BaseModel{
			Code:   code,
			Name:   fmt.Sprintf("放行决定·%s·%s", run.Code, proof.Code),
			Status: model.ReleaseDecisionInitialStatus, Version: 1,
			Description: strings.TrimSpace(input.Description),
		},
		Facility: run.Facility, Owner: actor,
		Category: run.Category, RiskLevel: run.RiskLevel,
		MetricValue: proof.MetricValue, MetricUnit: firstNonEmpty(proof.MetricUnit, run.MetricUnit),
		EffectiveAt: now, Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: run.Code,
		PrintRunID:  run.ID, ColorProofID: proof.ID,
		PrintRunCode: run.Code, ColorProofNo: proof.Code,
		BasisRunVersion:     run.Version,
		BasisProofVersion:   proof.Version,
		BasisProofReading:   proof.MetricValue,
		BasisToleranceLimit: limit,
	}
	if item.Evidence == "" {
		item.Evidence = firstNonEmpty(proof.Evidence, run.Evidence)
	}
	if err := s.repository.CreateVersioned(ctx, &item, actor, requestID, "created release decision draft from accepted proof"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("create 放行决定: %w", err)
	}
	item.BasisValid = true
	item.BasisReason = "依据有效：批次处于校样阶段且读数在允许范围内"
	_ = s.security.Audit(ctx, actor, requestID, "create", "ReleaseDecision", item.ID, "", item.Status,
		fmt.Sprintf("draft bound to 批次 %s@v%d 校样 %s@v%d 读数 %.2f/%.2f", run.Code, run.Version, proof.Code, proof.Version, proof.MetricValue, limit))
	return item, nil
}

func (s *releaseDecisionService) Update(ctx context.Context, id uint, input dto.UpdateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	if current.Status == string(constants.DecisionTypeRelease) || current.Status == string(constants.DecisionTypeQuarantine) {
		return model.ReleaseDecision{}, ErrLocked
	}
	// The bound batch/proof and basis snapshot are immutable; only evidence may
	// be amended while the decision is still open.
	current.Description = strings.TrimSpace(input.Description)
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, "updated decision evidence"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("update 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ReleaseDecision", id, current.Status, current.Status, "updated evidence only")
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ReleaseDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == string(constants.DecisionTypeRelease) || target == string(constants.DecisionTypeQuarantine) ||
		current.Status == string(constants.DecisionTypeRelease) || current.Status == string(constants.DecisionTypeQuarantine)) && !canReview(role) {
		return model.ReleaseDecision{}, ErrForbidden
	}
	if !constants.CanTransition(constants.ReleaseDecisionTransitions, current.Status, target) {
		return model.ReleaseDecision{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}

	// 放行 re-reads the bound batch and proof inside one transaction with the
	// decision and batch writes; a concurrent proof reject, reading update or
	// configuration version bump cannot interleave with a partial success.
	if target == string(constants.DecisionTypeRelease) {
		if current.BasisInvalidReason != "" {
			return model.ReleaseDecision{}, fmt.Errorf("%w: %s", ErrBasisInvalid, current.BasisInvalidReason)
		}
		// The authoritative batch/proof re-read happens row-locked inside the
		// release transaction; a stale basis is persisted there and returned.
		persistedReason, err := s.repository.ReleaseWithBasis(ctx, repository.ReleaseBasisInput{
			DecisionID: id, ExpectedVersion: input.ExpectedVersion, Target: target,
			Reason: input.Reason, Actor: actor, RequestID: requestID,
		})
		if err != nil {
			return model.ReleaseDecision{}, fmt.Errorf("放行决定: %w", err)
		}
		if persistedReason != "" {
			return model.ReleaseDecision{}, fmt.Errorf("%w: %s", ErrBasisInvalid, persistedReason)
		}
		return s.Get(ctx, id)
	}

	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("transition 放行决定: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "ReleaseDecision", id, before, target, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.ReleaseDecisionInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ReleaseDecision", id, current.Status, "deleted", "soft deleted 放行决定")
}

func (s *releaseDecisionService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

// currentBasisReason re-reads the bound batch and proof and returns the staleness
// reason, or "" when the basis still supports the captured decision.
func (s *releaseDecisionService) currentBasisReason(ctx context.Context, decision *model.ReleaseDecision) string {
	return s.repository.EvaluateBasis(ctx, decision)
}

// recheckBasis decorates a decision with the live basis verdict. Open
// decisions are re-read from the database and a newly discovered invalidation
// is persisted so reviewers see it on the page immediately; resolved decisions
// keep the historical basis frozen at release time.
func (s *releaseDecisionService) recheckBasis(ctx context.Context, decision *model.ReleaseDecision) {
	if decision.Status != model.ReleaseDecisionInitialStatus {
		decision.BasisValid = decision.BasisInvalidReason == ""
		decision.BasisReason = "已冻结依据：" + decision.PrintRunCode +
			" v" + strconv.FormatUint(uint64(decision.BasisRunVersion), 10) + " / " + decision.ColorProofNo +
			" v" + strconv.FormatUint(uint64(decision.BasisProofVersion), 10)
		if decision.BasisInvalidReason != "" {
			decision.BasisReason = decision.BasisInvalidReason
		}
		return
	}
	reason := s.currentBasisReason(ctx, decision)
	decision.BasisValid = reason == "" && decision.BasisInvalidReason == ""
	switch {
	case decision.BasisInvalidReason != "":
		decision.BasisReason = decision.BasisInvalidReason
	case reason != "":
		decision.BasisReason = reason
		_ = s.repository.MarkBasisInvalid(ctx, decision.ID, reason)
	default:
		decision.BasisReason = "依据有效：批次处于校样阶段且读数在允许范围内"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
