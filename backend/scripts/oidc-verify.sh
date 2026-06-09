#!/usr/bin/env bash
#
# OIDC Verification Script
# Validates production OIDC deployment
#
# Usage:
#   ./scripts/oidc-verify.sh [--issuer=https://api.example.com] [--verbose]
#

set -euo pipefail

# Colors
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly NC='\033[0m'

# Configuration
ISSUER="${OIDC_ISSUER:-http://localhost:18080}"
VERBOSE=false
FAILURES=0

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --issuer=*)
      ISSUER="${1#*=}"
      shift
      ;;
    --verbose)
      VERBOSE=true
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
  echo -e "${GREEN}[✓]${NC} $*"
}

log_fail() {
  echo -e "${RED}[✗]${NC} $*"
  ((FAILURES++))
}

log_warn() {
  echo -e "${YELLOW}[!]${NC} $*"
}

log_test() {
  echo -e "${CYAN}[TEST]${NC} $*"
}

log_detail() {
  if [[ "$VERBOSE" == true ]]; then
    echo -e "${NC}      $*${NC}"
  fi
}

# Make HTTP request with error handling
http_get() {
  local url=$1
  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" -L "$url" 2>&1) || {
    echo "CURL_ERROR"
    return 1
  }

  http_code=$(echo "$response" | tail -n1)
  local body
  body=$(echo "$response" | sed '$d')

  if [[ "$http_code" != "200" ]]; then
    log_detail "HTTP $http_code: $url"
    if [[ "$VERBOSE" == true ]]; then
      echo "$body"
    fi
    return 1
  fi

  echo "$body"
  return 0
}

