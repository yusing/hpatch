package router

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Collapse only adjacent, byte-identical runs of short token sequences. The
// bounded period search is independent of roles, commands and output formats;
// near-duplicates and intervening corrections are never treated as repetitions.
// Keep the first and last occurrence and label the omitted multiplicity. This
// remains lossy: positional occurrences may matter even when their bytes match.
func compactionPressureRepetitions(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(text) < 256 {
		return text, nil
	}
	if _, ok := compactionVisibleStringTokens(); !ok {
		return "", fmt.Errorf("cannot initialize compaction tokenizer")
	}
	_, pieces, err := compactionRetirementTokenCodec.Encode(text)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	start := 0
	for index := 0; index < len(pieces); {
		if index%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		end := index
		for period := 1; period <= 64 && index+period*8 <= len(pieces); period++ {
			unit := pieces[index : index+period]
			if !slices.Equal(unit, pieces[index+period:index+period*2]) {
				continue
			}
			count := 2
			for index+(count+1)*period <= len(pieces) &&
				slices.Equal(unit, pieces[index+count*period:index+(count+1)*period]) {
				count++
				if count%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return "", err
					}
				}
			}
			if count < 8 {
				continue
			}
			sample := strings.Join(unit, "")
			if len(sample)*(count-2) < 256 || !utf8.ValidString(sample) {
				continue
			}
			marker := fmt.Sprintf("\n[mekugi repetition: %d identical adjacent occurrences omitted]\n", count-2)
			_, markerPieces, err := compactionRetirementTokenCodec.Encode(marker)
			if err != nil {
				return "", err
			}
			if period*(count-2) <= len(markerPieces)+4 {
				continue
			}
			out.WriteString(strings.Join(pieces[start:index+period], ""))
			out.WriteString(marker)
			out.WriteString(sample)
			end = index + count*period
			start = end
			break
		}
		if end > index {
			index = end
		} else {
			index++
		}
	}
	if start == 0 {
		return text, nil
	}
	out.WriteString(strings.Join(pieces[start:], ""))
	result := out.String()
	_, resultPieces, err := compactionRetirementTokenCodec.Encode(result)
	if err != nil {
		return "", err
	}
	if len(resultPieces) >= len(pieces) {
		return text, nil
	}
	return result, nil
}
