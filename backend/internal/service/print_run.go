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

type PrintRunService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.PrintRun], error)
	Get(context.Context, uint) (model.PrintRun, error)
	Create(context.Context, dto.CreatePrintRun, string, string) (model.PrintRun, error)
	Update(context.Context, uint, dto.UpdatePrintRun, string, string) (model.PrintRun, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.PrintRun, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type printRunService struct {
	repository repository.PrintRunRepository
	decisions  repository.ReleaseDecisionRepository
	unitOfWork repository.UnitOfWork
	security   SecurityService
}

func NewPrintRunService(repo repository.PrintRunRepository, decisions repository.ReleaseDecisionRepository, unitOfWork repository.UnitOfWork, security SecurityService) PrintRunService {
	return &printRunService{repository: repo, decisions: decisions, unitOfWork: unitOfWork, security: security}
}

func (s *printRunService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.PrintRun], error) {
	return s.repository.List(ctx, query)
}

func (s *printRunService) Get(ctx context.Context, id uint) (model.PrintRun, error) {
	return s.repository.Get(ctx, id)
}

func (s *printRunService) Create(ctx context.Context, input dto.CreatePrintRun, actor, requestID string) (model.PrintRun, error) {
	if err := validatePrintRunBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.PrintRun{}, err
	}
	if input.AllowedMax != 0 && input.AllowedMin > input.AllowedMax {
		return model.PrintRun{}, ErrInvalidInput
	}
	item := model.PrintRun{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.PrintRunInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		AllowedMin: input.AllowedMin, AllowedMax: input.AllowedMax,
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.CreateVersioned(ctx, &item, actor, requestID, "created colour configuration"); err != nil {
		return model.PrintRun{}, fmt.Errorf("create 印刷批次: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "PrintRun", item.ID, "", item.Status, "created 印刷批次")
	return item, nil
}

func (s *printRunService) Update(ctx context.Context, id uint, input dto.UpdatePrintRun, actor, requestID string) (model.PrintRun, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PrintRun{}, err
	}
	if err := validatePrintRunBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.PrintRun{}, err
	}
	if input.AllowedMax != 0 && input.AllowedMin > input.AllowedMax {
		return model.PrintRun{}, ErrInvalidInput
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.AllowedMin = input.AllowedMin
	current.AllowedMax = input.AllowedMax
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	reason := "updated colour configuration"
	if err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		if err := deps.ReleaseDecisions.LockDraftBasisByRun(ctx, id); err != nil {
			return err
		}
		if _, err := deps.ReleaseDecisions.MarkDraftBasisInvalidByRun(ctx, id, current.Code, "批次配置已换版", actor, requestID); err != nil {
			return fmt.Errorf("invalidate release basis after run update: %w", err)
		}
		if err := deps.UpdatePrintRunVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, reason); err != nil {
			return fmt.Errorf("update 印刷批次: %w", err)
		}
		return deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "update", EntityType: "PrintRun",
			EntityID: id, BeforeState: current.Status, AfterState: current.Status, Detail: "updated business fields",
			CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		return model.PrintRun{}, err
	}
	return s.repository.Get(ctx, id)
}

func (s *printRunService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.PrintRun, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PrintRun{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == string(constants.RunStateReleased) || current.Status == string(constants.RunStateReleased)) && !canReview(role) {
		return model.PrintRun{}, ErrForbidden
	}
	if !constants.CanTransition(constants.PrintRunTransitions, current.Status, target) {
		return model.PrintRun{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		if err := deps.ReleaseDecisions.LockDraftBasisByRun(ctx, id); err != nil {
			return err
		}
		invalidReason := "批次配置已换版"
		if before == string(constants.RunStateProofing) && target != string(constants.RunStateProofing) {
			invalidReason = "关联批次已离开校样阶段"
		}
		if _, err := deps.ReleaseDecisions.MarkDraftBasisInvalidByRun(ctx, id, current.Code, invalidReason, actor, requestID); err != nil {
			return fmt.Errorf("invalidate release basis after run transition: %w", err)
		}
		if err := deps.UpdatePrintRunVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, input.Reason); err != nil {
			return fmt.Errorf("transition 印刷批次: %w", err)
		}
		return deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "transition", EntityType: "PrintRun",
			EntityID: id, BeforeState: before, AfterState: target, Detail: input.Reason,
			CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		return model.PrintRun{}, err
	}
	return s.repository.Get(ctx, id)
}

func canReview(role string) bool { return role == model.RoleReviewer || role == model.RoleAdmin }

func (s *printRunService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.PrintRunInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "PrintRun", id, current.Status, "deleted", "soft deleted 印刷批次")
}

func (s *printRunService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validatePrintRunBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
