package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/blueship581/print-color-calibration-release/backend/internal/repository"
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
	unitOfWork repository.UnitOfWork
	security   SecurityService
}

func NewReleaseDecisionService(
	repo repository.ReleaseDecisionRepository,
	unitOfWork repository.UnitOfWork,
	security SecurityService,
) ReleaseDecisionService {
	return &releaseDecisionService{repository: repo, unitOfWork: unitOfWork, security: security}
}

func (s *releaseDecisionService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ReleaseDecision], error) {
	return s.repository.List(ctx, query)
}

func (s *releaseDecisionService) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Create(ctx context.Context, input dto.CreateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	if err := validateReleaseDecisionBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseDecision{}, err
	}
	var item model.ReleaseDecision
	err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		if err := deps.ReleaseDecisions.LockDraftBasisByRun(ctx, input.PrintRunID); err != nil {
			return err
		}
		if err := deps.ReleaseDecisions.LockDraftBasisByProof(ctx, input.ColorProofID); err != nil {
			return err
		}
		run, err := deps.PrintRuns.GetForUpdate(ctx, input.PrintRunID)
		if err != nil {
			return err
		}
		proof, err := deps.ColorProofs.GetForUpdate(ctx, input.ColorProofID)
		if err != nil {
			return err
		}
		if err := validateReleaseBasis(run, proof); err != nil {
			return err
		}
		item = model.ReleaseDecision{
			BaseModel: model.BaseModel{
				Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
				Status: model.ReleaseDecisionInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
			},
			Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
			Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
			MetricValue: proof.MetricValue, MetricUnit: proof.MetricUnit,
			EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
			RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
			PrintRunID:  run.ID, PrintRunCode: run.Code, PrintRunVersion: run.Version,
			ColorProofID: proof.ID, ColorProofCode: proof.Code, ColorProofVersion: proof.Version,
			BasisValid: true,
		}
		if err := deps.CreateReleaseDecisionVersioned(ctx, &item, actor, requestID, "created release decision from valid run and proof"); err != nil {
			return fmt.Errorf("create 放行决定: %w", err)
		}
		return deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "create", EntityType: "ReleaseDecision",
			EntityID: item.ID, BeforeState: "", AfterState: item.Status,
			Detail:    fmt.Sprintf("basis=%s@v%d,%s@v%d", run.Code, run.Version, proof.Code, proof.Version),
			CreatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		return model.ReleaseDecision{}, err
	}
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
	if err := validateReleaseDecisionBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseDecision{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, "updated decision evidence"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("update 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ReleaseDecision", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ReleaseDecision, error) {
	target := strings.TrimSpace(input.Status)
	if (target == string(constants.DecisionTypeRelease) || target == string(constants.DecisionTypeQuarantine)) && !canReview(role) {
		return model.ReleaseDecision{}, ErrForbidden
	}
	err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		current, err := deps.ReleaseDecisions.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if (current.Status == string(constants.DecisionTypeRelease) || current.Status == string(constants.DecisionTypeQuarantine)) && !canReview(role) {
			return ErrForbidden
		}
		if !constants.CanTransition(constants.ReleaseDecisionTransitions, current.Status, target) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
		}

		if target == string(constants.DecisionTypeRelease) {
			if !current.BasisValid {
				return fmt.Errorf("%w: %s", ErrBasisInvalid, current.InvalidReason)
			}
			run, proof, err := lockedReleaseBasis(ctx, deps, current)
			if err != nil {
				return err
			}
			if reason := staleReleaseBasisReason(current, run, proof); reason != "" {
				return fmt.Errorf("%w: %s", ErrBasisInvalid, reason)
			}
		}

		before := current.Status
		current.Status = target
		current.Version = input.ExpectedVersion + 1
		current.UpdatedAt = time.Now().UTC()
		if err := deps.UpdateReleaseDecisionVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, input.Reason); err != nil {
			return fmt.Errorf("transition 放行决定: %w", err)
		}
		if err := deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "transition", EntityType: "ReleaseDecision",
			EntityID: id, BeforeState: before, AfterState: target, Detail: input.Reason,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("persist transition audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	return s.repository.Get(ctx, id)
}

