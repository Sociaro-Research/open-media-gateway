package kling

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const maxEffectPrices = 219

var effectPriceDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)

// setEffectPrices accepts only explicit account tariffs for reviewed scenes.
// Decode keys individually so duplicate keys, including escaped aliases, cannot
// silently replace a different operator-confirmed rate.
func (a *Adapter) setEffectPrices(c providerkit.Credentials) error {
	invalid := func() error {
		return errors.New("Kling effect_prices must be a JSON object of documented scenes and positive decimal USD/request strings")
	}
	raw := strings.TrimSpace(c.Get("effect_prices"))
	prices := map[string]money.USD{}
	if raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		start, err := decoder.Token()
		if err != nil || start != json.Delim('{') {
			return invalid()
		}
		for decoder.More() {
			if len(prices) >= maxEffectPrices {
				return invalid()
			}
			token, err := decoder.Token()
			scene, ok := token.(string)
			if err != nil || !ok || !slices.Contains(effectScenes, scene) {
				return invalid()
			}
			if _, duplicate := prices[scene]; duplicate {
				return invalid()
			}
			var rawPrice string
			if err := decoder.Decode(&rawPrice); err != nil || !effectPriceDecimal.MatchString(rawPrice) {
				return invalid()
			}
			price, err := money.Parse(rawPrice)
			if err != nil || price <= 0 {
				return invalid()
			}
			prices[scene] = price
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF {
			return invalid()
		}
	}
	// Commit only a fully valid configuration and replace only Effects tariffs.
	if a.operationPrices == nil {
		a.operationPrices = map[string]money.USD{}
	}
	for key := range a.operationPrices {
		if strings.HasPrefix(key, "effect:") {
			delete(a.operationPrices, key)
		}
	}
	for scene, price := range prices {
		a.operationPrices["effect:"+scene] = price
	}
	return nil
}

func (a *Adapter) quoteEffect(r core.MediaRequest) (core.TaskCost, string, error) {
	if r.Model != KlingEffects {
		return core.TaskCost{}, "", core.ErrModelUnavailable
	}
	if err := validateSolutionRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	parameters, err := core.NormalizeParameters(solutionModel(KlingEffects).Parameters, r.Options.Parameters)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	scene, ok := parameters["effect_scene"].(string)
	if !ok || !slices.Contains(effectScenes, scene) {
		return core.TaskCost{}, "", core.ErrOptionsInvalid
	}
	return a.quoteFixedOperation("effect:" + scene)
}
