package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

// The company website is required. A missing or rejected one must be named
// like every other organization field, not reported as a generic bad request.
func TestOrganizationWebsiteErrorNamesTheDomainField(t *testing.T) {
	repo := postgres.NewSystemRepository(nil)
	for _, website := range []string{"", "intranet", "192.168.1.10", "ftp://example.com"} {
		_, err := repo.CreateOrganization(context.Background(), "actor", "Example team", "owner@example.com", website)
		w := httptest.NewRecorder()
		orgError(w, err)
		var got struct {
			Error struct {
				Code, Message string
				Param         *string
			}
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Param == nil || *got.Error.Param != "domain" || !strings.Contains(got.Error.Message, "example.com") {
			t.Fatalf("website %q: %d %s", website, w.Code, w.Body.String())
		}
	}
	// Sales onboarding validates the same field through the same rule.
	in := org.ClientInput{OperationID: uuid.NewString(), Name: "Example team", Country: "EE", ContactName: "Owner", Email: "owner@example.com", Domain: "intranet"}
	if err := in.Normalize(); !errors.Is(err, org.InvalidDomain) || !errors.Is(err, org.ErrInvalid) {
		t.Fatal("onboarding website error", err)
	}
}
