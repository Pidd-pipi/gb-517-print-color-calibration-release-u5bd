package dto

// CreateReleaseDecision is the public write contract for 放行决定. A draft is
// generated from one 印刷批次 and one 已接收校样, so callers bind the evidence
// records instead of describing a standalone document. Status is deliberately
// omitted so callers cannot bypass the service state machine.
type CreateReleaseDecision struct {
	Code         string `json:"code" binding:"omitempty,min=2,max=64"`
	Description  string `json:"description" binding:"max=1000"`
	PrintRunID   uint   `json:"printRunId" binding:"required"`
	ColorProofID uint   `json:"colorProofId" binding:"required"`
	Evidence     string `json:"evidence" binding:"max=2000"`
}

// UpdateReleaseDecision only allows supplementary evidence on a draft. The
// bound batch/proof and the basis snapshot are immutable.
type UpdateReleaseDecision struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Description     string `json:"description" binding:"max=1000"`
	Evidence        string `json:"evidence" binding:"max=2000"`
}
