package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/config"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/database"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	Meta  struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

type record struct {
	ID      uint   `json:"id"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Version uint   `json:"version"`
}

func TestRBACLinkedComplianceWorkflowAndAuditing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if redisClient != nil {
		t.Fatal("test must use in-memory limiter without Redis")
	}
	engine := router.New(cfg, db, nil, logger)

	viewer := login(t, engine, "viewer")
	operator := login(t, engine, "operator")
	reviewer := login(t, engine, "reviewer")

	response, _ := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=10", viewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	response, _ = request(t, engine, http.MethodPost, "/api/manifests", viewer, "viewer-write", manifestPayload("TM-VIEWER", "CP-002"))
	assertStatus(t, response, http.StatusForbidden)
	response, _ = request(t, engine, http.MethodGet, "/api/audits", viewer, "", nil)
	assertStatus(t, response, http.StatusForbidden)

	response, body := request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-valid", manifestPayload("TM-ROUTER-001", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	manifest := decodeRecord(t, body)
	if manifest.Status != "draft" || response.Header.Get("X-Request-ID") != "manifest-create-valid" {
		t.Fatalf("unexpected manifest response: %+v request-id=%q", manifest, response.Header.Get("X-Request-ID"))
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-skip", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "must not skip submission",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-submit", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "submitted" || manifest.Version != 2 {
		t.Fatalf("manifest transition was not persisted: %+v", manifest)
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-stale", map[string]any{
		"status": "in_transit", "expectedVersion": uint(1), "reason": "stale client must conflict",
	})
	assertStatus(t, response, http.StatusConflict)

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-unverified", manifestPayload("TM-ROUTER-002", "CP-001"))
	assertStatus(t, response, http.StatusCreated)
	unverified := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", unverified.ID), operator, "manifest-block-unverified", map[string]any{
		"status": "submitted", "expectedVersion": unverified.Version, "reason": "must verify carrier first",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-rejected", manifestPayload("TM-ROUTER-003", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	rejected := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-submit-rejected", map[string]any{
		"status": "submitted", "expectedVersion": rejected.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	rejected = decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-reject", map[string]any{
		"status": "rejected", "expectedVersion": rejected.Version, "reason": "destination permit mismatch",
	})
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create-rejected", checkPayload("CC-ROUTER-002", rejected.Code))
	assertStatus(t, response, http.StatusCreated)
	rejectedCheck := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", rejectedCheck.ID), reviewer, "rejected-manifest-pass", map[string]any{
		"status": "pass", "expectedVersion": rejectedCheck.Version, "reason": "a rejected manifest must not pass",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create", checkPayload("CC-ROUTER-001", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	check := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), operator, "operator-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "operator must not decide",
	})
	assertStatus(t, response, http.StatusForbidden)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), reviewer, "reviewer-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "all four evidence groups verified",
	})
	assertStatus(t, response, http.StatusOK)
	check = decodeRecord(t, body)
	if check.Status != "pass" || check.Version != 2 {
		t.Fatalf("review decision was not persisted: %+v", check)
	}

	response, body = request(t, engine, http.MethodGet, "/api/audits?page=1&pageSize=100", reviewer, "audit-read", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("manifest-submit")) || !bytes.Contains(body, []byte("reviewer-decision")) {
		t.Fatalf("expected request IDs in immutable audit list: %s", string(body))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Meta.Total < 5 {
		t.Fatalf("expected audited mutations, got total=%d error=%v", envelope.Meta.Total, err)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppName: "hazardous-waste-transfer-compliance-test", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: "file:router-test?mode=memory&cache=shared",
		JWTSecret: "router-test-secret-at-least-32-characters", TokenTTL: time.Hour,
		RequestLimit: 10000, StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
	}
}

func login(t *testing.T, engine http.Handler, username string) string {
	t.Helper()
	response, body := request(t, engine, http.MethodPost, "/api/auth/login", "", "", map[string]any{
		"username": username, "password": "Admin123!",
	})
	assertStatus(t, response, http.StatusOK)
	var envelope struct {
		Data struct {
			Token string `json:"token"`
			Role  string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.Token == "" || envelope.Data.Role != username {
		t.Fatalf("login %s failed: role=%q error=%v body=%s", username, envelope.Data.Role, err, string(body))
	}
	return envelope.Data.Token
}

func request(t *testing.T, engine http.Handler, method, path, token, requestID string, payload any) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	engine.ServeHTTP(recorder, req)
	return recorder.Result(), recorder.Body.Bytes()
}

func assertStatus(t *testing.T, response *http.Response, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		t.Fatalf("expected HTTP %d, got %d", expected, response.StatusCode)
	}
}

func decodeRecord(t *testing.T, body []byte) record {
	t.Helper()
	var envelope struct {
		Data record `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == 0 {
		t.Fatalf("decode record: %v body=%s", err, string(body))
	}
	return envelope.Data
}