func lockedReleaseBasis(ctx context.Context, deps repository.Dependencies, decision model.ReleaseDecision) (model.PrintRun, model.ColorProof, error) {
	run, err := deps.PrintRuns.GetForUpdate(ctx, decision.PrintRunID)
	if err != nil {
		return model.PrintRun{}, model.ColorProof{}, err
	}
	proof, err := deps.ColorProofs.GetForUpdate(ctx, decision.ColorProofID)
	if err != nil {
		return model.PrintRun{}, model.ColorProof{}, err
	}
	return run, proof, nil
}

func validateReleaseBasis(run model.PrintRun, proof model.ColorProof) error {
	reasons := make([]string, 0, 4)
	if run.Status != string(constants.RunStateProofing) {
		reasons = append(reasons, fmt.Sprintf("关联批次 %s 当前为 %s，必须处于校样阶段", run.Code, run.Status))
	}
	if proof.Status != "accepted" {
		reasons = append(reasons, fmt.Sprintf("关联校样 %s 当前为 %s，必须已接收", proof.Code, proof.Status))
	}
	if !sameBasisGroup(run, proof) {
		reasons = append(reasons, fmt.Sprintf("校样 %s 不属于批次 %s", proof.Code, run.Code))
	}
	if reason := readingRangeReason(run, proof); reason != "" {
		reasons = append(reasons, reason)
	}
	if len(reasons) > 0 {
		return fmt.Errorf("%w: %s", ErrBasisInvalid, strings.Join(reasons, "；"))
	}
	return nil
}

func staleReleaseBasisReason(decision model.ReleaseDecision, run model.PrintRun, proof model.ColorProof) string {
	reasons := make([]string, 0, 5)
	if run.Version != decision.PrintRunVersion {
		reasons = append(reasons, fmt.Sprintf("关联批次 %s 配置已换版（v%d -> v%d）", decision.PrintRunCode, decision.PrintRunVersion, run.Version))
	}
	if proof.Version != decision.ColorProofVersion {
		reasons = append(reasons, fmt.Sprintf("关联校样 %s 出现更新读数（v%d -> v%d）", decision.ColorProofCode, decision.ColorProofVersion, proof.Version))
	}
	if run.Status != string(constants.RunStateProofing) {
		reasons = append(reasons, fmt.Sprintf("关联批次 %s 已离开校样阶段（当前 %s）", run.Code, run.Status))
	}
	if proof.Status == "rejected" {
		reasons = append(reasons, fmt.Sprintf("关联校样 %s 已被拒绝", proof.Code))
	} else if proof.Status != "accepted" {
		reasons = append(reasons, fmt.Sprintf("关联校样 %s 当前不是已接收状态（%s）", proof.Code, proof.Status))
	}
	if reason := readingRangeReason(run, proof); reason != "" {
		reasons = append(reasons, reason)
	}
	return strings.Join(reasons, "；")
}

func readingRangeReason(run model.PrintRun, proof model.ColorProof) string {
	if run.AllowedMax <= 0 || run.AllowedMin > run.AllowedMax {
		return fmt.Sprintf("批次 %s 未配置有效读数允许范围", run.Code)
	}
	if proof.MetricValue < run.AllowedMin || proof.MetricValue > run.AllowedMax {
		return fmt.Sprintf("校样读数 %s 超出批次允许范围 %s ~ %s",
			formatReading(proof.MetricValue, proof.MetricUnit),
			formatReading(run.AllowedMin, run.MetricUnit), formatReading(run.AllowedMax, run.MetricUnit))
	}
	return ""
}

func sameBasisGroup(run model.PrintRun, proof model.ColorProof) bool {
	runGroup := strings.TrimSpace(run.RelatedCode)
	proofGroup := strings.TrimSpace(proof.RelatedCode)
	return runGroup != "" && strings.EqualFold(runGroup, proofGroup)
}

func formatReading(value float64, unit string) string {
	return fmt.Sprintf("%s %s", strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), "."), strings.TrimSpace(unit))
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

func validateReleaseDecisionBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
