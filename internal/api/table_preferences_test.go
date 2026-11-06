package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
)

func TestTablePreferenceHTTPValidationAndCompletePayload(t *testing.T) {
	s, _ := newPersonalTestServer()
	store := &diagnosticProfileStore{}
	manager, err := auth.New(context.Background(), store, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "https://gateway.test"})
	if err != nil {
		t.Fatal(err)
	}
	s.auth = &diagnosticProfileAuth{manager: manager}
	h := s.handler()
	for _, body := range []string{
		`{"table_columns":{"PRIVATE_SENTINEL":["organization"]}}`,
		`{"table_columns":{"organizations":["PRIVATE_SENTINEL"]}}`,
		`{"table_columns":{"organizations":["organization","organization"]}}`,
		`{"table_columns":{"requests":["date"]}}`,
		`{"table_wide":{"PRIVATE_SENTINEL":true}}`,
		`{"table_wide":{"organizations":null}}`,
		`{"table_wide":{"PRIVATE_SENTINEL":"PRIVATE_VALUE"}}`,
		`{"table_columns":null,"table_wide":null}`,
	} {
		w := sendTest(h, "PATCH", "/v1/me", body, "alice", "")
		if w.Code != 400 || strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatal("unsafe/accepted preferences", w.Code, w.Body)
		}
	}
	if store.writes != 0 {
		t.Fatal("invalid preferences reached storage")
	}
	columns := map[string][]string{}
	wide := map[string]bool{}
	for table, ids := range auth.TableColumnIDs {
		wide[table] = false
		if table != "requests" {
			columns[table] = ids
		}
	}
	raw, err := json.Marshal(map[string]any{"table_columns": columns, "table_wide": wide, "report_columns": []string{"date", "generation_id", "organization", "user", "api_key", "model", "provider", "status", "tokens", "estimated_cost", "cost", "confirmed_cost", "settlement", "customer_charge", "profit", "profit_status"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 8192 {
		t.Fatal("complete preferences exceed existing body bound", len(raw))
	}
	w := sendTest(h, "PATCH", "/v1/me", string(raw), "alice", "")
	if w.Code != 200 || store.writes != 1 {
		t.Fatal("complete bounded preferences rejected", w.Code, w.Body)
	}
}
