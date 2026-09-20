package service

import (
	"sort"
	"strings"
	"unicode"
)

// OpenAIPrismDefaultModel follows Prism's project-chat frontend default observed
// on 2026-09-18. The older HAR's gpt-6-astra is no longer accepted upstream.
// Additional models still require explicit account mappings.
const OpenAIPrismDefaultModel = "gpt-5.6-sol"

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
