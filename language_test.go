package tts

import "testing"

func TestPublicLanguageFacade(t *testing.T) {
	lang := ParseLanguage("eng-Latn-US")
	if got := lang.String(); got != "en-Latn-US" {
		t.Fatalf("String() = %q, want %q", got, "en-Latn-US")
	}
	if got := lang.ISO6393(); got != "eng" {
		t.Fatalf("ISO6393() = %q, want %q", got, "eng")
	}
	if !lang.Matches(ParseLanguage("English-Latn-GB"), LanguageMatchLanguageScript) {
		t.Fatal("public language values should match by language and script")
	}
}
