package geo

import (
	"testing"

	"github.com/openpaily/paily-check/internal/interfaces"
)

func TestNativeGeoEvidenceCarriesOnlyValidatedEgressFields(t *testing.T) {
	evidence := NativeGeoEvidence{Info: &interfaces.GeoInfo{IP: "203.0.113.7", CountryCode: "US", ASN: 64500, ASNOrg: "Example"}, Provider: "ipwhois"}
	result := nativeGeoResult(evidence)
	if result.EgressIP == "" || result.EgressASN != 64500 || result.EgressProvider != "ipwhois" {
		t.Fatalf("unexpected evidence: %+v", result)
	}
}

func TestNativeGeoEvidenceRejectsInvalidIP(t *testing.T) {
	evidence := NativeGeoEvidence{Info: &interfaces.GeoInfo{IP: "not-an-ip", CountryCode: "US"}, Provider: "myip"}
	if evidence.Info == nil || evidence.Info.CountryCode == "" {
		t.Fatal("fixture missing country")
	}
	result := nativeGeoResult(evidence)
	if result.EgressIP != "" {
		t.Fatalf("invalid IP must not be persisted: %+v", result)
	}
}

func TestNativeGeoResultWithoutASNLeavesASNUnknown(t *testing.T) {
	result := nativeGeoResult(NativeGeoEvidence{Info: &interfaces.GeoInfo{IP: "2001:db8::7", CountryCode: "US"}, Provider: "myip"})
	if result.EgressIP == "" || result.EgressASN != 0 || result.EgressASNOrg != "" {
		t.Fatalf("missing ASN must remain unknown: %+v", result)
	}
}
