#!/usr/bin/env bash
#
# OIDC Key Rotation Script
# Generates new signing key pairs while preserving old keys for grace period
#
# Usage:
#   ./scripts/oidc-key-rotate.sh [--grace-period=7d] [--algorithm=all|rs256|es256]
#

set -euo pipefail

# Colors
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m'

# Configuration
GRACE_PERIOD="7d"
ALGORITHM="all"
FORCE=false
API_BASE="${API_BASE_URL:-http://localhost:18080}"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --grace-period=*)
      GRACE_PERIOD="${1#*=}"
      shift
      ;;
    --algorithm=*)
      ALGORITHM="${1#*=}"
      if [[ ! "$ALGORITHM" =~ ^(all|rs256|es256)$ ]]; then
        echo -e "${RED}Invalid algorithm. Use: all, rs256, or es256${NC}" >&2
        exit 1
      fi
      shift
      ;;
    --force)
      FORCE=true
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

# Convert grace period to seconds
parse_duration() {
  local duration=$1
  local value="${duration%[a-z]}"
  local unit="${duration#$value}"

  case $unit in
    s) echo "$value" ;;
    m) echo $((value * 60)) ;;
    h) echo $((value * 3600)) ;;
    d) echo $((value * 86400)) ;;
    *)
      log_error "Invalid duration unit: $unit (use s/m/h/d)"
      exit 1
      ;;
  esac
}

# Check prerequisites
check_prerequisites() {
  log_step "Checking Prerequisites"

  if ! command -v curl &> /dev/null; then
    log_error "curl not found"
    exit 1
  fi

  if ! command -v jq &> /dev/null; then
    log_error "jq not found"
    exit 1
  fi

  if ! command -v psql &> /dev/null; then
    log_warn "psql not found, some checks will be skipped"
  fi

  if [[ -z "${ADMIN_TOKEN:-}" ]]; then
    log_error "ADMIN_TOKEN not set"
    log_info "Get admin token via: POST /api/v1/auth/login"
    exit 1
  fi

  # This KEK is provided by TOTP_ENCRYPTION_KEY (cfg.Totp.EncryptionKey), which
  # the code actually reads; it protects OIDC signing keys, TOTP, monitor, and
  # backup secrets. Rotating it invalidates all of them at once.
  if [[ -z "${TOTP_ENCRYPTION_KEY:-}" ]]; then
    log_error "TOTP_ENCRYPTION_KEY not set"
    exit 1
  fi

  log_success "Prerequisites OK"
}

# Get current signing keys
get_current_keys() {
  log_step "Fetching Current Signing Keys"

  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" \
    -X GET "${API_BASE}/api/v1/admin/oidc/keys" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}")

  http_code=$(echo "$response" | tail -n1)
  local body
  body=$(echo "$response" | sed '$d')

  if [[ "$http_code" != "200" ]]; then
    log_error "Failed to fetch current keys (HTTP $http_code)"
    echo "$body" | jq '.' 2>/dev/null || echo "$body"
    exit 1
  fi

  local key_count
  key_count=$(echo "$body" | jq '.data | length')

  log_info "Found $key_count active signing keys"

  if [[ "$key_count" -eq 0 ]]; then
    log_warn "No existing keys found. This is the initial key generation."
    if [[ "$FORCE" == false ]]; then
      read -r -p "$(echo -e "${YELLOW}Continue with initial key generation? (y/N): ${NC}")" confirm
      if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        log_info "Aborted by user"
        exit 0
      fi
    fi
  else
    echo "$body" | jq -r '.data[] | "  KID: \(.kid)  |  Algorithm: \(.algorithm)  |  Created: \(.created_at)"'
  fi

  echo ""
}

