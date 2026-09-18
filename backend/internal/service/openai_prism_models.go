package service

import (
	"sort"
	"strings"
	"unicode"
)

// OpenAIPrismDefaultModel is the model observed on Prism's project chat API.
// Additional models require an explicit account mapping; this is not a claim
// that the Codex or ChatGPT Web model catalogs are available through Prism.
const OpenAIPrismDefaultModel = "gpt-6-astra"

func NormalizeOpenAIPrismModel(model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, "*?\\/") || strings.IndexFunc(model, unicode.IsSpace) >= 0 {
		return "", false
	}
	return model, true
}

func OpenAIPrismModels() []string {
	return []string{OpenAIPrismDefaultModel}
}

// OpenAIPrismAccountModels exposes only concrete public names authorized by
// this account. Model mappings use the same whitelist semantics as other
// transports, without adding any implicit Codex aliases.
func OpenAIPrismAccountModels(account *Account) []string {
	if account == nil || len(account.GetModelMapping()) == 0 {
		return OpenAIPrismModels()
	}
	models := make([]string, 0, len(account.GetModelMapping()))
	for publicName, upstreamModel := range account.GetModelMapping() {
		name, validName := NormalizeOpenAIPrismModel(publicName)
		_, validTarget := NormalizeOpenAIPrismModel(upstreamModel)
		if validName && validTarget {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return models
}

func isOpenAIPrismModelSupported(account *Account, requestedModel string) bool {
	model, valid := NormalizeOpenAIPrismModel(requestedModel)
	if !valid {
		return false
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return model == OpenAIPrismDefaultModel
	}
	target, matched := mapping[model]
	_, validTarget := NormalizeOpenAIPrismModel(target)
	return matched && validTarget
}
