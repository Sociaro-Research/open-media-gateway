package auth

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"strings"
	"testing"
)

type profileFixture struct {
	Repository
	calls int
}

func (f *profileFixture) UpdateProfile(_ context.Context, id string, in ProfileUpdate) (identity.User, error) {
	f.calls++
	return identity.User{ID: id}, nil
}
func TestProfileValidation(t *testing.T) {
	repo := &profileFixture{}
	m := &Manager{repo: repo}
	ctx := context.Background()
	for _, name := range []string{"", "  ", "a\nb", strings.Repeat("é", 101)} {
		if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Name: &name}); !errors.Is(err, ErrInvalidProfile) {
			t.Fatal(name, err)
		}
	}
	bad := "auto"
	for _, in := range []ProfileUpdate{{}, {Theme: &bad}} {
		if _, err := m.UpdateProfile(ctx, "self", in); !errors.Is(err, ErrInvalidProfile) {
			t.Fatal(err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid update reached storage")
	}
	for _, theme := range []string{"dark", "light", "system"} {
		if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Theme: &theme}); err != nil {
			t.Fatal(err)
		}
	}
	name := strings.Repeat("é", 100)
	if _, err := m.UpdateProfile(ctx, "self", ProfileUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
}

func TestReportColumnPreferencesValidation(t *testing.T) {
	repo := &profileFixture{}
	m := &Manager{repo: repo}
	for _, columns := range [][]string{{}, {"date", "date"}, {"unknown"}} {
		if _, err := m.UpdateProfile(context.Background(), "self", ProfileUpdate{ReportColumns: &columns}); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("columns=%v err=%v", columns, err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid columns reached storage")
	}
	columns := []string{"date", "generation_id", "api_key", "cost"}
	if _, err := m.UpdateProfile(context.Background(), "self", ProfileUpdate{ReportColumns: &columns}); err != nil {
		t.Fatal(err)
	}
}
