package capturer

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

// contentTokens counts decoded JSON keys and scalar values independently, not
// their wire spelling or JSON punctuation. Strings containing JSON/code remain
// literal text: escaping inside model-visible content is real token overhead.
// This is a local content estimate, never provider billing usage.
func contentTokens(payload []byte, codec tokenizer.Codec) (int, error) {
	if capturedPayloadLooksLikeSSE(payload) {
		payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
		total := 0
		var data [][]byte
		flush := func() error {
			if len(data) == 0 {
				return nil
			}
			body := bytes.Join(data, []byte{'\n'})
			data = nil
			if bytes.Equal(bytes.TrimSpace(body), []byte("[DONE]")) {
				return nil
			}
			n, err := contentTokens(body, codec)
			total += n
			return err
		}
		for line := range bytes.SplitSeq(payload, []byte{'\n'}) {
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) == 0 {
				if err := flush(); err != nil {
					return 0, err
				}
				continue
			}
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				data = append(data, bytes.TrimPrefix(value, []byte{' '}))
			}
		}
		if err := flush(); err != nil {
			return 0, err
		}
		return total, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return codec.Count(string(payload))
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return codec.Count(string(payload))
	}
	return decodedTokens(value, codec)
}

func decodedTokens(value any, codec tokenizer.Codec) (int, error) {
	switch value := value.(type) {
	case map[string]any:
		total := 0
		for key, child := range value {
			n, err := codec.Count(key)
			if err != nil {
				return 0, err
			}
			total += n
			n, err = decodedTokens(child, codec)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	case []any:
		total := 0
		for _, child := range value {
			n, err := decodedTokens(child, codec)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	case string:
		return codec.Count(value)
	case json.Number:
		return codec.Count(normalizedNumber(string(value)))
	case bool:
		if value {
			return codec.Count("true")
		}
		return codec.Count("false")
	default:
		return codec.Count("null")
	}
}

// Normalize decimal spelling without float rounding or exponent-sized allocation.
func normalizedNumber(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	mantissa, exponent, _ := strings.Cut(strings.ToLower(value), "e")
	var power big.Int
	if exponent != "" {
		power.SetString(exponent, 10)
	}
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		power.Sub(&power, big.NewInt(int64(len(mantissa)-dot-1)))
		mantissa = mantissa[:dot] + mantissa[dot+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0"
	}
	digits := strings.TrimRight(mantissa, "0")
	power.Add(&power, big.NewInt(int64(len(mantissa)-len(digits))))
	if power.Sign() == 0 {
		return sign + digits
	}
	return sign + digits + "e" + power.String()
}

// Only assistant output_text participates in CTP output compression. Tool-call
// translation, reasoning, and generated commentary are not compression savings.
func measureOutputText(output []byte, codec tokenizer.Codec) (payloadMetrics, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(output, &rawItems); err != nil {
		return payloadMetrics{}, err
	}
	var result payloadMetrics
	for _, raw := range rawItems {
		var item struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if part.Type != "output_text" {
				continue
			}
			n, err := codec.Count(part.Text)
			if err != nil {
				return payloadMetrics{}, err
			}
			result.Bytes += uint64(len(part.Text))
			result.Tokens += uint64(n)
		}
	}
	return result, nil
}
