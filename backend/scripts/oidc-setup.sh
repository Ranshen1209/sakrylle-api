#!/usr/bin/env bash
#
# OIDC Setup Script
# Generates KEK, validates environment, and initializes OIDC signing keys
#
# Usage:
#   ./scripts/oidc-setup.sh [--production]
#
# Flags:
#   --production    Skip interactive prompts, require all env vars
#

set -euo pipefail

# Colors
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m' # No Color

# Configuration
PRODUCTION_MODE=false
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --production)
      PRODUCTION_MODE=true
      shift
      ;;
    *)
      echo -e "${RED}Unknown argument: $1${NC}" >&2
      exit 1
      ;;
  esac
done

# Logging functions
log_info() {
  echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_warn() {
  echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $*" >&2
}

log_step() {
  echo -e "${BLUE}=== $* ===${NC}"
}

# Generate cryptographically secure random hex
generate_hex() {
  local bytes=$1
  openssl rand -hex "$bytes"
}

# Check if command exists
require_command() {
  local cmd=$1
  if ! command -v "$cmd" &> /dev/null; then
    log_error "Required command not found: $cmd"
    exit 1
  fi
}

# Validate environment variable
validate_env() {
  local var_name=$1
  local min_length=${2:-1}

  if [[ -z "${!var_name:-}" ]]; then
    log_error "Required environment variable not set: $var_name"
    return 1
  fi

  if [[ ${#!var_name} -lt $min_length ]]; then
    log_error "$var_name must be at least $min_length characters"
    return 1
  fi

  return 0
}

# Check PostgreSQL connection
check_postgres() {
  log_step "Checking PostgreSQL Connection"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if ! validate_env POSTGRES_PASSWORD 1; then
    return 1
  fi

  log_info "Testing connection to ${db_user}@${db_host}:${db_port}/${db_name}"

  if ! PGPASSWORD="${POSTGRES_PASSWORD}" psql \
       -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
       -c "SELECT 1;" &> /dev/null; then
    log_error "Cannot connect to PostgreSQL"
    log_warn "Ensure POSTGRES_HOST, POSTGRES_PORT, POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB are set correctly"
    return 1
  fi

  log_success "PostgreSQL connection OK"
  return 0
}

# Check if migrations are applied
check_migrations() {
  log_step "Checking Database Migrations"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  local required_tables=("oidc_signing_keys" "oauth_scopes")

  for table in "${required_tables[@]}"; do
    log_info "Checking table: $table"

    if ! PGPASSWORD="${POSTGRES_PASSWORD}" psql \
         -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
         -tAc "SELECT to_regclass('public.$table');" | grep -q "$table"; then
      log_error "Required table not found: $table"
      log_warn "Run migrations first: make migrate-up or atlas migrate apply"
      return 1
    fi
  done

  log_success "All required tables exist"

  # Check for OIDC scopes
  log_info "Checking OIDC scopes registration"

  local scope_count
  scope_count=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT COUNT(*) FROM oauth_scopes WHERE scope IN ('openid', 'profile', 'email');")

  if [[ "$scope_count" -lt 3 ]]; then
    log_warn "OIDC scopes not fully registered (found $scope_count/3)"
    log_info "Migration 150_grant_oidc_scopes.sql should have seeded them"
  else
    log_success "OIDC scopes registered ($scope_count/3)"
  fi

  return 0
}

# Generate KEK if not present
# This KEK is provided by TOTP_ENCRYPTION_KEY (cfg.Totp.EncryptionKey), the var
# the code actually reads. It protects OIDC signing keys, TOTP secrets, monitor,
# and backup secrets; rotating it invalidates all of them at once.
generate_kek() {
  log_step "Checking OIDC Key Encryption Key (KEK)"

  if validate_env TOTP_ENCRYPTION_KEY 64; then
    log_success "TOTP_ENCRYPTION_KEY already set (${#TOTP_ENCRYPTION_KEY} chars)"
    return 0
  fi

  log_warn "TOTP_ENCRYPTION_KEY not set or too short"

  if [[ "$PRODUCTION_MODE" == true ]]; then
    log_error "In production mode, KEK must be pre-set"
    return 1
  fi

  log_info "Generating new 256-bit KEK (64 hex chars)..."
  local new_kek
  new_kek=$(generate_hex 32)

  echo ""
  log_success "Generated KEK:"
  echo -e "${GREEN}${new_kek}${NC}"
  echo ""
  log_warn "Add this to your .env file:"
  echo -e "${YELLOW}TOTP_ENCRYPTION_KEY=${new_kek}${NC}"
  echo ""

  read -r -p "$(echo -e "${BLUE}Export KEK to current shell? (y/N): ${NC}")" confirm
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    export TOTP_ENCRYPTION_KEY="$new_kek"
    log_success "KEK exported to current session"
  else
    log_warn "KEK not exported. Set TOTP_ENCRYPTION_KEY manually before proceeding."
    return 1
  fi

  return 0
}

# Check if keys already exist
check_existing_keys() {
  log_step "Checking Existing OIDC Signing Keys"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  local key_count
  key_count=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT COUNT(*) FROM oidc_signing_keys WHERE deleted_at IS NULL;")

  if [[ "$key_count" -gt 0 ]]; then
    log_warn "Found $key_count existing signing keys"

    local current_kid
    current_kid=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
      -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
      -tAc "SELECT value FROM settings WHERE key = 'oidc_current_kid';")

    if [[ -n "$current_kid" ]]; then
      log_info "Current active KID: $current_kid"
    else
      log_warn "No current_kid set in settings"
    fi

    if [[ "$PRODUCTION_MODE" == true ]]; then
      log_error "Keys already exist. Use oidc-key-rotate.sh to rotate."
      return 1
    fi

    read -r -p "$(echo -e "${YELLOW}Delete existing keys and regenerate? (y/N): ${NC}")" confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
      log_info "Keeping existing keys"
      return 0
    fi

    log_warn "Soft-deleting existing keys..."
    PGPASSWORD="${POSTGRES_PASSWORD}" psql \
      -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
      -c "UPDATE oidc_signing_keys SET deleted_at = NOW() WHERE deleted_at IS NULL;"

    log_success "Existing keys marked as deleted"
  else
    log_info "No existing keys found"
  fi

  return 0
}

# Generate initial signing keys via API
generate_signing_keys() {
  log_step "Generating OIDC Signing Keys"

  local api_base="${API_BASE_URL:-http://localhost:18080}"

  if ! validate_env ADMIN_TOKEN 1; then
    log_error "ADMIN_TOKEN not set"
    log_warn "Get admin token via: POST /api/v1/auth/login with admin credentials"
    return 1
  fi

  log_info "Calling POST ${api_base}/api/v1/admin/oidc/keys/generate"

  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" \
    -X POST "${api_base}/api/v1/admin/oidc/keys/generate" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json")

  http_code=$(echo "$response" | tail -n1)
  local body
  body=$(echo "$response" | sed '$d')

  if [[ "$http_code" != "200" ]]; then
    log_error "API call failed with HTTP $http_code"
    echo "$body" | jq '.' 2>/dev/null || echo "$body"
    return 1
  fi

  log_success "Signing keys generated successfully"
  echo "$body" | jq '.'

  # Extract KIDs
  local rs256_kid
  local es256_kid
  rs256_kid=$(echo "$body" | jq -r '.data.rs256_kid // empty')
  es256_kid=$(echo "$body" | jq -r '.data.es256_kid // empty')

  if [[ -n "$rs256_kid" ]]; then
    log_success "RS256 KID: $rs256_kid"
  fi
  if [[ -n "$es256_kid" ]]; then
    log_success "ES256 KID: $es256_kid"
  fi

  return 0
}

# Verify OIDC discovery endpoint
verify_discovery() {
  log_step "Verifying OIDC Discovery Endpoint"

  local api_base="${API_BASE_URL:-http://localhost:18080}"
  local issuer="${OIDC_ISSUER:-${api_base}}"

  log_info "GET ${issuer}/.well-known/openid-configuration"

  local response
  response=$(curl -s "${issuer}/.well-known/openid-configuration")

  if ! echo "$response" | jq -e '.issuer' &> /dev/null; then
    log_error "Discovery endpoint returned invalid JSON"
    echo "$response"
    return 1
  fi

  local discovered_issuer
  discovered_issuer=$(echo "$response" | jq -r '.issuer')

  if [[ "$discovered_issuer" != "$issuer" ]]; then
    log_warn "Issuer mismatch: expected $issuer, got $discovered_issuer"
  fi

  log_success "Discovery endpoint OK"
  echo "$response" | jq '{
    issuer,
    authorization_endpoint,
    token_endpoint,
    jwks_uri,
    response_types_supported,
    id_token_signing_alg_values_supported,
    scopes_supported
  }'

  return 0
}

# Verify JWKS endpoint
verify_jwks() {
  log_step "Verifying JWKS Endpoint"

  local api_base="${API_BASE_URL:-http://localhost:18080}"
  local issuer="${OIDC_ISSUER:-${api_base}}"

  log_info "GET ${issuer}/.well-known/jwks.json"

  local response
  response=$(curl -s "${issuer}/.well-known/jwks.json")

  if ! echo "$response" | jq -e '.keys' &> /dev/null; then
    log_error "JWKS endpoint returned invalid JSON"
    echo "$response"
    return 1
  fi

  local key_count
  key_count=$(echo "$response" | jq '.keys | length')

  if [[ "$key_count" -eq 0 ]]; then
    log_error "JWKS contains no keys"
    return 1
  fi

  log_success "JWKS endpoint OK ($key_count keys)"
  echo "$response" | jq '.keys[] | {kid, kty, alg, use}'

  return 0
}

# Main execution
main() {
  log_info "OIDC Setup Script"
  log_info "Production mode: $PRODUCTION_MODE"
  echo ""

  # Check required commands
  require_command openssl
  require_command psql
  require_command curl
  require_command jq

  # Step 1: Generate KEK
  if ! generate_kek; then
    log_error "KEK setup failed"
    exit 1
  fi

  # Step 2: Check PostgreSQL
  if ! check_postgres; then
    log_error "PostgreSQL check failed"
    exit 1
  fi

  # Step 3: Check migrations
  if ! check_migrations; then
    log_error "Migration check failed"
    exit 1
  fi

  # Step 4: Check existing keys
  if ! check_existing_keys; then
    log_error "Existing key check failed"
    exit 1
  fi

  # Step 5: Generate signing keys
  if ! generate_signing_keys; then
    log_error "Key generation failed"
    exit 1
  fi

  # Step 6: Verify discovery
  if ! verify_discovery; then
    log_warn "Discovery verification failed (non-fatal if server not running)"
  fi

  # Step 7: Verify JWKS
  if ! verify_jwks; then
    log_warn "JWKS verification failed (non-fatal if server not running)"
  fi

  echo ""
  log_success "OIDC setup complete!"
  echo ""
  log_info "Next steps:"
  echo "  1. Ensure TOTP_ENCRYPTION_KEY is persisted in .env"
  echo "  2. Restart sub2api service"
  echo "  3. Run ./scripts/oidc-verify.sh to validate production deployment"
  echo ""
}

main "$@"
