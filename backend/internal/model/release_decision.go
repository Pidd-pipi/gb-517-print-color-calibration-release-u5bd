package model

import "time"

// ReleaseDecision models 放行决定. A decision is no longer a standalone note:
// every decision binds one PrintRun and one accepted ColorProof. The basis
// snapshots freeze the configuration version, proof reading and allowed
// reading range captured when the draft was generated, so the reviewer can
// always tell which evidence the decision was based on.
type ReleaseDecision struct {
	BaseModel
	Facility    string    `json:"facility" gorm:"size:120;index"`
	Owner       string    `json:"owner" gorm:"size:120;index"`
	Category    string    `json:"category" gorm:"size:80;index"`
	RiskLevel   string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue float64   `json:"metricValue"`
	MetricUnit  string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt time.Time `json:"effectiveAt"`
	Evidence    string    `json:"evidence" gorm:"size:2000"`
	RelatedCode string    `json:"relatedCode" gorm:"size:64;index"`
	// Bound evidence records. The IDs are immutable once the draft exists.
	PrintRunID   uint   `json:"printRunId" gorm:"index;not null"`
	ColorProofID uint   `json:"colorProofId" gorm:"index;not null"`
	PrintRunCode string `json:"printRunCode" gorm:"size:64;index"`
	ColorProofNo string `json:"colorProofNo" gorm:"size:64;index"`
	// Basis snapshot captured at draft creation.
	BasisRunVersion     uint    `json:"basisRunVersion"`
	BasisProofVersion   uint    `json:"basisProofVersion"`
	BasisProofReading   float64 `json:"basisProofReading"`
	BasisToleranceLimit float64 `json:"basisToleranceLimit"`
	// BasisInvalidReason is empty while the bound evidence still matches the
	// snapshot; it is persisted atomically when the basis goes stale.
	BasisInvalidReason string                    `json:"basisInvalidReason" gorm:"size:500"`
	Revisions          []ReleaseDecisionRevision `json:"revisions,omitempty" gorm:"foreignKey:ReleaseDecisionID"`

	// Computed on read by re-checking the bound records; never persisted.
	BasisValid  bool   `json:"basisValid" gorm:"-"`
	BasisReason string `json:"basisReason" gorm:"-"`
}

func (item *ReleaseDecision) GetBase() *BaseModel { return &item.BaseModel }

func (item ReleaseDecision) TableName() string { return "release_decisions" }

var ReleaseDecisionInitialStatus = "draft"

// ReleaseDecisionRevision preserves every decision and its evidence as an
// immutable approval record, including who made it and which request did so.
type ReleaseDecisionRevision struct {
	ID                  uint      `json:"id" gorm:"primaryKey"`
	ReleaseDecisionID   uint      `json:"releaseDecisionId" gorm:"not null;uniqueIndex:idx_release_decision_revision"`
	Version             uint      `json:"version" gorm:"not null;uniqueIndex:idx_release_decision_revision"`
	Status              string    `json:"status" gorm:"size:40;not null"`
	Name                string    `json:"name" gorm:"size:160;not null"`
	RiskLevel           string    `json:"riskLevel" gorm:"size:32"`
	MetricValue         float64   `json:"metricValue"`
	MetricUnit          string    `json:"metricUnit" gorm:"size:24"`
	Evidence            string    `json:"evidence" gorm:"size:2000"`
	RelatedCode         string    `json:"relatedCode" gorm:"size:64"`
	PrintRunID          uint      `json:"printRunId"`
	ColorProofID        uint      `json:"colorProofId"`
	PrintRunCode        string    `json:"printRunCode" gorm:"size:64"`
	ColorProofNo        string    `json:"colorProofNo" gorm:"size:64"`
	BasisRunVersion     uint      `json:"basisRunVersion"`
	BasisProofVersion   uint      `json:"basisProofVersion"`
	BasisProofReading   float64   `json:"basisProofReading"`
	BasisToleranceLimit float64   `json:"basisToleranceLimit"`
	BasisInvalidReason  string    `json:"basisInvalidReason" gorm:"size:500"`
	Actor               string    `json:"actor" gorm:"size:80;not null"`
	RequestID           string    `json:"requestId" gorm:"size:80;not null"`
	Reason              string    `json:"reason" gorm:"size:500;not null"`
	CreatedAt           time.Time `json:"createdAt"`
}
