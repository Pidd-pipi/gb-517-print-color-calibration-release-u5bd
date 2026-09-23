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

	runPayload := recordPayload("PR-TEST-001", "测试色彩配置")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "run-create", runPayload)
	run := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	runPath := "/api/runs/" + uintString(run.ID) + "/transition"
	status, _ = perform(t, engine, http.MethodPost, runPath, tokens["operator"], "run-printing", map[string]any{"status": "printing", "expectedVersion": run.Version, "reason": "plates and ink verified"})
	if status != http.StatusOK {
		t.Fatalf("run transition status = %d", status)
	}
	status, _ = perform(t, engine, http.MethodPost, runPath, tokens["operator"], "run-proofing", map[string]any{"status": "proofing", "expectedVersion": 2, "reason": "proof ready"})
	if status != http.StatusOK {
		t.Fatalf("run proofing status = %d", status)
	}

	proofPayload := recordPayload("CP-TEST-001", "测试校样")
	proofPayload["metricValue"] = 1.8
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "proof-create", proofPayload)
	proof := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create proof status = %d body=%s", status, body)
	}
	proofPath := "/api/proofs/" + uintString(proof.ID) + "/transition"
	status, _ = perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "proof-review", map[string]any{"status": "review", "expectedVersion": proof.Version, "reason": "ready for review"})
	if status != http.StatusOK {
		t.Fatalf("proof review status = %d", status)
	}
	status, _ = perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "proof-accept", map[string]any{"status": "accepted", "expectedVersion": 2, "reason": "reading accepted"})
	if status != http.StatusOK {
		t.Fatalf("proof accept status = %d", status)
	}

	payload := recordPayload("RD-TEST-001", "测试放行决定")
	payload["printRunId"] = run.ID
	payload["colorProofId"] = proof.ID
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["viewer"], "viewer-create", payload); status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "decision-create", payload)
	if status != http.StatusCreated {
		t.Fatalf("operator create decision status = %d body=%s", status, body)
	}
	decision := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	transition := map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "quality gate accepted"}
	path := "/api/release/" + uintString(decision.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, path, tokens["operator"], "operator-release", transition); status != http.StatusForbidden {
		t.Fatalf("operator release status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, path, tokens["reviewer"], "reviewer-release", transition)
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
	update := recordPayload("ignored", "不得覆盖的决定")
	update["expectedVersion"] = detail.Version
	if status, _ := perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "locked-update", update); status != http.StatusConflict {
		t.Fatalf("resolved decision update status = %d, want 409", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/release/"+uintString(decision.ID), tokens["admin"], "locked-delete", nil); status != http.StatusConflict {
		t.Fatalf("resolved decision delete status = %d, want 409", status)
	}

	if status, _ := perform(t, engine, http.MethodDelete, "/api/runs/"+uintString(run.ID), tokens["admin"], "locked-run-delete", nil); status != http.StatusConflict {
		t.Fatalf("active run delete status = %d, want 409", status)
	}
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["operator"], "run-read", nil)
	runDetail := decodeData[struct {
		Revisions []struct {
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if len(runDetail.Revisions) != 3 || runDetail.Revisions[0].RequestID != "run-proofing" {
		t.Fatalf("unexpected colour configuration revisions: %+v", runDetail.Revisions)
	}

	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["viewer"], "viewer-audit", nil); status != http.StatusForbidden {
		t.Fatalf("viewer audit status = %d, want 403", status)
	}
	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["reviewer"], "reviewer-audit", nil); status != http.StatusOK {
		t.Fatalf("reviewer audit status = %d, want 200", status)
	}
}

func TestReleaseBasisDraftRules(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-basis-draft.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	operatorToken := loginToken(t, engine, "operator")

	status, body := perform(t, engine, http.MethodGet, "/api/runs?search=PR-003", operatorToken, "draft-find-run", nil)
	runPage := decodeData[[]struct{ ID uint }](t, body)
	if status != http.StatusOK || len(runPage) != 1 {
		t.Fatalf("find proofing run status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/proofs?search=CP-003", operatorToken, "draft-find-proof", nil)
	proofPage := decodeData[[]struct{ ID uint }](t, body)
	if status != http.StatusOK || len(proofPage) != 1 {
		t.Fatalf("find accepted proof status = %d body=%s", status, body)
	}
	validPayload := recordPayload("RD-VALID-BASIS", "合格依据草稿")
	validPayload["printRunId"] = runPage[0].ID
	validPayload["colorProofId"] = proofPage[0].ID
	if status, body := perform(t, engine, http.MethodPost, "/api/release", operatorToken, "draft-valid-create", validPayload); status != http.StatusCreated {
		t.Fatalf("valid basis draft status = %d body=%s", status, body)
	}

	status, body = perform(t, engine, http.MethodGet, "/api/runs?search=PR-001", operatorToken, "draft-find-setup-run", nil)
	setupRunPage := decodeData[[]struct{ ID uint }](t, body)
	if status != http.StatusOK || len(setupRunPage) != 1 {
		t.Fatalf("find setup run status = %d body=%s", status, body)
	}
	invalidPayload := recordPayload("RD-INVALID-BASIS", "非校样阶段草稿")
	invalidPayload["printRunId"] = setupRunPage[0].ID
	invalidPayload["colorProofId"] = proofPage[0].ID
	if status, body := perform(t, engine, http.MethodPost, "/api/release", operatorToken, "draft-invalid-create", invalidPayload); status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("校样阶段")) {
		t.Fatalf("invalid basis draft status = %d body=%s", status, body)
	}
}

func TestReleaseBasisInvalidation(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-basis.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"operator", "reviewer"} {
		tokens[role] = loginToken(t, engine, role)
	}

	runPayload := recordPayload("PR-BASIS-001", "失效依据批次")
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "basis-run-create", runPayload)
	run := decodeData[struct{ ID, Version uint }](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	runPath := "/api/runs/" + uintString(run.ID) + "/transition"
	for _, step := range []struct {
		requestID, status string
		version           uint
	}{
		{"basis-run-printing", "printing", 1}, {"basis-run-proofing", "proofing", 2},
	} {
		if status, _ := perform(t, engine, http.MethodPost, runPath, tokens["operator"], step.requestID,
			map[string]any{"status": step.status, "expectedVersion": step.version, "reason": step.requestID}); status != http.StatusOK {
			t.Fatalf("%s status = %d", step.requestID, status)
		}
	}

	proofPayload := recordPayload("CP-BASIS-001", "失效依据校样")
	proofPayload["metricValue"] = 1.2
	status, body = perform(t, engine, http.MethodPost, "/api/proofs", tokens["operator"], "basis-proof-create", proofPayload)
	proof := decodeData[struct{ ID, Version uint }](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create proof status = %d body=%s", status, body)
	}
	proofPath := "/api/proofs/" + uintString(proof.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["operator"], "basis-proof-review",
		map[string]any{"status": "review", "expectedVersion": 1, "reason": "review"}); status != http.StatusOK {
		t.Fatalf("proof review status = %d", status)
	}
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "basis-proof-accept",
		map[string]any{"status": "accepted", "expectedVersion": 2, "reason": "accept"}); status != http.StatusOK {
		t.Fatalf("proof accept status = %d", status)
	}

	decisionPayload := recordPayload("RD-BASIS-001", "失效依据决定")
	decisionPayload["printRunId"] = run.ID
	decisionPayload["colorProofId"] = proof.ID
	status, body = perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "basis-decision-create", decisionPayload)
	decision := decodeData[struct{ ID, Version uint }](t, body)
	if status != http.StatusCreated || decision.Version != 1 {
		t.Fatalf("create decision status = %d version=%d body=%s", status, decision.Version, body)
	}

	runUpdate := recordPayload("ignored", "失效依据批次")
	runUpdate["expectedVersion"] = 3
	runUpdate["allowedMax"] = 4
	if status, _ := perform(t, engine, http.MethodPut, "/api/runs/"+uintString(run.ID), tokens["operator"], "basis-run-config-change", runUpdate); status != http.StatusOK {
		t.Fatalf("run config change status = %d", status)
	}

	rejectProof := map[string]any{"status": "review", "expectedVersion": 3, "reason": "new reading needs review"}
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "basis-proof-unaccept", rejectProof); status != http.StatusOK {
		t.Fatalf("proof back to review status = %d", status)
	}
	if status, _ := perform(t, engine, http.MethodPost, proofPath, tokens["reviewer"], "basis-proof-reject",
		map[string]any{"status": "rejected", "expectedVersion": 4, "reason": "reject after reread"}); status != http.StatusOK {
		t.Fatalf("proof reject status = %d", status)
	}

	if status, body := perform(t, engine, http.MethodPost, "/api/release/"+uintString(decision.ID)+"/transition", tokens["reviewer"], "basis-blocked-release",
		map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "attempt release"}); status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("关联校样")) {
		t.Fatalf("blocked release status = %d body=%s", status, body)
	}
	_, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(decision.ID), tokens["reviewer"], "basis-decision-read", nil)
	detail := decodeData[struct {
		Version        uint   `json:"version"`
		BasisValid     bool   `json:"basisValid"`
		InvalidReason  string `json:"invalidReason"`
		PrintRunCode   string `json:"printRunCode"`
		ColorProofCode string `json:"colorProofCode"`
	}](t, body)
	if detail.Version != 4 || detail.BasisValid || !strings.Contains(detail.InvalidReason, "拒绝") || !strings.Contains(detail.InvalidReason, "换版") || detail.PrintRunCode != "PR-BASIS-001" || detail.ColorProofCode != "CP-BASIS-001" {
		t.Fatalf("decision basis was not invalidated atomically: %+v", detail)
	}
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
		"riskLevel": "medium", "metricValue": 2.1, "metricUnit": "dE", "allowedMin": 0, "allowedMax": 3,
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
