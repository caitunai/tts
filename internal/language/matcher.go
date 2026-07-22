package language

import (
	"strings"

	iso639 "github.com/barbashov/iso639-3"
)

// MatchLevel controls how much of a language tag must match a mapping rule.
type MatchLevel uint8

const (
	// MatchLanguage compares only the ISO language identity and ignores script,
	// region, variants, and extensions.
	MatchLanguage MatchLevel = iota + 1
	// MatchLanguageScript compares the ISO language identity and script.
	MatchLanguageScript
	// MatchLanguageRegion compares the ISO language identity and region.
	MatchLanguageRegion
	// MatchExact compares the complete normalized language tag.
	MatchExact
)

// Language is a normalized language value with its ISO and BCP-47 parts.
type Language struct {
	tag     string
	primary string
	iso6391 string
	iso6393 string
	script  string
	region  string
}

// Parse creates a Language from an ISO-639 code, BCP-47 tag, or language name.
func Parse(input string) Language {
	tag := Normalize(input)
	language := Language{
		tag:     tag,
		primary: Primary(tag),
	}
	if tag == "" || tag == Auto {
		return language
	}

	if iso := iso639.FromAnyCode(language.primary); iso != nil {
		language.iso6391 = iso.Part1
		language.iso6393 = iso.Part3
	}

	parts := splitTag(tag)
	for _, part := range parts[1:] {
		switch {
		case language.script == "" && isScript(part):
			language.script = titleASCII(part)
		case language.region == "" && isRegion(part):
			language.region = canonicalRegion(part)
		}
	}
	return language
}

// String returns the normalized BCP-47-style representation.
func (l Language) String() string {
	return l.tag
}

// Primary returns the normalized primary language subtag.
func (l Language) Primary() string {
	return l.primary
}

// ISO6391 returns the two-letter ISO-639-1 code when one exists.
func (l Language) ISO6391() string {
	return l.iso6391
}

// ISO6393 returns the three-letter ISO-639-3 code when one exists.
func (l Language) ISO6393() string {
	return l.iso6393
}

// Script returns the normalized ISO-15924 script subtag.
func (l Language) Script() string {
	return l.script
}

// Region returns the normalized ISO-3166-1 or UN M49 region subtag.
func (l Language) Region() string {
	return l.region
}

// Matches reports whether two languages match at the requested granularity.
func (l Language) Matches(other Language, level MatchLevel) bool {
	if l.tag == "" || other.tag == "" {
		return false
	}
	if level == MatchExact {
		return l.tag == other.tag
	}
	if !l.sameLanguage(other) {
		return false
	}

	switch level {
	case MatchLanguage:
		return true
	case MatchLanguageScript:
		return l.script == other.script
	case MatchLanguageRegion:
		return l.region == other.region
	default:
		return false
	}
}

func (l Language) sameLanguage(other Language) bool {
	if l.iso6393 != "" && other.iso6393 != "" {
		return l.iso6393 == other.iso6393
	}
	return l.primary != "" && l.primary == other.primary
}

// Mapping maps one or more language patterns to a provider-specific value.
type Mapping[T any] struct {
	value     T
	level     MatchLevel
	languages []Language
}

// Map creates a provider language mapping rule.
func Map[T any](value T, level MatchLevel, languages ...string) Mapping[T] {
	rule := Mapping[T]{
		value:     value,
		level:     level,
		languages: make([]Language, 0, len(languages)),
	}
	for _, item := range languages {
		rule.languages = append(rule.languages, Parse(item))
	}
	return rule
}

// Mapper resolves normalized languages to provider-specific values.
type Mapper[T any] struct {
	rules []Mapping[T]
}

// NewMapper creates a mapper. More specific rules win; declaration order is
// used when matching rules have the same granularity.
func NewMapper[T any](rules ...Mapping[T]) Mapper[T] {
	return Mapper[T]{rules: append([]Mapping[T](nil), rules...)}
}

// Lookup resolves input using the mapper's rules.
func (m Mapper[T]) Lookup(input string) (T, bool) {
	return m.LookupLanguage(Parse(input))
}

// LookupLanguage resolves an already parsed Language value.
func (m Mapper[T]) LookupLanguage(input Language) (T, bool) {
	var zero T
	bestSpecificity := 0
	var best T
	found := false

	for _, rule := range m.rules {
		if rule.level < MatchLanguage || rule.level > MatchExact {
			continue
		}
		for _, candidate := range rule.languages {
			if !input.Matches(candidate, rule.level) {
				continue
			}
			specificity := rule.level.specificity()
			if !found || specificity > bestSpecificity {
				best = rule.value
				bestSpecificity = specificity
				found = true
			}
			break
		}
	}
	if !found {
		return zero, false
	}
	return best, true
}

func (l MatchLevel) specificity() int {
	switch l {
	case MatchExact:
		return 3
	case MatchLanguageScript, MatchLanguageRegion:
		return 2
	case MatchLanguage:
		return 1
	default:
		return 0
	}
}

// Resolve returns the mapped value or fallback when no rule matches.
func (m Mapper[T]) Resolve(input string, fallback T) T {
	if value, ok := m.Lookup(input); ok {
		return value
	}
	return fallback
}

func splitTag(tag string) []string {
	if tag == "" {
		return nil
	}
	return strings.Split(tag, "-")
}

func canonicalRegion(region string) string {
	if len(region) == 2 {
		return strings.ToUpper(region)
	}
	return region
}
