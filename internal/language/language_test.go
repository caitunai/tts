package language

import "testing"

func TestNormalizeAcceptsCommonLanguageStandards(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"eng", "en"},
		{"en_US", "en-US"},
		{"English", "en"},
		{"zho", "zh"},
		{"cmn-Hans-CN", "cmn-Hans-CN"},
		{"Chinese", "zh"},
		{"yue-Hant-HK", "yue-Hant-HK"},
		{"Chinese,Yue", "yue"},
		{"zh-yue", "yue"},
		{"amh", "am"},
		{"akk", "akk"},
		{"swa", "sw"},
		{"jpn_JP", "ja-JP"},
		{"fra", "fr"},
		{"deu-DE", "de-DE"},
		{"Auto", "auto"},
	}

	for _, tt := range tests {
		if got := Normalize(tt.input); got != tt.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPrimary(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"eng-US", "en"},
		{"cmn-Hans-CN", "cmn"},
		{"yue-Hant-HK", "yue"},
		{"auto", "auto"},
	}

	for _, tt := range tests {
		if got := Primary(tt.input); got != tt.want {
			t.Fatalf("Primary(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWithDefaultRegion(t *testing.T) {
	defaults := map[string]string{
		"en": "US",
		"zh": "CN",
		"ja": "JP",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"eng", "en-US"},
		{"en-GB", "en-GB"},
		{"zho", "zh-CN"},
		{"cmn-Hans-CN", "cmn-Hans-CN"},
		{"jpn", "ja-JP"},
		{"auto", "auto"},
	}

	for _, tt := range tests {
		if got := WithDefaultRegion(tt.input, defaults); got != tt.want {
			t.Fatalf("WithDefaultRegion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