# Verify OIDC discovery metadata
verify_discovery() {
  log_test "OIDC Discovery Metadata"

  local url="${ISSUER}/.well-known/openid-configuration"
  log_detail "GET $url"

  local response
  if ! response=$(http_get "$url"); then
    log_fail "Discovery endpoint unreachable"
    return 1
  fi

  if ! echo "$response" | jq -e . &> /dev/null; then
    log_fail "Discovery returned invalid JSON"
    log_detail "$response"
    return 1
  fi

  # Check required fields per RFC 8414
  local required_fields=(
    "issuer"
    "authorization_endpoint"
    "token_endpoint"
    "jwks_uri"
    "response_types_supported"
    "subject_types_supported"
    "id_token_signing_alg_values_supported"
  )

  local missing_fields=()
  for field in "${required_fields[@]}"; do
    if ! echo "$response" | jq -e ".$field" &> /dev/null; then
      missing_fields+=("$field")
    fi
  done

  if [[ ${#missing_fields[@]} -gt 0 ]]; then
    log_fail "Discovery missing required fields: ${missing_fields[*]}"
    return 1
  fi

  # Validate issuer consistency
  local discovered_issuer
  discovered_issuer=$(echo "$response" | jq -r '.issuer')

  if [[ "$discovered_issuer" != "$ISSUER" ]]; then
    log_fail "Issuer mismatch: expected $ISSUER, got $discovered_issuer"
    return 1
  fi

  # Check supported algorithms
  local supported_algs
  supported_algs=$(echo "$response" | jq -r '.id_token_signing_alg_values_supported | @csv')

  if [[ ! "$supported_algs" =~ RS256 ]]; then
    log_warn "RS256 not in supported algorithms: $supported_algs"
  fi

  # Check scopes
  local scopes_supported
  scopes_supported=$(echo "$response" | jq -r '.scopes_supported // [] | @csv')

  local required_scopes=("openid" "profile" "email")
  for scope in "${required_scopes[@]}"; do
    if [[ ! "$scopes_supported" =~ $scope ]]; then
      log_warn "Required scope not advertised: $scope"
    fi
  done

  log_success "Discovery metadata valid"

  if [[ "$VERBOSE" == true ]]; then
    echo "$response" | jq '{
      issuer,
      authorization_endpoint,
      token_endpoint,
      jwks_uri,
      response_types_supported,
      subject_types_supported,
      id_token_signing_alg_values_supported,
      scopes_supported,
      grant_types_supported,
      token_endpoint_auth_methods_supported,
      code_challenge_methods_supported
    }'
  fi

  return 0
}

# Verify JWKS endpoint
verify_jwks() {
  log_test "JWKS Endpoint"

  local url="${ISSUER}/.well-known/jwks.json"
  log_detail "GET $url"

  local response
  if ! response=$(http_get "$url"); then
    log_fail "JWKS endpoint unreachable"
    return 1
  fi

  if ! echo "$response" | jq -e '.keys' &> /dev/null; then
    log_fail "JWKS returned invalid structure"
    log_detail "$response"
    return 1
  fi

  local key_count
  key_count=$(echo "$response" | jq '.keys | length')

  if [[ "$key_count" -eq 0 ]]; then
    log_fail "JWKS contains no keys"
    return 1
  fi

  log_detail "Found $key_count signing keys"

  # Validate each key structure
  local valid_keys=0
  local i=0
  while [[ $i -lt $key_count ]]; do
    local key
    key=$(echo "$response" | jq ".keys[$i]")

    local kid kty alg use

    kid=$(echo "$key" | jq -r '.kid // empty')
    kty=$(echo "$key" | jq -r '.kty // empty')
    alg=$(echo "$key" | jq -r '.alg // empty')
    use=$(echo "$key" | jq -r '.use // empty')

    if [[ -z "$kid" ]]; then
      log_warn "Key $i missing kid"
    fi

    if [[ -z "$kty" ]]; then
      log_warn "Key $i missing kty"
    elif [[ ! "$kty" =~ ^(RSA|EC)$ ]]; then
      log_warn "Key $i has unsupported kty: $kty"
    fi

    if [[ "$use" != "sig" ]]; then
      log_warn "Key $i has unexpected use: $use (expected 'sig')"
    fi

    # Check algorithm-specific fields
    if [[ "$kty" == "RSA" ]]; then
      local n e
      n=$(echo "$key" | jq -r '.n // empty')
      e=$(echo "$key" | jq -r '.e // empty')

      if [[ -z "$n" || -z "$e" ]]; then
        log_warn "RSA key $kid missing modulus (n) or exponent (e)"
      else
        ((valid_keys++))
      fi
    elif [[ "$kty" == "EC" ]]; then
      local crv x y
      crv=$(echo "$key" | jq -r '.crv // empty')
      x=$(echo "$key" | jq -r '.x // empty')
      y=$(echo "$key" | jq -r '.y // empty')

      if [[ -z "$crv" || -z "$x" || -z "$y" ]]; then
        log_warn "EC key $kid missing curve/coordinates (crv, x, y)"
      else
        ((valid_keys++))
      fi
    fi

    if [[ "$VERBOSE" == true ]]; then
      log_detail "Key: kid=$kid, kty=$kty, alg=$alg, use=$use"
    fi

    ((i++))
  done

  if [[ $valid_keys -eq 0 ]]; then
    log_fail "No valid signing keys in JWKS"
    return 1
  fi

  log_success "JWKS valid ($valid_keys/$key_count keys usable)"

  return 0
}

# Verify authorization endpoint is reachable
verify_authorization_endpoint() {
  log_test "Authorization Endpoint"

  local url="${ISSUER}/oauth/authorize"
  log_detail "GET $url (expect HTML)"

  # Authorization endpoint should return HTML, not JSON
  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" -L "$url" 2>&1) || {
    log_fail "Authorization endpoint unreachable"
    return 1
  }

  http_code=$(echo "$response" | tail -n1)

  # 400 is acceptable (missing required params)
  # 200 with HTML is ideal
  if [[ "$http_code" =~ ^(200|400)$ ]]; then
    log_success "Authorization endpoint reachable (HTTP $http_code)"
    return 0
  else
    log_fail "Authorization endpoint returned HTTP $http_code"
    return 1
  fi
}

# Verify token endpoint is reachable
verify_token_endpoint() {
  log_test "Token Endpoint"

  local url="${ISSUER}/oauth/token"
  log_detail "POST $url (expect 400 invalid_request)"

  # Token endpoint should reject empty POST with 400
  local response
  local http_code

  response=$(curl -s -w "\n%{http_code}" -X POST "$url" 2>&1) || {
    log_fail "Token endpoint unreachable"
    return 1
  }

  http_code=$(echo "$response" | tail -n1)
  local body
  body=$(echo "$response" | sed '$d')

  # Expect 400 for missing grant_type
  if [[ "$http_code" == "400" ]]; then
    log_success "Token endpoint reachable (correctly rejects invalid request)"
    return 0
  elif [[ "$http_code" == "200" ]]; then
    log_warn "Token endpoint returned 200 for empty POST (unexpected)"
    return 0
  else
    log_fail "Token endpoint returned HTTP $http_code"
    log_detail "$body"
    return 1
  fi
}

# Check database scopes (requires psql)
verify_database_scopes() {
  log_test "Database OIDC Scopes"

  if ! command -v psql &> /dev/null; then
    log_warn "psql not found, skipping database checks"
    return 0
  fi

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if [[ -z "${POSTGRES_PASSWORD:-}" ]]; then
    log_warn "POSTGRES_PASSWORD not set, skipping database checks"
    return 0
  fi

  log_detail "Checking oauth_scopes table"

  local scope_check
  scope_check=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT scope FROM oauth_scopes WHERE scope IN ('openid', 'profile', 'email') ORDER BY scope;" 2>&1) || {
    log_warn "Could not query database: $scope_check"
    return 0
  }

  local found_scopes
  found_scopes=$(echo "$scope_check" | tr '\n' ',' | sed 's/,$//')

  if [[ "$found_scopes" =~ openid ]] && [[ "$found_scopes" =~ profile ]] && [[ "$found_scopes" =~ email ]]; then
    log_success "OIDC scopes registered in database"
    log_detail "Scopes: $found_scopes"
    return 0
  else
    log_fail "OIDC scopes incomplete in database: $found_scopes"
    return 1
  fi
}