# Generate new signing keys
generate_new_keys() {
  log_step "Generating New Signing Keys"

  log_info "Algorithm: $ALGORITHM"

  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" \
    -X POST "${API_BASE}/api/v1/admin/oidc/keys/generate" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json")

  http_code=$(echo "$response" | tail -n1)
  local body
  body=$(echo "$response" | sed '$d')

  if [[ "$http_code" != "200" ]]; then
    log_error "Failed to generate new keys (HTTP $http_code)"
    echo "$body" | jq '.' 2>/dev/null || echo "$body"
    exit 1
  fi

  log_success "New signing keys generated"

  local rs256_kid
  local es256_kid
  rs256_kid=$(echo "$body" | jq -r '.data.rs256_kid // empty')
  es256_kid=$(echo "$body" | jq -r '.data.es256_kid // empty')

  if [[ -n "$rs256_kid" ]]; then
    log_success "New RS256 KID: $rs256_kid"
  fi
  if [[ -n "$es256_kid" ]]; then
    log_success "New ES256 KID: $es256_kid"
  fi

  # Store for later use
  NEW_RS256_KID="$rs256_kid"
  NEW_ES256_KID="$es256_kid"

  echo ""
}

# Update current_kid setting
update_current_kid() {
  log_step "Updating Current KID"

  # Prefer RS256 for backward compatibility
  local new_kid="${NEW_RS256_KID:-$NEW_ES256_KID}"

  if [[ -z "$new_kid" ]]; then
    log_error "No new KID available to set as current"
    exit 1
  fi

  log_info "Setting current_kid to: $new_kid"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if [[ -z "${POSTGRES_PASSWORD:-}" ]]; then
    log_error "POSTGRES_PASSWORD not set"
    exit 1
  fi

  PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -c "INSERT INTO settings (key, value, updated_at)
        VALUES ('oidc_current_kid', '$new_kid', NOW())
        ON CONFLICT (key) DO UPDATE
        SET value = EXCLUDED.value, updated_at = NOW();" || {
    log_error "Failed to update current_kid in database"
    exit 1
  }

  log_success "Current KID updated to $new_kid"
  echo ""
}

