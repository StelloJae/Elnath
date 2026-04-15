package locale

import "unicode"

type blockCounts struct {
	hangul   int
	hiragana int
	katakana int
	han      int
	latin    int
	cyrillic int
	arabic   int
	total    int
}

func DetectLanguage(text string) (lang string, confidence float64) {
	counts := countBlocks(text)
	if counts.total < 3 {
		return "en", 0
	}

	total := float64(counts.total)
	hangulRatio := float64(counts.hangul) / total
	hiraganaRatio := float64(counts.hiragana) / total
	katakanaRatio := float64(counts.katakana) / total
	hanRatio := float64(counts.han) / total
	latinRatio := float64(counts.latin) / total
	jaKanaRatio := hiraganaRatio + katakanaRatio

	if hangulRatio > 0.3 {
		return "ko", hangulRatio
	}
	if jaKanaRatio > 0.15 {
		conf := jaKanaRatio + hanRatio*0.3
		if conf > 1 {
			conf = 1
		}
		return "ja", conf
	}
	if hanRatio > 0.3 && jaKanaRatio < 0.05 {
		return "zh", hanRatio
	}
	if latinRatio > 0.5 {
		return "en", latinRatio
	}

	return "en", 0
}

func countBlocks(text string) blockCounts {
	var counts blockCounts
	for _, r := range text {
		if !unicode.IsLetter(r) && !isHan(r) {
			continue
		}
		counts.total++

		switch {
		case isHangul(r):
			counts.hangul++
		case isHiragana(r):
			counts.hiragana++
		case isKatakana(r):
			counts.katakana++
		case isHan(r):
			counts.han++
		case isLatin(r):
			counts.latin++
		case isCyrillic(r):
			counts.cyrillic++
		case isArabic(r):
			counts.arabic++
		}
	}
	return counts
}

func isHangul(r rune) bool {
	return (r >= 0xAC00 && r <= 0xD7A3) || (r >= 0x1100 && r <= 0x11FF)
}

func isHiragana(r rune) bool {
	return r >= 0x3040 && r <= 0x309F
}

func isKatakana(r rune) bool {
	return r >= 0x30A0 && r <= 0x30FF
}

func isHan(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF
}

func isLatin(r rune) bool {
	return (r >= 0x41 && r <= 0x7A) || (r >= 0xC0 && r <= 0x24F)
}

func isCyrillic(r rune) bool {
	return r >= 0x0400 && r <= 0x04FF
}

func isArabic(r rune) bool {
	return r >= 0x0600 && r <= 0x06FF
}
