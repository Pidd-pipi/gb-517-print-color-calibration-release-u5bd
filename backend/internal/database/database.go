package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/config"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(ctx context.Context, cfg config.Config, log *slog.Logger) (*gorm.DB, *redis.Client, error) {
	var dialector gorm.Dialector
	switch cfg.DatabaseDriver {
	case "postgres":
		dialector = postgres.Open(cfg.DatabaseDSN)
	case "mysql":
		dialector = mysql.Open(cfg.DatabaseDSN)
	case "sqlite":
		dialector = sqlite.Open(cfg.DatabaseDSN)
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", cfg.DatabaseDriver)
	}
	logLevel := logger.Warn
	if cfg.Environment == "development" {
		logLevel = logger.Info
	}
	var db *gorm.DB
	var err error
	for attempt := 1; attempt <= 20; attempt++ {
		db, err = gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logLevel)})
		if err == nil {
			sqlDB, dbErr := db.DB()
			if dbErr == nil && sqlDB.PingContext(ctx) == nil {
				break
			}
			if dbErr != nil {
				err = dbErr
			} else {
				err = sqlDB.PingContext(ctx)
			}
		}
		log.Warn("database not ready", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("connect database: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, nil, err
	}
	if err := Seed(ctx, db); err != nil {
		return nil, nil, err
	}
	var redisClient *redis.Client
	if cfg.RedisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
		if err := redisClient.Ping(ctx).Err(); err != nil {
			return nil, nil, fmt.Errorf("connect redis: %w", err)
		}
	}
	return db, redisClient, nil
}

func migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{}, &model.AuditLog{},
		&model.PressUnit{},
		&model.PrintRun{}, &model.PrintRunRevision{},
		&model.ColorProof{},
		&model.ReleaseDecision{}, &model.ReleaseDecisionRevision{},
	)
}

