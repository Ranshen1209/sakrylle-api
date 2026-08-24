package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadGatewayDeepSeekFilesDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 1000, cfg.Gateway.DeepSeekFiles.TenantMaxFiles)
	require.Equal(t, int64(2*1024*1024*1024), cfg.Gateway.DeepSeekFiles.TenantMaxBytes)
	require.Equal(t, 10000, cfg.Gateway.DeepSeekFiles.AccountMaxFiles)
	require.Equal(t, int64(25*1024*1024*1024), cfg.Gateway.DeepSeekFiles.AccountMaxBytes)
}

func TestLoadGatewayDeepSeekFilesFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_DEEPSEEK_FILES_TENANT_MAX_FILES", "120")
	t.Setenv("GATEWAY_DEEPSEEK_FILES_TENANT_MAX_BYTES", "3145728")
	t.Setenv("GATEWAY_DEEPSEEK_FILES_ACCOUNT_MAX_FILES", "1200")
	t.Setenv("GATEWAY_DEEPSEEK_FILES_ACCOUNT_MAX_BYTES", "33554432")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 120, cfg.Gateway.DeepSeekFiles.TenantMaxFiles)
	require.Equal(t, int64(3145728), cfg.Gateway.DeepSeekFiles.TenantMaxBytes)
	require.Equal(t, 1200, cfg.Gateway.DeepSeekFiles.AccountMaxFiles)
	require.Equal(t, int64(33554432), cfg.Gateway.DeepSeekFiles.AccountMaxBytes)
}

func TestValidateGatewayDeepSeekFilesLimits(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*GatewayDeepSeekFilesConfig)
		wantErr string
	}{
		{
			name:    "tenant file limit must be positive",
			mutate:  func(c *GatewayDeepSeekFilesConfig) { c.TenantMaxFiles = 0 },
			wantErr: "gateway.deepseek_files.tenant_max_files must be positive",
		},
		{
			name:    "tenant byte limit must be positive",
			mutate:  func(c *GatewayDeepSeekFilesConfig) { c.TenantMaxBytes = 0 },
			wantErr: "gateway.deepseek_files.tenant_max_bytes must be positive",
		},
		{
			name:    "account file limit must be positive",
			mutate:  func(c *GatewayDeepSeekFilesConfig) { c.AccountMaxFiles = 0 },
			wantErr: "gateway.deepseek_files.account_max_files must be positive",
		},
		{
			name:    "account byte limit must be positive",
			mutate:  func(c *GatewayDeepSeekFilesConfig) { c.AccountMaxBytes = 0 },
			wantErr: "gateway.deepseek_files.account_max_bytes must be positive",
		},
		{
			name: "tenant file limit must be below account limit",
			mutate: func(c *GatewayDeepSeekFilesConfig) {
				c.TenantMaxFiles = c.AccountMaxFiles
			},
			wantErr: "gateway.deepseek_files.tenant_max_files must be less than account_max_files",
		},
		{
			name: "tenant byte limit must be below account limit",
			mutate: func(c *GatewayDeepSeekFilesConfig) {
				c.TenantMaxBytes = c.AccountMaxBytes
			},
			wantErr: "gateway.deepseek_files.tenant_max_bytes must be less than account_max_bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			tt.mutate(&cfg.Gateway.DeepSeekFiles)

			require.ErrorContains(t, cfg.Validate(), tt.wantErr)
		})
	}
}
