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
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit", AllowedMin: 0, AllowedMax: 3,
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "PR-002", Name: "印刷批次示例二", Status: "printing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%", AllowedMin: 0, AllowedMax: 3,
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "PR-003", Name: "印刷批次示例三", Status: "proofing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 1.6, MetricUnit: "dE", AllowedMin: 0, AllowedMax: 3,
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.PrintRunRevision, 0, len(items))
		for _, item := range items {
			revisions = append(revisions, model.PrintRunRevision{
				PrintRunID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
				Facility: item.Facility, Owner: item.Owner, Category: item.Category,
				RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
				AllowedMin: item.AllowedMin, AllowedMax: item.AllowedMax,
				Evidence: item.Evidence, RelatedCode: item.RelatedCode,
				Actor: "seed", RequestID: "startup-seed", Reason: "initial colour configuration",
			})
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
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "CP-002", Name: "色彩校样示例二", Status: "review", Version: 1,
			Description: "用于启动验证和主要流程演示的色彩校样记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "CP-003", Name: "色彩校样示例三", Status: "accepted", Version: 1,
			Description: "用于启动验证和主要流程演示的色彩校样记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 1.6, MetricUnit: "dE",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Create(&items).Error
}

func seedReleaseDecision(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.ReleaseDecision{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	type basisRef struct {
		Code        string
		RelatedCode string
		ID          uint
		Version     uint
	}
	var runs []basisRef
	if err := db.WithContext(ctx).Model(&model.PrintRun{}).Select("id, code, related_code, version").Scan(&runs).Error; err != nil {
		return err
	}
	var proofs []basisRef
	if err := db.WithContext(ctx).Model(&model.ColorProof{}).Select("id, code, related_code, version").Scan(&proofs).Error; err != nil {
		return err
	}
	runByRelated := make(map[string]basisRef, len(runs))
	for _, run := range runs {
		runByRelated[run.RelatedCode] = run
	}
	proofByRelated := make(map[string]basisRef, len(proofs))
	for _, proof := range proofs {
		proofByRelated[proof.RelatedCode] = proof
	}
	items := []model.ReleaseDecision{

		{BaseModel: model.BaseModel{Code: "RD-001", Name: "放行决定示例一", Status: "draft", Version: 1,
			Description: "用于启动验证和主要流程演示的放行决定记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "RD-002", Name: "放行决定示例二", Status: "release", Version: 1,
			Description: "用于启动验证和主要流程演示的放行决定记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "RD-003", Name: "放行决定示例三", Status: "rework", Version: 1,
			Description: "用于启动验证和主要流程演示的放行决定记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: "score",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range items {
			run := runByRelated[items[i].RelatedCode]
			proof := proofByRelated[items[i].RelatedCode]
			items[i].PrintRunID = run.ID
			items[i].PrintRunCode = run.Code
			items[i].PrintRunVersion = run.Version
			items[i].ColorProofID = proof.ID
			items[i].ColorProofCode = proof.Code
			items[i].ColorProofVersion = proof.Version
			items[i].BasisValid = false
			items[i].InvalidReason = "启动示例数据，不代表当前有效依据"
		}
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.ReleaseDecisionRevision, 0, len(items))
		for _, item := range items {
			revision := model.ReleaseDecisionRevision{
				ReleaseDecisionID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
				RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
				Evidence: item.Evidence, RelatedCode: item.RelatedCode,
				PrintRunID: item.PrintRunID, PrintRunCode: item.PrintRunCode, PrintRunVersion: item.PrintRunVersion,
				ColorProofID: item.ColorProofID, ColorProofCode: item.ColorProofCode, ColorProofVersion: item.ColorProofVersion,
				BasisValid: item.BasisValid, InvalidReason: item.InvalidReason,
				Actor: "seed", RequestID: "startup-seed", Reason: "initial release decision",
			}
			revisions = append(revisions, revision)
		}
		return tx.Create(&revisions).Error
	})
}
