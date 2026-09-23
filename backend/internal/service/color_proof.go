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

type ColorProofService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ColorProof], error)
	Get(context.Context, uint) (model.ColorProof, error)
	Create(context.Context, dto.CreateColorProof, string, string) (model.ColorProof, error)
	Update(context.Context, uint, dto.UpdateColorProof, string, string) (model.ColorProof, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ColorProof, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type colorProofService struct {
	repository repository.ColorProofRepository
	decisions  repository.ReleaseDecisionRepository
	unitOfWork repository.UnitOfWork
	security   SecurityService
}

func NewColorProofService(repo repository.ColorProofRepository, decisions repository.ReleaseDecisionRepository, unitOfWork repository.UnitOfWork, security SecurityService) ColorProofService {
	return &colorProofService{repository: repo, decisions: decisions, unitOfWork: unitOfWork, security: security}
}

func (s *colorProofService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ColorProof], error) {
	return s.repository.List(ctx, query)
}

func (s *colorProofService) Get(ctx context.Context, id uint) (model.ColorProof, error) {
	return s.repository.Get(ctx, id)
}

func (s *colorProofService) Create(ctx context.Context, input dto.CreateColorProof, actor, requestID string) (model.ColorProof, error) {
	if err := validateColorProofBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ColorProof{}, err
	}
	item := model.ColorProof{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ColorProofInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.Create(ctx, &item); err != nil {
		return model.ColorProof{}, fmt.Errorf("create 色彩校样: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ColorProof", item.ID, "", item.Status, "created 色彩校样")
	return item, nil
}

func (s *colorProofService) Update(ctx context.Context, id uint, input dto.UpdateColorProof, actor, requestID string) (model.ColorProof, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ColorProof{}, err
	}
	if err := validateColorProofBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ColorProof{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		if err := deps.ReleaseDecisions.LockDraftBasisByProof(ctx, id); err != nil {
			return err
		}
		if _, err := deps.ReleaseDecisions.MarkDraftBasisInvalidByProof(ctx, id, current.Code, "关联校样出现更新读数", actor, requestID); err != nil {
			return fmt.Errorf("invalidate release basis after proof update: %w", err)
		}
		if err := deps.UpdateColorProof(ctx, id, input.ExpectedVersion, &current); err != nil {
			return fmt.Errorf("update 色彩校样: %w", err)
		}
		return deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "update", EntityType: "ColorProof",
			EntityID: id, BeforeState: current.Status, AfterState: current.Status, Detail: "updated business fields",
			CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		return model.ColorProof{}, err
	}
	return s.repository.Get(ctx, id)
}

func (s *colorProofService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ColorProof, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ColorProof{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == "accepted" || target == "rejected" || current.Status == "accepted" || current.Status == "rejected") && !canReview(role) {
		return model.ColorProof{}, ErrForbidden
	}
	if !constants.CanTransition(constants.ColorProofTransitions, current.Status, target) {
		return model.ColorProof{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	invalidReason := "关联校样状态已变化"
	if target == "rejected" {
		invalidReason = "关联校样已被拒绝"
	} else if before == "accepted" && target != "accepted" {
		invalidReason = "关联校样不再是已接收状态"
	}
	if err := s.unitOfWork.Within(ctx, func(deps repository.Dependencies) error {
		if err := deps.ReleaseDecisions.LockDraftBasisByProof(ctx, id); err != nil {
			return err
		}
		if _, err := deps.ReleaseDecisions.MarkDraftBasisInvalidByProof(ctx, id, current.Code, invalidReason, actor, requestID); err != nil {
			return fmt.Errorf("invalidate release basis after proof transition: %w", err)
		}
		if err := deps.UpdateColorProof(ctx, id, input.ExpectedVersion, &current); err != nil {
			return fmt.Errorf("transition 色彩校样: %w", err)
		}
		return deps.Security.AppendAudit(ctx, &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "transition", EntityType: "ColorProof",
			EntityID: id, BeforeState: before, AfterState: target, Detail: input.Reason,
			CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		return model.ColorProof{}, err
	}
	return s.repository.Get(ctx, id)
}

func (s *colorProofService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ColorProof", id, current.Status, "deleted", "soft deleted 色彩校样")
}

func (s *colorProofService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateColorProofBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