func TestRegisteredDestinationsEnforcedAtManifestGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, _, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, nil, logger)

	operator := login(t, engine, "operator")
	reviewer := login(t, engine, "reviewer")

	// The generator archive list exposes the 备案去向 rows (facility + licence).
	response, body := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=10", operator, "", nil)
	assertStatus(t, response, http.StatusOK)
	var listEnvelope struct {
		Data []struct {
			Code                   string `json:"code"`
			Version                uint   `json:"version"`
			RegisteredDestinations []struct {
				FacilityName string `json:"facilityName"`
				LicenseNo    string `json:"licenseNo"`
				Status       string `json:"status"`
			} `json:"registeredDestinations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listEnvelope); err != nil {
		t.Fatalf("decode generator list: %v body=%s", err, string(body))
	}
	var seedGenerator struct {
		version   uint
		facilityA bool
		facilityB bool
		facilityC bool
		revokedD  bool
	}
	for _, item := range listEnvelope.Data {
		if item.Code != "WG-001" {
			continue
		}
		seedGenerator.version = item.Version
		for _, dest := range item.RegisteredDestinations {
			switch {
			case dest.FacilityName == "合规处置中心 A" && dest.LicenseNo == "DISP-LIC-A-001" && dest.Status == "active":
				seedGenerator.facilityA = true
			case dest.FacilityName == "资源化利用中心 B" && dest.LicenseNo == "DISP-LIC-B-002" && dest.Status == "active":
				seedGenerator.facilityB = true
			case dest.FacilityName == "安全填埋中心 C" && dest.LicenseNo == "DISP-LIC-C-003" && dest.Status == "active":
				seedGenerator.facilityC = true
			case dest.FacilityName == "旧版临时焚烧点 D" && dest.Status == "revoked":
				seedGenerator.revokedD = true
			}
		}
	}
	if !seedGenerator.facilityA || !seedGenerator.facilityB || !seedGenerator.facilityC || !seedGenerator.revokedD {
		t.Fatalf("generator list missing registered destinations: %+v", seedGenerator)
	}

	// A draft naming an unregistered facility is blocked at submit; the error
	// names the concrete location and the manifest keeps status/version.
	response, _ = request(t, engine, http.MethodPost, "/api/manifests", operator, "dest-create-missing",
		manifestPayloadWithDestination("TM-DEST-001", "CP-002", "未备案的街边回收站 X"))
	assertStatus(t, response, http.StatusCreated)
	missing := bodyOf(t, engine, operator, "TM-DEST-001")
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", missing.ID), operator, "dest-submit-missing", map[string]any{
		"status": "submitted", "expectedVersion": missing.Version, "reason": "destination not on file",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("未备案的街边回收站 X")) || !bytes.Contains(body, []byte("not registered")) {
		t.Fatalf("missing-destination error must name the location: %s", string(body))
	}
	if fresh := bodyOf(t, engine, operator, "TM-DEST-001"); fresh.Status != "draft" || fresh.Version != 1 {
		t.Fatalf("failed submit must leave manifest and version untouched, got status=%s version=%d", fresh.Status, fresh.Version)
	}

	// A draft naming the seed revoked facility is blocked with its licence.
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "dest-create-revoked",
		manifestPayloadWithDestination("TM-DEST-002", "CP-002", "旧版临时焚烧点 D"))
	assertStatus(t, response, http.StatusCreated)
	revokedManifest := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", revokedManifest.ID), operator, "dest-submit-revoked", map[string]any{
		"status": "submitted", "expectedVersion": revokedManifest.Version, "reason": "licence was withdrawn",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("旧版临时焚烧点 D")) || !bytes.Contains(body, []byte("DISP-LIC-D-004")) || !bytes.Contains(body, []byte("revoked")) {
		t.Fatalf("revoked-destination error must name facility and licence: %s", string(body))
	}

	// A draft naming an active registered destination passes the submit gate.
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "dest-create-valid",
		manifestPayloadWithDestination("TM-DEST-003", "CP-002", "资源化利用中心 B"))
	assertStatus(t, response, http.StatusCreated)
	valid := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", valid.ID), operator, "dest-submit-valid", map[string]any{
		"status": "submitted", "expectedVersion": valid.Version, "reason": "destination registered",
	})
	assertStatus(t, response, http.StatusOK)
	valid = decodeRecord(t, body)

	// A second draft with an active destination exists before the unit later
	// adjusts its 备案 list; resubmitting that draft must then be blocked.
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "dest-create-draft",
		manifestPayloadWithDestination("TM-DEST-004", "CP-002", "安全填埋中心 C"))
	assertStatus(t, response, http.StatusCreated)
	draft := decodeRecord(t, body)

	// The unit later changes registrations: A and B are revoked and C is
	// withdrawn. Maintaining the list happens through the unit update.
	response, body = request(t, engine, http.MethodPut, "/api/generators/1", operator, "dest-generator-update", generatorUpdatePayload(seedGenerator.version, []map[string]any{
		{"facilityName": "合规处置中心 A", "licenseNo": "DISP-LIC-A-001", "status": "revoked"},
		{"facilityName": "资源化利用中心 B", "licenseNo": "DISP-LIC-B-002", "status": "revoked"},
	}))
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte(`"status":"revoked"`)) {
		t.Fatalf("updated generator should return revoked destinations: %s", string(body))
	}

	// The stale draft is blocked on (re)submit against the current list.
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", draft.ID), operator, "dest-draft-blocked", map[string]any{
		"status": "submitted", "expectedVersion": draft.Version, "reason": "resubmitting stale draft",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("安全填埋中心 C")) || !bytes.Contains(body, []byte("not registered")) {
		t.Fatalf("stale draft should be blocked with concrete location: %s", string(body))
	}
	if fresh := bodyOf(t, engine, operator, "TM-DEST-004"); fresh.Status != "draft" || fresh.Version != 1 {
		t.Fatalf("blocked draft must remain unchanged, got status=%s version=%d", fresh.Status, fresh.Version)
	}

	// Dispatch re-checks the current 备案, so the earlier submitted manifest
	// cannot depart for the now-revoked facility; state/version stay as-is.
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", valid.ID), operator, "dest-dispatch-revoked", map[string]any{
		"status": "in_transit", "expectedVersion": valid.Version, "reason": "trying dispatch anyway",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !bytes.Contains(body, []byte("资源化利用中心 B")) || !bytes.Contains(body, []byte("DISP-LIC-B-002")) {
		t.Fatalf("dispatch block must name facility and licence: %s", string(body))
	}
	if fresh := bodyOf(t, engine, operator, "TM-DEST-003"); fresh.Status != "submitted" || fresh.Version != 2 {
		t.Fatalf("failed dispatch must leave manifest and version untouched, got status=%s version=%d", fresh.Status, fresh.Version)
	}
	// A submitted manifest can still be rejected (that gate is not a destination check).
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", valid.ID), operator, "dest-reject-valid", map[string]any{
		"status": "rejected", "expectedVersion": valid.Version, "reason": "facility licence withdrawn",
	})
	assertStatus(t, response, http.StatusOK)

	// A manifest already in transit before the registration change carries on
	// unchanged: in_transit -> received is never destination-gated. Restore C
	// for a fresh submit, then revoke it again before receive.
	response, _ = request(t, engine, http.MethodPut, "/api/generators/1", reviewer, "dest-generator-restore", generatorUpdatePayload(seedGenerator.version+1, []map[string]any{
		{"facilityName": "安全填埋中心 C", "licenseNo": "DISP-LIC-C-003", "status": "active"},
	}))
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "dest-create-intransit",
		manifestPayloadWithDestination("TM-DEST-005", "CP-002", "安全填埋中心 C"))
	assertStatus(t, response, http.StatusCreated)
	moving := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", moving.ID), operator, "dest-submit-intransit", map[string]any{
		"status": "submitted", "expectedVersion": moving.Version, "reason": "registered destination",
	})
	assertStatus(t, response, http.StatusOK)
	moving = decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", moving.ID), operator, "dest-dispatch-intransit", map[string]any{
		"status": "in_transit", "expectedVersion": moving.Version, "reason": "departing",
	})
	assertStatus(t, response, http.StatusOK)
	moving = decodeRecord(t, body)
	// Withdraw every destination while the truck is on the road.
	response, _ = request(t, engine, http.MethodPut, "/api/generators/1", reviewer, "dest-generator-empty", generatorUpdatePayload(seedGenerator.version+2, nil))
	assertStatus(t, response, http.StatusOK)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", moving.ID), operator, "dest-receive-anyway", map[string]any{
		"status": "received", "expectedVersion": moving.Version, "reason": "arrived at facility",
	})
	assertStatus(t, response, http.StatusOK)
	if fresh := bodyOf(t, engine, operator, "TM-DEST-005"); fresh.Status != "received" {
		t.Fatalf("in-transit manifest must complete unaffected, got status=%s", fresh.Status)
	}
}

// bodyOf reads a manifest by code via the list endpoint and decodes its record.
func bodyOf(t *testing.T, engine http.Handler, token, code string) record {
	t.Helper()
	response, body := request(t, engine, http.MethodGet, "/api/manifests?page=1&pageSize=100", token, "", nil)
	assertStatus(t, response, http.StatusOK)
	var envelope struct {
		Data []struct {
			record
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode manifest list: %v body=%s", err, string(body))
	}
	for _, item := range envelope.Data {
		if item.Code == code {
			return item.record
		}
	}
	t.Fatalf("manifest %s not found", code)
	return record{}
}

func generatorUpdatePayload(expectedVersion uint, destinations []map[string]any) map[string]any {
	return map[string]any{
		"expectedVersion": expectedVersion,
		"name":            "产废单位示例一",
		"permitNumber":    "PERMIT-WG-001",
		"permitExpiresAt": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"wasteCategories": "HW08 废矿物油",
		"description":     "用于启动验证和主要流程演示的产废单位记录",
		"facility":        "危险废物转运合规核验区域1",
		"owner":           "运行一组",
		"category":        "常规",
		"riskLevel":       "low",
		"metricValue":     12.5,
		"metricUnit":      "unit",
		"effectiveAt":     time.Now().UTC().Format(time.RFC3339),
		"evidence":        "已完成基础证据核对",
		"relatedCode":     "REL-518-01",
		// nil slice serialises to null, which binds as an empty destination list.
		"registeredDestinations": destinations,
	}
}

func manifestPayload(code, carrier string) map[string]any {
	payload := manifestPayloadWithDestination(code, carrier, "合规处置中心 A")
	payload["relatedCode"] = strings.ReplaceAll(code, "TM", "REL")
	return payload
}

func manifestPayloadWithDestination(code, carrier, destination string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试联单", "description": "valid linked transfer manifest",
		"generatorCode": "WG-001", "carrierCode": carrier, "wasteCode": "HW08-900-249-08", "quantityKg": 680.5,
		"destination": destination, "facility": "东区危废暂存区", "owner": "operator", "category": "危废转运",
		"riskLevel": "medium", "metricValue": 68, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/manifest.pdf", "relatedCode": strings.ReplaceAll(code, "TM", "REL"),
	}
}
func checkPayload(code, manifest string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试核验", "description": "linked compliance decision",
		"manifestCode": manifest, "checklist": "产废许可、承运资质、联单数量、处置去向", "decisionBasis": "",
		"facility": "复核中心", "owner": "reviewer", "category": "联单复核", "riskLevel": "medium",
		"metricValue": 92, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/check.pdf", "relatedCode": manifest,
	}
}
