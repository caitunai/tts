package language

import (
	"slices"
	"strings"
	"unicode"

	iso639 "github.com/barbashov/iso639-3"
	bcp47 "golang.org/x/text/language"
)

const Auto = "auto"

var specialAliases = map[string]string{
	"":            "",
	"auto":        Auto,
	"automatic":   Auto,
	"cantonese":   "yue",
	"chinese,yue": "yue",
	"chinese-yue": "yue",
	"farsi":       "fa",
	"mandarin":    "zh",
	"putonghua":   "zh",
	"zh-yue":      "yue",
}

func Normalize(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	tag := strings.ReplaceAll(trimmed, "_", "-")
	key := strings.ToLower(tag)
	if alias, ok := specialAliases[key]; ok {
		return alias
	}

	if !strings.Contains(tag, "-") {
		if code := normalizeISOLanguage(tag); code != "" {
			return code
		}
		if parsed, ok := parseBCP47(tag); ok {
			return parsed
		}
		return canonicalizeTag(tag)
	}

	parts := strings.Split(tag, "-")
	if len(parts) == 0 {
		return canonicalizeTag(tag)
	}

	if primary := normalizeISOLanguage(parts[0]); primary != "" {
		parts[0] = primary
	} else {
		parts[0] = strings.ToLower(parts[0])
	}

	candidate := strings.Join(parts, "-")
	if parsed, ok := parseBCP47(candidate); ok {
		return parsed
	}
	return canonicalizeTag(candidate)
}

func Primary(input string) string {
	normalized := Normalize(input)
	if normalized == "" || normalized == Auto {
		return normalized
	}
	if before, _, ok := strings.Cut(normalized, "-"); ok {
		return before
	}
	return normalized
}

func WithDefaultRegion(input string, defaults map[string]string) string {
	normalized := Normalize(input)
	if normalized == "" || normalized == Auto {
		return normalized
	}
	if hasRegion(normalized) {
		return normalized
	}

	primary := Primary(normalized)
	region := strings.ToUpper(defaults[primary])
	if region == "" {
		return normalized
	}

	parts := strings.Split(normalized, "-")
	insertAt := 1
	if len(parts) > 1 && isScript(parts[1]) {
		insertAt = 2
	}
	parts = append(parts, "")
	copy(parts[insertAt+1:], parts[insertAt:])
	parts[insertAt] = region

	if parsed, ok := parseBCP47(strings.Join(parts, "-")); ok {
		return parsed
	}
	return canonicalizeTag(strings.Join(parts, "-"))
}

func normalizeISOLanguage(value string) string {
	code := strings.ToLower(strings.TrimSpace(value))
	if alias, ok := specialAliases[code]; ok {
		return alias
	}

	if lang := iso639.FromAnyCode(code); lang != nil {
		return preferredISOCode(lang)
	}
	if lang := iso639.FromName(value); lang != nil {
		return preferredISOCode(lang)
	}

	return ""
}

func preferredISOCode(lang *iso639.Language) string {
	if lang == nil {
		return ""
	}
	if lang.Part1 != "" {
		return strings.ToLower(lang.Part1)
	}
	if lang.Part3 != "" {
		return strings.ToLower(lang.Part3)
	}
	if lang.Part2T != "" {
		return strings.ToLower(lang.Part2T)
	}
	if lang.Part2B != "" {
		return strings.ToLower(lang.Part2B)
	}
	return ""
}

func parseBCP47(tag string) (string, bool) {
	parsed, err := bcp47.Parse(tag)
	if err != nil {
		return "", false
	}
	value := parsed.String()
	return value, value != "" && value != "und"
}

func canonicalizeTag(tag string) string {
	parts := strings.Split(tag, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		switch {
		case i == 0:
			parts[i] = strings.ToLower(part)
		case isScript(part):
			parts[i] = titleASCII(part)
		case isRegion(part):
			parts[i] = strings.ToUpper(part)
		default:
			parts[i] = strings.ToLower(part)
		}
	}
	return strings.Join(parts, "-")
}

func hasRegion(tag string) bool {
	parts := strings.Split(tag, "-")
	return slices.ContainsFunc(parts[1:], isRegion)
}

func isScript(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, ch := range value {
		if !unicode.IsLetter(ch) {
			return false
		}
	}
	return true
}

func isRegion(value string) bool {
	if len(value) == 2 {
		for _, ch := range value {
			if !unicode.IsLetter(ch) {
				return false
			}
		}
		return true
	}
	if len(value) == 3 {
		for _, ch := range value {
			if !unicode.IsDigit(ch) {
				return false
			}
		}
		return true
	}
	return false
}

func titleASCII(value string) string {
	lower := strings.ToLower(value)
	return strings.ToUpper(lower[:1]) + lower[1:]
}
