package utils

import "unicode"

func SplitNameByWords(src string) []string {
	var runes [][]rune
	var afterDot []bool
	var lastClass, class int
	pendingDot := false

	// split into fields based on class of unicode character
	for _, r := range src {
		switch {
		case unicode.IsLower(r):
			class = 1
		case unicode.IsUpper(r):
			class = 2
		case unicode.IsDigit(r):
			class = 3
		case r == '.':
			// When we encounter a dot, force a new word segment to start
			// but don't include the dot itself
			lastClass = -1 // Use a special value to force a break
			pendingDot = true
			continue
		default:
			class = 4
		}

		// Don't split when going from uppercase to digit (S3 case)
		// lastClass == -1 is our special marker for a dot, which forces a break
		if (class == lastClass || (lastClass == 2 && class == 3)) && lastClass != -1 {
			sz := len(runes) - 1
			runes[sz] = append(runes[sz], r)
		} else {
			runes = append(runes, []rune{r})
			afterDot = append(afterDot, pendingDot)
			pendingDot = false
		}
		lastClass = class
	}

	// handle upper case -> lower case sequences, e.g.
	// "PDFL", "oader" -> "PDF", "Loader"
	// Skip the merge when the next word came right after a dot (e.g. "Map.primary"
	// must stay as ["Map", "primary"], not ["Ma", "pprimary"]).
	for i := range len(runes) - 1 {
		if afterDot[i+1] {
			continue
		}
		if unicode.IsUpper(runes[i][0]) && unicode.IsLower(runes[i+1][0]) {
			runes[i+1] = append([]rune{runes[i][len(runes[i])-1]}, runes[i+1]...)
			runes[i] = runes[i][:len(runes[i])-1]
		}
	}

	words := make([]string, 0, len(runes))
	for _, s := range runes {
		if len(s) > 0 {
			words = append(words, string(s))
		}
	}

	return words
}
