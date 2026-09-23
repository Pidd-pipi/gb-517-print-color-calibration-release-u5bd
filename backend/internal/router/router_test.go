package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/config"
	"github.com/blueship581/print-color-calibration-release/backend/internal/database"
	"github.com/blueship581/print-color-calibration-release/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data json.RawMessage `json:"data"`
}

type testRecordRef struct {
	ID      uint   `json:"id"`
	Version uint   `json:"version"`
	Code    string `json:"code"`
}

func TestRBACAndImmutableRevisionFlows(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "operator", "reviewer", "admin"} {
		tokens[role] = loginToken(t, engine, role)
	}

	// A release decision must bind one print run and one accepted color proof.
	runPayload := recordPayload("PR-TEST-001", "测试色彩配置")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "run-create", runPayload)
	run := decodeData[testRecordRef](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	runPath := "/api/runs/" + uintString(run.ID) + "/transition"
	for _, step := range []struct {
		status string
		reqID  string
		reason string
	}{
		{"printing", "run-printing", "plates and ink verified"},
		{"proofing", "run-proofing", "first sheets ready for colour proof"},
	} {
		status, body = perform(t, engine, http.MethodPost, runPath, tokens["operator"], step.reqID,
			map[string]any{"status": step.status, "expectedVersion": run.Version, "reason": step.reason})
		run = decodeData[testRecordRef](t, body)
		if status != http.StatusOK {
			t.Fatalf("run -> %s status = %d body=%s", step.status, status, body)
		}
	}

	proofPayload := recordPayload("CP-TEST-001", "测试校样读数")
	proofPayload["metricValue"] = 1.6
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "proof-create", proofPayload)
	proof := decodeData[testRecordRef](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create proof status = %d body=%s", status, body)
	}
	proofPath := "/api/proofs/" + uintString(proof.ID) + "/transition"
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "proof-review",
		map[string]any{"status": "review", "expectedVersion": proof.Version, "reason": "proof strip submitted for review"})
	if status != http.StatusOK {
		t.Fatalf("proof review status = %d body=%s", status, body)
	}
	proof = decodeData[testRecordRef](t, body)
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "operator-accept",
		map[string]any{"status": "accepted", "expectedVersion": proof.Version, "reason": "operator cannot accept proof"}); status != http.StatusForbidden {
		t.Fatalf("operator accept proof status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "proof-accept",
		map[string]any{"status": "accepted", "expectedVersion": proof.Version, "reason": "colour tolerance independently verified"})
	if status != http.StatusOK {
		t.Fatalf("reviewer accept proof status = %d body=%s", status, body)
	}

	// Drafts can only be generated from a proofing batch with an accepted proof.
	invalidDraft := map[string]any{"code": "RD-TEST-NORUN", "printRunId": run.ID + 9999, "colorProofId": proof.ID}
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "draft-missing-run", invalidDraft); status != http.StatusUnprocessableEntity {
		t.Fatalf("draft with missing run status = %d, want 422", status)
	}
	decisionPayload := map[string]any{"code": "RD-TEST-001", "description": "router integration test", "printRunId": run.ID, "colorProofId": proof.ID}
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["viewer"], "viewer-create", decisionPayload); status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "decision-create", decisionPayload)
	if status != http.StatusCreated {
		t.Fatalf("operator create decision status = %d body=%s", status, body)
	}
	decision := decodeData[struct {
		ID             uint    `json:"id"`
		Version        uint    `json:"version"`
		PrintRunCode   string  `json:"printRunCode"`
		ColorProofNo   string  `json:"colorProofNo"`
		BasisTolerance float64 `json:"basisToleranceLimit"`
		BasisValid     bool    `json:"basisValid"`
	}](t, body)
	if decision.PrintRunCode != run.Code || decision.ColorProofNo != "CP-TEST-001" || decision.BasisTolerance != 3 || !decision.BasisValid {
		t.Fatalf("decision basis snapshot wrong: %+v run=%+v", decision, run)
	}
	transition := map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "quality gate accepted"}
	decisionPath := "/api/release/" + uintString(decision.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, decisionPath, tokens["operator"], "operator-release", transition); status != http.StatusForbidden {
		t.Fatalf("operator release status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, decisionPath, tokens["reviewer"], "reviewer-release", transition)
	if status != http.StatusOK {
		t.Fatalf("reviewer release status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(decision.ID), tokens["reviewer"], "decision-read", nil)
	detail := decodeData[struct {
		Version   uint `json:"version"`
		Revisions []struct {
			Version   uint   `json:"version"`
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if status != http.StatusOK || detail.Version != 2 || len(detail.Revisions) != 2 || detail.Revisions[0].RequestID != "reviewer-release" {
		t.Fatalf("unexpected decision revision chain: status=%d detail=%+v", status, detail)
	}
	// The bound batch must cross to released in the same atomic operation.
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "released-run-read", nil)
	releasedRun := decodeData[struct {
		Status  string `json:"status"`
		Version uint   `json:"version"`
	}](t, body)
	if releasedRun.Status != "released" || releasedRun.Version != run.Version+1 {
		t.Fatalf("batch not released atomically: %+v", releasedRun)
	}
	update := map[string]any{"expectedVersion": detail.Version, "evidence": "不得覆盖的决定"}
	if status, _ := perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "locked-update", update); status != http.StatusConflict {
		t.Fatalf("resolved decision update status = %d, want 409", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/release/"+uintString(decision.ID), tokens["admin"], "locked-delete", nil); status != http.StatusConflict {
		t.Fatalf("resolved decision delete status = %d, want 409", status)
	}

	if status, _ := perform(t, engine, http.MethodDelete, "/api/runs/"+uintString(run.ID), tokens["admin"], "locked-run-delete", nil); status != http.StatusConflict {
		t.Fatalf("active run delete status = %d, want 409", status)
	}

	// A rejected proof must invalidate a draft basis and block the release.
	staleRun, staleProof, staleDecision := setupBasisDecision(t, engine, tokens, "STALE", 2.2)
	status, body = perform(t, engine, http.MethodPost, "/api/proofs/"+uintString(staleProof.ID)+"/transition", tokens["reviewer"], "proof-reject",
		map[string]any{"status": "review", "expectedVersion": staleProof.Version, "reason": "send proof back before rejection"})
	if status != http.StatusOK {
		t.Fatalf("proof back to review status = %d body=%s", status, body)
	}
	proofVersion := decodeData[testRecordRef](t, body).Version
	if status, _ = perform(t, engine, http.MethodPost, "/api/proofs/"+uintString(staleProof.ID)+"/transition", tokens["reviewer"], "proof-rejected",
		map[string]any{"status": "rejected", "expectedVersion": proofVersion, "reason": "colour drift beyond expectation"}); status != http.StatusOK {
		t.Fatalf("proof reject status = %d", status)
	}
	stalePath := "/api/release/" + uintString(staleDecision.ID) + "/transition"
	if status, body = perform(t, engine, http.MethodPost, stalePath, tokens["reviewer"], "stale-release",
		map[string]any{"status": "release", "expectedVersion": staleDecision.Version, "reason": "should be blocked"}); status != http.StatusConflict {
		t.Fatalf("release on rejected proof status = %d body=%s, want 409", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(staleDecision.ID), tokens["reviewer"], "staled-decision-read", nil)
	staleDetail := decodeData[struct {
		Status             string `json:"status"`
		Version            uint   `json:"version"`
		BasisValid         bool   `json:"basisValid"`
		BasisReason        string `json:"basisReason"`
		BasisInvalidReason string `json:"basisInvalidReason"`
		PrintRunID         uint   `json:"printRunId"`
		ColorProofID       uint   `json:"colorProofId"`
	}](t, body)
	if status != http.StatusOK || staleDetail.Status != "draft" || staleDetail.BasisValid ||
		staleDetail.Version != staleDecision.Version+1 ||
		!strings.Contains(staleDetail.BasisReason, "拒绝") || staleDetail.BasisInvalidReason == "" ||
		staleDetail.PrintRunID != staleRun.ID || staleDetail.ColorProofID != staleProof.ID {
		t.Fatalf("basis invalidation not persisted: %+v", staleDetail)
	}
	// The blocked release must leave the batch untouched.
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(staleRun.ID), tokens["reviewer"], "stale-run-read", nil)
	if runAfter := decodeData[struct {
		Status string `json:"status"`
	}](t, body); runAfter.Status != "proofing" {
		t.Fatalf("batch changed after blocked release: %+v", runAfter)
	}

	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["viewer"], "viewer-audit", nil); status != http.StatusForbidden {
		t.Fatalf("viewer audit status = %d, want 403", status)
	}
	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["reviewer"], "reviewer-audit", nil); status != http.StatusOK {
		t.Fatalf("reviewer audit status = %d, want 200", status)
	}
}

// setupBasisDecision creates a proofing run, an accepted proof and a draft
// decision binding both, returning the created records.
func setupBasisDecision(t *testing.T, engine *gin.Engine, tokens map[string]string, suffix string, reading float64) (testRecordRef, testRecordRef, testRecordRef) {
	t.Helper()
	runPayload := recordPayload("PR-TEST-"+suffix, "失效测试批次")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "stale-run-create", runPayload)
	run := decodeData[testRecordRef](t, body)
	if status != http.StatusCreated {
		t.Fatalf("stale run create status = %d body=%s", status, body)
	}
	for _, target := range []string{"printing", "proofing"} {
		status, body = perform(t, engine, http.MethodPost, "/api/runs/"+uintString(run.ID)+"/transition", tokens["operator"], "stale-run-"+target,
			map[string]any{"status": target, "expectedVersion": run.Version, "reason": "advance run for stale basis test"})
		run = decodeData[testRecordRef](t, body)
		if status != http.StatusOK {
			t.Fatalf("stale run -> %s status = %d body=%s", target, status, body)
		}
	}
	proofPayload := recordPayload("CP-TEST-"+suffix, "失效测试校样")
	proofPayload["metricValue"] = reading
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "stale-proof-create", proofPayload)
	proof := decodeData[testRecordRef](t, body)
	if status != http.StatusCreated {
		t.Fatalf("stale proof create status = %d body=%s", status, body)
	}
	for _, step := range []struct {
		target string
		token  string
		reason string
	}{
		{"review", tokens["operator"], "submit proof"},
		{"accepted", tokens["reviewer"], "accept proof"},
	} {
		status, body = perform(t, engine, http.MethodPost, "/api/proofs/"+uintString(proof.ID)+"/transition", step.token, "stale-proof-"+step.target,
			map[string]any{"status": step.target, "expectedVersion": proof.Version, "reason": step.reason})
		proof = decodeData[testRecordRef](t, body)
		if status != http.StatusOK {
			t.Fatalf("stale proof -> %s status = %d body=%s", step.target, status, body)
		}
	}
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "stale-decision-create",
		map[string]any{"printRunId": run.ID, "colorProofId": proof.ID})
	decision := decodeData[testRecordRef](t, body)
	if status != http.StatusCreated {
		t.Fatalf("stale decision create status = %d body=%s", status, body)
	}
	return run, proof, decision
}

