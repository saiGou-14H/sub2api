package service

import "context"

const CodexTicketSettingsKey = "openai_codex_ticket_settings"
const CodexTicketMaxRevision = uint64(1<<53 - 1)

type CodexTicketMissingPolicy string

const (
	CodexTicketPassthrough CodexTicketMissingPolicy = "passthrough"
	CodexTicketReject      CodexTicketMissingPolicy = "reject"
)

const (
	CodexTicketCookiePinRequired = "required"
	CodexTicketCookiePinOptional = "optional"
)

type CodexTicketSettings struct {
	SchemaVersion        int                      `json:"schema_version"`
	Revision             uint64                   `json:"revision,string"`
	Enabled              bool                     `json:"enabled"`
	HarvestProxyID       *int64                   `json:"harvest_proxy_id"`
	Models               []string                 `json:"models"`
	TargetLength         int                      `json:"target_length"`
	TTLSeconds           int                      `json:"ttl_seconds"`
	RefreshBeforeSeconds int                      `json:"refresh_before_seconds"`
	CookiePinMode        string                   `json:"cookie_pin_mode"`
	MissingPolicy        CodexTicketMissingPolicy `json:"missing_ticket_policy"`
	MaxConcurrency       int                      `json:"max_concurrency"`
	MaxProbesPerMinute   int                      `json:"max_probes_per_minute"`
}

type CodexTicketSettingsRepository interface {
	Load(context.Context) (CodexTicketSettings, error)
	Save(context.Context, uint64, CodexTicketSettings) (CodexTicketSettings, error)
}
