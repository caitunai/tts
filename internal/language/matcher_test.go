package language

import "testing"

func TestLanguageExposesNormalizedIdentity(t *testing.T) {
	lang := Parse("deu-Latn-DE")

	if got := lang.String(); got != "de-Latn-DE" {
		t.Fatalf("String() = %q, want %q", got, "de-Latn-DE")
	}
	if got := lang.Primary(); got != "de" {
		t.Fatalf("Primary() = %q, want %q", got, "de")
	}
	if got := lang.ISO6391(); got != "de" {
		t.Fatalf("ISO6391() = %q, want %q", got, "de")
	}
	if got := lang.ISO6393(); got != "deu" {
		t.Fatalf("ISO6393() = %q, want %q", got, "deu")
	}
	if got := lang.Script(); got != "Latn" {
		t.Fatalf("Script() = %q, want %q", got, "Latn")
	}
	if got := lang.Region(); got != "DE" {
		t.Fatalf("Region() = %q, want %q", got, "DE")
	}
}

func TestLanguageMatchesAtDifferentGranularities(t *testing.T) {
	enUS := Parse("eng-Latn-US")
	enGB := Parse("en-Latn-GB")
	enUSWithoutScript := Parse("en-US")

	if !enUS.Matches(enGB, MatchLanguage) {
		t.Fatal("English tags should match at language granularity")
	}
	if !enUS.Matches(enGB, MatchLanguageScript) {
		t.Fatal("English tags should match at language-script granularity")
	}
	if enUS.Matches(enGB, MatchLanguageRegion) {
		t.Fatal("different regions must not match at language-region granularity")
	}
	if enUS.Matches(enGB, MatchExact) {
		t.Fatal("different tags must not match exactly")
	}
	if !enUS.Matches(enUSWithoutScript, MatchLanguageRegion) {
		t.Fatal("script should be ignored at language-region granularity")
	}
	if enUS.Matches(enUSWithoutScript, MatchLanguageScript) {
		t.Fatal("missing script must not match at language-script granularity")
	}
	if Parse("zh").Matches(Parse("cmn"), MatchLanguage) {
		t.Fatal("ISO-639 macrolanguage and individual language must remain distinct")
	}
}

func TestMapperSupportsGroupsAndSpecificity(t *testing.T) {
	mapper := NewMapper(
		Map("English", MatchLanguage, "en"),
		Map("British English", MatchExact, "en-GB"),
		Map("Chinese", MatchLanguage, "zh", "cmn", "yue"),
	)

	tests := []struct {
		input string
		want  string
	}{
		{"eng-US", "English"},
		{"English", "English"},
		{"en-GB", "British English"},
		{"zho-Hans-CN", "Chinese"},
		{"cmn-Hans-CN", "Chinese"},
		{"yue-Hant-HK", "Chinese"},
	}

	for _, tt := range tests {
		if got := mapper.Resolve(tt.input, "Auto"); got != tt.want {
			t.Fatalf("Resolve(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	if got := mapper.Resolve("ja", "Auto"); got != "Auto" {
		t.Fatalf("Resolve(%q) = %q, want %q", "ja", got, "Auto")
	}
}