# Mark old keys for deletion after grace period
mark_old_keys_for_deletion() {
  log_step "Marking Old Keys for Deletion"

  local grace_seconds
  grace_seconds=$(parse_duration "$GRACE_PERIOD")

  log_info "Grace period: $GRACE_PERIOD ($grace_seconds seconds)"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if [[ -z "${POSTGRES_PASSWORD:-}" ]]; then
    log_error "POSTGRES_PASSWORD not set"
    exit 1
  fi

  # Get list of old keys (not the newly generated ones)
  local old_kids=()

  if [[ -n "${NEW_RS256_KID:-}" ]]; then
    old_kids+=("'$NEW_RS256_KID'")
  fi
  if [[ -n "${NEW_ES256_KID:-}" ]]; then
    old_kids+=("'$NEW_ES256_KID'")
  fi

  local where_clause="deleted_at IS NULL"
  if [[ ${#old_kids[@]} -gt 0 ]]; then
    local kid_list
    kid_list=$(IFS=,; echo "${old_kids[*]}")
    where_clause="deleted_at IS NULL AND kid NOT IN ($kid_list)"
  fi

  local old_key_count
  old_key_count=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT COUNT(*) FROM oidc_signing_keys WHERE $where_clause;")

  if [[ "$old_key_count" -eq 0 ]]; then
    log_info "No old keys to mark for deletion"
    echo ""
    return 0
  fi

  log_info "Marking $old_key_count old keys for deletion after grace period"

  PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -c "UPDATE oidc_signing_keys
        SET deleted_at = NOW() + INTERVAL '$grace_seconds seconds'
        WHERE $where_clause
        RETURNING kid, algorithm, deleted_at;" || {
    log_error "Failed to mark old keys for deletion"
    exit 1
  }

  log_success "Old keys will be deleted after $GRACE_PERIOD"
  echo ""
}

# Clean up expired keys
cleanup_expired_keys() {
  log_step "Cleaning Up Expired Keys"

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if [[ -z "${POSTGRES_PASSWORD:-}" ]]; then
    log_error "POSTGRES_PASSWORD not set"
    exit 1
  fi

  local expired_count
  expired_count=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT COUNT(*) FROM oidc_signing_keys WHERE deleted_at IS NOT NULL AND deleted_at <= NOW();")

  if [[ "$expired_count" -eq 0 ]]; then
    log_info "No expired keys to clean up"
    echo ""
    return 0
  fi

  log_info "Deleting $expired_count expired keys"

  PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -c "DELETE FROM oidc_signing_keys
        WHERE deleted_at IS NOT NULL AND deleted_at <= NOW()
        RETURNING kid, algorithm;" || {
    log_error "Failed to delete expired keys"
    exit 1
  }

  log_success "Expired keys deleted"
  echo ""
}

# Trigger JWKS cache refresh
refresh_jwks_cache() {
  log_step "Triggering JWKS Cache Refresh"

  # Access the JWKS endpoint to trigger cache rebuild
  local jwks_url="${API_BASE}/.well-known/jwks.json"

  log_info "GET $jwks_url"

  local response
  response=$(curl -s "$jwks_url")

  if echo "$response" | jq -e '.keys' &> /dev/null; then
    local key_count
    key_count=$(echo "$response" | jq '.keys | length')
    log_success "JWKS cache refreshed ($key_count keys)"
  else
    log_warn "JWKS endpoint returned unexpected response"
    echo "$response"
  fi

  echo ""
}

# Verify rotation success
verify_rotation() {
  log_step "Verifying Key Rotation"

  # Check JWKS contains new keys
  local jwks_url="${API_BASE}/.well-known/jwks.json"
  local response
  response=$(curl -s "$jwks_url")

  if ! echo "$response" | jq -e '.keys' &> /dev/null; then
    log_error "JWKS endpoint returned invalid response"
    return 1
  fi

  local jwks_kids
  jwks_kids=$(echo "$response" | jq -r '.keys[].kid' | sort)

  log_info "Keys in JWKS:"
  echo "$jwks_kids" | while read -r kid; do
    echo "  - $kid"
  done

  # Verify new keys are present
  if [[ -n "${NEW_RS256_KID:-}" ]]; then
    if echo "$jwks_kids" | grep -q "$NEW_RS256_KID"; then
      log_success "New RS256 key ($NEW_RS256_KID) is in JWKS"
    else
      log_error "New RS256 key ($NEW_RS256_KID) NOT in JWKS"
      return 1
    fi
  fi

  if [[ -n "${NEW_ES256_KID:-}" ]]; then
    if echo "$jwks_kids" | grep -q "$NEW_ES256_KID"; then
      log_success "New ES256 key ($NEW_ES256_KID) is in JWKS"
    else
      log_error "New ES256 key ($NEW_ES256_KID) NOT in JWKS"
      return 1
    fi
  fi

  echo ""
  log_success "Key rotation verified"
  return 0
}

# Main execution
main() {
  log_info "OIDC Key Rotation Script"
  log_info "Grace period: $GRACE_PERIOD"
  log_info "Algorithm: $ALGORITHM"
  echo ""

  if [[ "$FORCE" == false ]]; then
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${YELLOW}WARNING: This will rotate OIDC signing keys${NC}"
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "This operation will:"
    echo "  1. Generate new signing key pairs"
    echo "  2. Update the current signing key"
    echo "  3. Keep old keys active for $GRACE_PERIOD"
    echo "  4. Clean up expired keys"
    echo ""
    echo "Old tokens signed with previous keys will remain valid"
    echo "during the grace period."
    echo ""
    read -r -p "$(echo -e "${YELLOW}Continue? (y/N): ${NC}")" confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
      log_info "Aborted by user"
      exit 0
    fi
    echo ""
  fi

  check_prerequisites
  get_current_keys
  generate_new_keys
  update_current_kid
  mark_old_keys_for_deletion
  cleanup_expired_keys
  refresh_jwks_cache
  verify_rotation

  log_success "Key rotation complete!"
  echo ""
  log_info "Next steps:"
  echo "  1. Monitor for any validation errors in logs"
  echo "  2. After grace period ($GRACE_PERIOD), run this script again to clean up old keys"
  echo "  3. Consider running: ./scripts/oidc-verify.sh --issuer=${API_BASE}"
  echo ""
}

main "$@"
