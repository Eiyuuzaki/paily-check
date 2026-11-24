package geo

import (
	"testing"
)

// ipinfo returns the ISO code in "country" and a region name in "region", and
// no country name at all. Filling Country from Region put "California" in a
// field documented as the country name.
func TestParseIPInfoUsesCountryAsCode(t *testing.T) {
	body := []byte(`{"ip":"203.0.113.9","country":"US","region":"California","loc":"34.05,-118.24","org":"AS64500 Example","timezone":"America/Los_Angeles"}`)

	info, err := parseIPInfo(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.CountryCode != "US" {
		t.Fatalf("CountryCode = %q, want US", info.CountryCode)
	}
	if info.Country != "" {
		t.Fatalf("Country = %q, want empty (ipinfo supplies no country name)", info.Country)
	}
	if info.Org == "" {
		t.Fatal("Org should be carried through")
	}
	if info.Lat == 0 || info.Lon == 0 {
		t.Fatalf("loc was not parsed: lat=%v lon=%v", info.Lat, info.Lon)
	}
}

// A padded answer must be trimmed before the two-letter check, or a valid code
// arriving as " US " would be discarded.
func TestFirstNonEmptyTrims(t *testing.T) {
	if got := firstNonEmpty("", "  ", " US "); got != "US" {
		t.Fatalf("firstNonEmpty = %q, want US", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty = %q, want empty", got)
	}
	if got := firstNonEmpty(" DE ", "US"); got != "DE" {
		t.Fatalf("firstNonEmpty = %q, want DE", got)
	}
}

func TestParseIdentMeFallsBackToCountryCodeKey(t *testing.T) {
	body := []byte(`{"ip":"203.0.113.9","country_code":"de","tz":"Europe/Berlin"}`)

	info, err := parseIdentMe(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.CountryCode != "DE" {
		t.Fatalf("CountryCode = %q, want DE", info.CountryCode)
	}
}

// A provider that answers with an error body must not be read as a country.
func TestParsersRejectErrorPayloads(t *testing.T) {
	if _, err := parseIPWhoIs([]byte(`{"success":false}`)); err == nil {
		t.Fatal("ipwhois failure payload must be an error")
	}
	if _, err := parseIPAPI([]byte(`{"status":"fail"}`)); err == nil {
		t.Fatal("ip-api failure payload must be an error")
	}
	if _, err := parseIPAPICo([]byte(`{"error":true}`)); err == nil {
		t.Fatal("ipapi.co failure payload must be an error")
	}
	if _, err := parseMyIP([]byte(`{"cc":"XX"}`)); err == nil {
		t.Fatal("myip unknown country must be an error")
	}
}

// Only a real two-letter code survives; the empty string and placeholders do not.
func TestIsValidCountryCode(t *testing.T) {
	for _, code := range []string{"US", "de", "Hk"} {
		if !isValidCountryCode(code) {
			t.Fatalf("%q should be valid", code)
		}
	}
	for _, code := range []string{"", "XX", "USA", "U1", " U"} {
		if isValidCountryCode(code) {
			t.Fatalf("%q should be rejected", code)
		}
	}
}

// A malformed body is an error, not an empty success.
func TestParsersRejectGarbage(t *testing.T) {
	if _, err := parseIPInfo([]byte("<html>nope</html>")); err == nil {
		t.Fatal("garbage must be an error")
	}
	if _, err := parseIPSB([]byte("not json")); err == nil {
		t.Fatal("garbage must be an error")
	}
}