func testConfig(dsn string) config.Config {
	return config.Config{
		AppName: "print-color-calibration-release", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: dsn, JWTSecret: "gb517-router-tests-secret",
		TokenTTL: time.Hour, RequestLimit: 1000, StartupTimeout: time.Second,
		ShutdownTimeout: time.Second, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: time.Second, IdleTimeout: time.Second,
	}
}

func loginToken(t *testing.T, engine *gin.Engine, username string) string {
	t.Helper()
	status, body := perform(t, engine, http.MethodPost, "/api/auth/login", "", "login-"+username, map[string]any{"username": username, "password": "Admin123!"})
	if status != http.StatusOK {
		t.Fatalf("login %s status = %d body=%s", username, status, body)
	}
	return decodeData[struct {
		Token string `json:"token"`
	}](t, body).Token
}

func recordPayload(code, name string) map[string]any {
	return map[string]any{
		"code": code, "name": name, "description": "router integration test",
		"facility": "测试印刷区", "owner": "operator", "category": "校准",
		"riskLevel": "medium", "metricValue": 2.1, "metricUnit": "dE",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339), "evidence": "spectrophotometer evidence", "relatedCode": "PR-001",
	}
}

func perform(t *testing.T, engine *gin.Engine, method, path, token, requestID string, payload any) (int, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("X-Request-ID", requestID)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes()
}

func decodeData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	var value T
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		t.Fatalf("decode data %s: %v", envelope.Data, err)
	}
	return value
}

func uintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
