package service

import (
	"testing"
	"time"
)

func TestBuildUserInfoJWTClaims_IncludesIssAndAud(t *testing.T) {
	claims := BuildUserInfoJWTClaims(
		"https://sub.sakrylle.com", "client-a",
		"42", "alice", "alice@example.com", true,
		time.Unix(1_700_000_000, 0), 0,
	)
	if claims["iss"] != "https://sub.sakrylle.com" {
		t.Fatalf("iss = %v, want issuer", claims["iss"])
	}
	aud, ok := claims["aud"].([]string)
	if !ok || len(aud) != 1 || aud[0] != "client-a" {
		t.Fatalf("aud = %v, want [client-a]", claims["aud"])
	}
	if claims["email_verified"] != true {
		t.Fatalf("email_verified = %v, want true", claims["email_verified"])
	}
	if claims["sub"] != "42" {
		t.Fatalf("sub = %v, want 42", claims["sub"])
	}
}
