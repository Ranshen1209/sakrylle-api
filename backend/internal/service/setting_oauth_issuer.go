package service

import (
	"context"
	"log/slog"
	"strings"
)

type OAuthIssuerSource string

const (
	OAuthIssuerSourceCanonical OAuthIssuerSource = "oauth_issuer"
	OAuthIssuerSourceFrontend  OAuthIssuerSource = "frontend_url"
	OAuthIssuerSourceConfig    OAuthIssuerSource = "config.frontend_url"
	OAuthIssuerSourceNone      OAuthIssuerSource = ""
)

// GetOAuthIssuer keeps discovery and device-flow issuer resolution consistent.
func (s *SettingService) GetOAuthIssuer(ctx context.Context) (string, OAuthIssuerSource) {
	if s == nil {
		return "", OAuthIssuerSourceNone
	}
	if s.settingRepo != nil {
		if value, err := s.settingRepo.GetValue(ctx, SettingKeyOAuthIssuer); err == nil {
			if issuer := strings.TrimRight(strings.TrimSpace(value), "/"); issuer != "" {
				return issuer, OAuthIssuerSourceCanonical
			}
		}
		if value, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL); err == nil {
			if issuer := strings.TrimRight(strings.TrimSpace(value), "/"); issuer != "" {
				return issuer, OAuthIssuerSourceFrontend
			}
		}
	}
	if s.cfg != nil {
		if issuer := strings.TrimRight(strings.TrimSpace(s.cfg.Server.FrontendURL), "/"); issuer != "" {
			return issuer, OAuthIssuerSourceConfig
		}
	}
	return "", OAuthIssuerSourceNone
}

func (s *SettingService) WarnIfOAuthIssuerMissing(ctx context.Context) {
	if s == nil {
		return
	}
	if _, source := s.GetOAuthIssuer(ctx); source != OAuthIssuerSourceNone {
		return
	}
	slog.Warn("oauth_issuer and frontend_url are both empty; OAuth discovery and device flow will fall back to request scheme://Host",
		"hint", "set settings.oauth_issuer to the canonical public origin")
}
