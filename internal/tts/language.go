package tts

import langnorm "github.com/caitunai/tts/internal/language"

type Language = langnorm.Language
type LanguageMatchLevel = langnorm.MatchLevel

const (
	LanguageMatchLanguage       = langnorm.MatchLanguage
	LanguageMatchLanguageScript = langnorm.MatchLanguageScript
	LanguageMatchLanguageRegion = langnorm.MatchLanguageRegion
	LanguageMatchExact          = langnorm.MatchExact
)

func ParseLanguage(input string) Language {
	return langnorm.Parse(input)
}

func NormalizeLanguage(input string) string {
	return langnorm.Normalize(input)
}

func PrimaryLanguage(input string) string {
	return langnorm.Primary(input)
}