func Seed(ctx context.Context, db *gorm.DB) error {
	var users int64
	if err := db.WithContext(ctx).Model(&model.User{}).Count(&users).Error; err != nil {
		return err
	}
	if users == 0 {
		password, err := bcrypt.GenerateFromPassword([]byte("Admin123!"), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		seedUsers := []model.User{
			{Username: "admin", DisplayName: "系统管理员", PasswordHash: string(password), Role: model.RoleAdmin, Active: true},
			{Username: "reviewer", DisplayName: "质量复核员", PasswordHash: string(password), Role: model.RoleReviewer, Active: true},
			{Username: "operator", DisplayName: "现场操作员", PasswordHash: string(password), Role: model.RoleOperator, Active: true},
			{Username: "viewer", DisplayName: "只读观察员", PasswordHash: string(password), Role: model.RoleViewer, Active: true},
		}
		if err := db.WithContext(ctx).Create(&seedUsers).Error; err != nil {
			return err
		}
	}

	if err := seedPressUnit(ctx, db); err != nil {
		return err
	}

	if err := seedPrintRun(ctx, db); err != nil {
		return err
	}

	if err := seedColorProof(ctx, db); err != nil {
		return err
	}

	if err := seedReleaseDecision(ctx, db); err != nil {
		return err
	}

	return nil
}

func seedPressUnit(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.PressUnit{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	items := []model.PressUnit{

		{BaseModel: model.BaseModel{Code: "PU-001", Name: "印刷设备示例一", Status: "ready", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "PU-002", Name: "印刷设备示例二", Status: "setup", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "PU-003", Name: "印刷设备示例三", Status: "printing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: "score",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Create(&items).Error
}

func seedPrintRun(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.PrintRun{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	items := []model.PrintRun{

		{BaseModel: model.BaseModel{Code: "PR-001", Name: "印刷批次示例一", Status: "setup", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01", ToleranceLimit: 3},

		{BaseModel: model.BaseModel{Code: "PR-002", Name: "印刷批次示例二", Status: "printing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02", ToleranceLimit: 3},

		{BaseModel: model.BaseModel{Code: "PR-003", Name: "印刷批次示例三", Status: "proofing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: "score",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03", ToleranceLimit: 3},

		{BaseModel: model.BaseModel{Code: "PR-004", Name: "印刷批次示例四", Status: "released", Version: 2,
			Description: "已随放行决定完成放行的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域4", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 1.6, MetricUnit: "ΔE",
			EffectiveAt: now.Add(9 * time.Hour), Evidence: "放行前校样读数已留档", RelatedCode: "REL-517-04", ToleranceLimit: 3},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.PrintRunRevision, 0, len(items)+1)
		for _, item := range items {
			version := item.Version
			statuses := []string{item.Status}
			if item.Code == "PR-004" {
				// Released seed run keeps its full proofing -> released chain.
				statuses = []string{"proofing", "released"}
			}
			for idx, status := range statuses {
				revVersion := uint(idx + 1)
				if item.Code != "PR-004" {
					revVersion = version
				}
				reason := "initial colour configuration"
				if item.Code == "PR-004" && status == "released" {
					reason = "放行决定 RD-002 复核放行"
				}
				revisions = append(revisions, model.PrintRunRevision{
					PrintRunID: item.ID, Version: revVersion, Status: status, Name: item.Name,
					Facility: item.Facility, Owner: item.Owner, Category: item.Category,
					RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
					Evidence: item.Evidence, RelatedCode: item.RelatedCode, ToleranceLimit: item.ToleranceLimit,
					Actor: "seed", RequestID: "startup-seed", Reason: reason,
				})
			}
		}
		return tx.Create(&revisions).Error
	})
}

func seedColorProof(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.ColorProof{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	items := []model.ColorProof{

		{BaseModel: model.BaseModel{Code: "CP-001", Name: "色彩校样示例一", Status: "captured", Version: 1,
			Description: "用于启动验证和主要流程演示的色彩校样记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 1.2, MetricUnit: "ΔE",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "分光光度计首次采样读数", RelatedCode: "PR-001"},

		{BaseModel: model.BaseModel{Code: "CP-002", Name: "色彩校样示例二", Status: "review", Version: 1,
			Description: "已提交复核等待接收的色彩校样记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 2.1, MetricUnit: "ΔE",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "第二次抽样读数，等待复核接收", RelatedCode: "PR-002"},

		{BaseModel: model.BaseModel{Code: "CP-003", Name: "色彩校样示例三", Status: "accepted", Version: 1,
			Description: "已被复核员接收、可作为放行依据的校样记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 1.8, MetricUnit: "ΔE",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "校样阶段读数已接收，ΔE 在允许范围内", RelatedCode: "PR-003"},

		{BaseModel: model.BaseModel{Code: "CP-004", Name: "色彩校样示例四", Status: "accepted", Version: 1,
			Description: "随已放行批次归档的已接收校样记录"}, Facility: "印刷色彩批次校准放行区域4", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 1.6, MetricUnit: "ΔE",
			EffectiveAt: now.Add(9 * time.Hour), Evidence: "放行复核时使用的接收读数", RelatedCode: "PR-004"},
	}
	return db.WithContext(ctx).Create(&items).Error
}

func seedReleaseDecision(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.ReleaseDecision{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	type seedDecision struct {
		code, name, status   string
		version              uint
		runCode, proofNo     string
		basisRun, basisProof uint
		reading, limit       float64
		basisInvalid         string
		reason               string
	}
	specs := []seedDecision{
		{
			code: "RD-001", name: "放行决定示例一", status: "draft", version: 1,
			runCode: "PR-003", proofNo: "CP-003", basisRun: 1, basisProof: 1,
			reading: 1.8, limit: 3, reason: "initial release decision draft",
		},
		{
			code: "RD-002", name: "放行决定示例二", status: "release", version: 2,
			runCode: "PR-004", proofNo: "CP-004", basisRun: 1, basisProof: 1,
			reading: 1.6, limit: 3, reason: "reviewer release",
		},
		{
			code: "RD-003", name: "放行决定示例三", status: "rework", version: 1,
			runCode: "PR-002", proofNo: "CP-002", basisRun: 1, basisProof: 1,
			reading: 2.1, limit: 3, basisInvalid: "批次已不在校样阶段", reason: "initial release decision sent to rework",
		},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var runs []model.PrintRun
		if err := tx.Find(&runs).Error; err != nil {
			return err
		}
		runByCode := make(map[string]model.PrintRun, len(runs))
		for _, run := range runs {
			runByCode[run.Code] = run
		}
		var proofs []model.ColorProof
		if err := tx.Find(&proofs).Error; err != nil {
			return err
		}
		proofByCode := make(map[string]model.ColorProof, len(proofs))
		for _, proof := range proofs {
			proofByCode[proof.Code] = proof
		}
		items := make([]model.ReleaseDecision, 0, len(specs))
		for _, spec := range specs {
			run := runByCode[spec.runCode]
			proof := proofByCode[spec.proofNo]
			items = append(items, model.ReleaseDecision{
				BaseModel: model.BaseModel{Code: spec.code, Name: spec.name, Status: spec.status, Version: spec.version,
					Description: "用于启动验证和主要流程演示的放行决定记录"},
				Facility: run.Facility, Owner: "质量复核组",
				Category: run.Category, RiskLevel: run.RiskLevel,
				MetricValue: spec.reading, MetricUnit: "ΔE",
				EffectiveAt: now, Evidence: proof.Evidence, RelatedCode: spec.runCode,
				PrintRunID: run.ID, ColorProofID: proof.ID,
				PrintRunCode: spec.runCode, ColorProofNo: spec.proofNo,
				BasisRunVersion: spec.basisRun, BasisProofVersion: spec.basisProof,
				BasisProofReading: spec.reading, BasisToleranceLimit: spec.limit,
				BasisInvalidReason: spec.basisInvalid,
			})
		}
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.ReleaseDecisionRevision, 0, len(items)+1)
		for i, item := range items {
			initialStatus := item.Status
			if item.Status == "release" {
				initialStatus = "draft"
			}
			revisions = append(revisions, model.ReleaseDecisionRevision{
				ReleaseDecisionID: item.ID, Version: 1,
				Status: initialStatus,
				Name:   item.Name, RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
				Evidence: item.Evidence, RelatedCode: item.RelatedCode,
				PrintRunID: item.PrintRunID, ColorProofID: item.ColorProofID,
				PrintRunCode: item.PrintRunCode, ColorProofNo: item.ColorProofNo,
				BasisRunVersion: item.BasisRunVersion, BasisProofVersion: item.BasisProofVersion,
				BasisProofReading: item.BasisProofReading, BasisToleranceLimit: item.BasisToleranceLimit,
				Actor: "seed", RequestID: "startup-seed", Reason: "initial release decision draft",
			})
			if item.Status == "release" {
				revisions = append(revisions, model.ReleaseDecisionRevision{
					ReleaseDecisionID: item.ID, Version: 2, Status: "release",
					Name: item.Name, RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
					Evidence: item.Evidence, RelatedCode: item.RelatedCode,
					PrintRunID: item.PrintRunID, ColorProofID: item.ColorProofID,
					PrintRunCode: item.PrintRunCode, ColorProofNo: item.ColorProofNo,
					BasisRunVersion: item.BasisRunVersion, BasisProofVersion: item.BasisProofVersion,
					BasisProofReading: item.BasisProofReading, BasisToleranceLimit: item.BasisToleranceLimit,
					Actor: "reviewer", RequestID: "startup-seed", Reason: specs[i].reason,
				})
			}
		}
		return tx.Create(&revisions).Error
	})
}
