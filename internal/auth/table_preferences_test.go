package auth

import (
	"context"
	"strings"
	"testing"
)

func TestTablePreferenceValidationAndDiagnostics(t *testing.T) {
	repo := &profileFixture{}
	m := &Manager{repo: repo}
	yes := true
	for _, in := range []ProfileUpdate{
		{TableColumns: &map[string][]string{}},
		{TableColumns: &map[string][]string{"PRIVATE_SENTINEL": {"name"}}},
		{TableColumns: &map[string][]string{"requests": {"date"}}},
		{TableColumns: &map[string][]string{"organizations": {}}},
		{TableColumns: &map[string][]string{"organizations": {"organization", "organization"}}},
		{TableColumns: &map[string][]string{"organizations": {"PRIVATE_SENTINEL"}}},
		{TableWide: &map[string]*bool{}},
		{TableWide: &map[string]*bool{"PRIVATE_SENTINEL": &yes}},
		{TableWide: &map[string]*bool{"requests": nil}},
	} {
		_, err := m.UpdateProfile(context.Background(), "self", in)
		validation, ok := err.(*ProfileValidationError)
		if !ok {
			t.Fatalf("untyped validation: %v", err)
		}
		field, message := validation.Diagnostic()
		if !strings.HasPrefix(field, "table_") || strings.Contains(field+message, "PRIVATE_SENTINEL") {
			t.Fatal(field, message)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid preferences reached repository")
	}
	for table, columns := range TableColumnIDs {
		in := ProfileUpdate{TableWide: &map[string]*bool{table: &yes}}
		if table != "requests" {
			in.TableColumns = &map[string][]string{table: columns}
		}
		if _, err := m.UpdateProfile(context.Background(), "self", in); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
	if _, err := m.UpdateProfile(context.Background(), "self", ProfileUpdate{TableColumns: &map[string][]string{"organizations": nil}}); err != nil {
		t.Fatal("reset rejected", err)
	}
}