# Check for signing keys in database
verify_database_keys() {
  log_test "Database Signing Keys"

  if ! command -v psql &> /dev/null; then
    log_warn "psql not found, skipping database checks"
    return 0
  fi

  local db_host="${POSTGRES_HOST:-localhost}"
  local db_port="${POSTGRES_PORT:-5432}"
  local db_name="${POSTGRES_DB:-sub2api}"
  local db_user="${POSTGRES_USER:-sub2api}"

  if [[ -z "${POSTGRES_PASSWORD:-}" ]]; then
    log_warn "POSTGRES_PASSWORD not set, skipping database checks"
    return 0
  fi

  log_detail "Checking oidc_signing_keys table"

  local key_count
  key_count=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
    -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
    -tAc "SELECT COUNT(*) FROM oidc_signing_keys WHERE deleted_at IS NULL;" 2>&1) || {
    log_warn "Could not query database: $key_count"
    return 0
  }

  if [[ "$key_count" =~ ^[0-9]+$ ]] && [[ "$key_count" -gt 0 ]]; then
    log_success "Found $key_count active signing keys in database"

    local current_kid
    current_kid=$(PGPASSWORD="${POSTGRES_PASSWORD}" psql \
      -h "$db_host" -p "$db_port" -U "$db_user" -d "$db_name" \
      -tAc "SELECT value FROM settings WHERE key = 'oidc_current_kid';" 2>&1) || {
      log_warn "Could not query current_kid from settings"
      return 0
    }

    if [[ -n "$current_kid" ]]; then
      log_detail "Current active KID: $current_kid"
    else
      log_warn "No oidc_current_kid set in settings"
    fi

    return 0
  else
    log_fail "No active signing keys found in database"
    return 1
  fi
}

# Verify KEK is set
# This KEK is provided by TOTP_ENCRYPTION_KEY (the var the code actually reads,
# cfg.Totp.EncryptionKey). It protects the OIDC signing keys, TOTP secrets,
# monitor, and backup secrets; rotating it invalidates all of them at once.
verify_kek() {
  log_test "Key Encryption Key (KEK)"

  if [[ -z "${TOTP_ENCRYPTION_KEY:-}" ]]; then
    log_fail "TOTP_ENCRYPTION_KEY not set"
    log_detail "Set this environment variable to decrypt signing keys"
    return 1
  fi

  local kek_len=${#TOTP_ENCRYPTION_KEY}

  if [[ $kek_len -lt 64 ]]; then
    log_fail "TOTP_ENCRYPTION_KEY too short ($kek_len chars, need 64)"
    return 1
  fi

  log_success "KEK configured ($kek_len chars)"
  return 0
}

# Main execution
main() {
  echo -e "${BLUE}╔════════════════════════════════════════╗${NC}"
  echo -e "${BLUE}║  OIDC Production Verification Script  ║${NC}"
  echo -e "${BLUE}╚════════════════════════════════════════╝${NC}"
  echo ""
  log_info "Issuer: $ISSUER"
  log_info "Verbose: $VERBOSE"
  echo ""

  # Run all verification tests
  verify_kek
  verify_discovery
  verify_jwks
  verify_authorization_endpoint
  verify_token_endpoint
  verify_database_scopes
  verify_database_keys

  echo ""
  echo -e "${BLUE}════════════════════════════════════════${NC}"

  if [[ $FAILURES -eq 0 ]]; then
    log_success "All checks passed!"
    echo ""
    log_info "OIDC provider is production-ready"
    exit 0
  else
    log_fail "$FAILURES check(s) failed"
    echo ""
    log_warn "Review failures above before deploying to production"
    exit 1
  fi
}

main "$@"
