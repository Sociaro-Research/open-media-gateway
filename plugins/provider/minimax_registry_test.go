package providerassets

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
)

func TestMiniMaxBuiltInDefinitionAndFactory(t *testing.T) {
	d, ok := Find("minimax")
	if !ok || d.ID != "minimax" || d.DefaultModel != "MiniMax-H3" || !d.PublicCatalog || !d.AuthenticatedCheck {
		t.Fatal("MiniMax definition not composed")
	}
	if _, err := d.Factory(providerkit.Credentials{}, nil, nil); err == nil {
		t.Fatal("factory accepted missing credentials")
	}
	p, err := d.Factory(providerkit.Credentials{APIKey: "deterministic-registry-fixture"}, nil, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := p.(*minimax.Adapter)
	if !ok {
		t.Fatal("factory returned wrong adapter")
	}
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 33 {
		t.Fatal("reviewed catalogue missing", len(models), err)
	}
	for _, id := range []string{"openai", "mock", "bytedance", "alibaba", "agnes", "sonilo", "kling"} {
		if _, ok := Find(id); !ok {
			t.Fatal("existing provider removed", id)
		}
	}
	if Canonical("dashscope") != "alibaba" {
		t.Fatal("existing alias lost")
	}
}
