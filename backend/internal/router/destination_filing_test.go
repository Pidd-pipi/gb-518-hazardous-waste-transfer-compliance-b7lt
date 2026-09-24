package router_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/config"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/database"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type destinationRecord struct {
	FacilityName  string `json:"facilityName"`
	LicenseNumber string `json:"licenseNumber"`
	Status        string `json:"status"`
}

type generatorDetail struct {
	record
	Name         string              `json:"name"`
	PermitNumber string              `json:"permitNumber"`
	Destinations []destinationRecord `json:"destinations"`
}

func TestFiledDestinationsBlockManifestsAndRevokeOnlyAffectsNewSubmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := filingTestConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, _, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, nil, logger)

	operator := login(t, engine, "operator")
	now := time.Now().UTC().Format(time.RFC3339)
	stamp := time.Now().UnixNano()

	// A dedicated generator keeps the workflow independent of the seed data.
	generatorCode := fmt.Sprintf("WG-FILED-%d", stamp%1000000)
	facilityA := destinationRecord{FacilityName: "测试备案处置厂 甲", LicenseNumber: "TEST-DISPOSAL-LIC-A", Status: "active"}
	facilityB := destinationRecord{FacilityName: "测试备案处置厂 乙", LicenseNumber: "TEST-DISPOSAL-LIC-B", Status: "active"}
	payload := generatorCreatePayload(generatorCode, now, []destinationRecord{facilityA, facilityB})
	response, body := request(t, engine, http.MethodPost, "/api/generators", operator, "filing-generator-create", payload)
	assertStatus(t, response, http.StatusCreated)
	generator := decodeGenerator(t, body)
	if len(generator.Destinations) != 2 {
		t.Fatalf("expected 2 filed destinations, got %+v", generator.Destinations)
	}
	for _, destination := range generator.Destinations {
		if destination.Status != "active" {
			t.Fatalf("new filings must be active, got %+v", destination)
		}
	}

	// New generators are created in the active state required by manifest
	// submission, so no status transition is needed here.

	// 1. Draft with an unfiled destination cannot be submitted; the response
	// names the concrete location.
	unfiledDestination := "未备案处置厂 丙"
	unfiled := createFilingManifest(t, engine, operator, fmt.Sprintf("TM-UNFILED-%d", stamp%1000000), generatorCode, unfiledDestination)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", unfiled.ID), operator, "filing-unfiled-submit", map[string]any{
		"status": "submitted", "expectedVersion": unfiled.Version, "reason": "unfiled destination must be blocked",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(string(body), unfiledDestination) || !strings.Contains(string(body), "filed disposal destinations") {
		t.Fatalf("expected concrete location in error, got %s", string(body))
	}
	unfiled = decodeRecord(t, mustGet(t, engine, fmt.Sprintf("/api/manifests/%d", unfiled.ID), operator))
	if unfiled.Status != "draft" || unfiled.Version != 1 {
		t.Fatalf("blocked submission must leave manifest and version untouched, got %+v", unfiled)
	}

	// 2. A filing that is subsequently revoked blocks (re)submission and the
	// error reports the concrete location together with its license number.
	revokedDestination := "临时焚烧处置点 丁"
	revoked := createFilingManifest(t, engine, operator, fmt.Sprintf("TM-REVOKED-%d", stamp%1000000), generatorCode, revokedDestination)
	generator = updateGeneratorDestinations(t, engine, operator, generator, []destinationRecord{
		{FacilityName: facilityA.FacilityName, LicenseNumber: facilityA.LicenseNumber},
		{FacilityName: facilityB.FacilityName, LicenseNumber: facilityB.LicenseNumber},
		{FacilityName: revokedDestination, LicenseNumber: "TEST-DISPOSAL-LIC-REVOKED"},
	})
	generator = updateGeneratorDestinations(t, engine, operator, generator, []destinationRecord{
		{FacilityName: facilityA.FacilityName, LicenseNumber: facilityA.LicenseNumber},
		{FacilityName: facilityB.FacilityName, LicenseNumber: facilityB.LicenseNumber},
	})
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", revoked.ID), operator, "filing-revoked-submit", map[string]any{
		"status": "submitted", "expectedVersion": revoked.Version, "reason": "revoked destination must be blocked",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(string(body), revokedDestination) || !strings.Contains(string(body), "TEST-DISPOSAL-LIC-REVOKED") || !strings.Contains(string(body), "revoked") {
		t.Fatalf("expected revoked location and license in error, got %s", string(body))
	}

	// 3. A manifest submitted while the filing is active reaches submitted.
	valid := createFilingManifest(t, engine, operator, fmt.Sprintf("TM-FILED-OK-%d", stamp%1000000), generatorCode, facilityA.FacilityName)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", valid.ID), operator, "filing-valid-submit", map[string]any{
		"status": "submitted", "expectedVersion": valid.Version, "reason": "active filing matches destination",
	})
	assertStatus(t, response, http.StatusOK)
	valid = decodeRecord(t, body)
	if valid.Status != "submitted" || valid.Version != 2 {
		t.Fatalf("active filing must submit, got %+v", valid)
	}

	// 4. The generator later withdraws facility A. The already-submitted
	// manifest and its version stay exactly as they were, and dispatch proceeds
	// as before because the destination was locked in at submission time.
	generator = updateGeneratorDestinations(t, engine, operator, generator, []destinationRecord{
		{FacilityName: facilityB.FacilityName, LicenseNumber: facilityB.LicenseNumber},
	})
	valid = decodeRecord(t, mustGet(t, engine, fmt.Sprintf("/api/manifests/%d", valid.ID), operator))
	if valid.Status != "submitted" || valid.Version != 2 {
		t.Fatalf("filing change must not alter submitted manifest, got %+v", valid)
	}
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", valid.ID), operator, "filing-dispatched-after-revoke", map[string]any{
		"status": "in_transit", "expectedVersion": valid.Version, "reason": "in-flight manifest follows the filing captured at submission",
	})
	assertStatus(t, response, http.StatusOK)
	valid = decodeRecord(t, body)
	if valid.Status != "in_transit" || valid.Version != 3 {
		t.Fatalf("in-transit transition must survive later filing revocation, got %+v", valid)
	}

	// 5. Only a draft (re)submitted after the withdrawal is blocked; the error
	// still names the withdrawn facility and its license.
	redraft := createFilingManifest(t, engine, operator, fmt.Sprintf("TM-REDRAFT-%d", stamp%1000000), generatorCode, facilityA.FacilityName)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", redraft.ID), operator, "filing-redraft-submit", map[string]any{
		"status": "submitted", "expectedVersion": redraft.Version, "reason": "withdrawn filing blocks new submission",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(string(body), facilityA.FacilityName) || !strings.Contains(string(body), "TEST-DISPOSAL-LIC-A") {
		t.Fatalf("expected withdrawn facility/license in error, got %s", string(body))
	}
}

func filingTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppName: "hazardous-waste-transfer-compliance-filing-test", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: "file:filing-test?mode=memory&cache=shared",
		JWTSecret: "filing-test-secret-at-least-32-characters-long", TokenTTL: time.Hour,
		RequestLimit: 10000, StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
	}
}

func generatorCreatePayload(code, now string, destinations []destinationRecord) map[string]any {
	return map[string]any{
		"code": code, "name": "备案去向测试单位", "permitNumber": code + "-PERMIT",
		"permitExpiresAt": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"wasteCategories": "HW08 废矿物油", "facility": "备案测试区域", "owner": "operator",
		"category": "危废", "riskLevel": "medium", "metricValue": 10, "metricUnit": "score",
		"effectiveAt": now, "evidence": "minio://evidence/tests/generator.pdf", "relatedCode": "",
		"destinations": destinations,
	}
}

func createFilingManifest(t *testing.T, engine http.Handler, token, code, generatorCode, destination string) record {
	t.Helper()
	payload := map[string]any{
		"code": code, "name": "备案去向测试联单", "generatorCode": generatorCode, "carrierCode": "CP-002",
		"wasteCode": "HW08-900-249-08", "quantityKg": 320, "destination": destination,
		"facility": "备案测试区域", "owner": "operator", "category": "危废", "riskLevel": "medium",
		"metricValue": 40, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/manifest.pdf", "relatedCode": "REL-FILING",
	}
	response, body := request(t, engine, http.MethodPost, "/api/manifests", token, "", payload)
	assertStatus(t, response, http.StatusCreated)
	return decodeRecord(t, body)
}

func decodeGenerator(t *testing.T, body []byte) generatorDetail {
	t.Helper()
	var envelope struct {
		Data generatorDetail `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == 0 {
		t.Fatalf("decode generator: %v body=%s", err, string(body))
	}
	return envelope.Data
}

func updateGeneratorDestinations(t *testing.T, engine http.Handler, token string, current generatorDetail, destinations []destinationRecord) generatorDetail {
	t.Helper()
	payload := map[string]any{
		"expectedVersion": current.Version, "name": current.Name, "permitNumber": current.PermitNumber,
		"permitExpiresAt": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"wasteCategories": "HW08 废矿物油", "facility": "备案测试区域", "owner": "operator",
		"category": "危废", "riskLevel": "medium", "metricValue": 10, "metricUnit": "score",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339), "evidence": "minio://evidence/tests/generator.pdf", "relatedCode": "",
		"destinations": destinations,
	}
	response, body := request(t, engine, http.MethodPut, fmt.Sprintf("/api/generators/%d", current.ID), token, "", payload)
	assertStatus(t, response, http.StatusOK)
	return decodeGenerator(t, body)
}

func mustGet(t *testing.T, engine http.Handler, path, token string) []byte {
	t.Helper()
	response, body := request(t, engine, http.MethodGet, path, token, "", nil)
	assertStatus(t, response, http.StatusOK)
	return body
}
